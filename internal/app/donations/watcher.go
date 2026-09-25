package donations

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"go.uber.org/zap"

	"telesrv/internal/domain"
)

// erc20TransferSig is keccak256("Transfer(address,address,uint256)"), the
// standard ERC-20 Transfer event topic0 every USDT/USDC deposit is found by.
var erc20TransferSig = common.HexToHash("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef")

// maxScanBatchBlocks caps how many blocks one pass advances by, so a
// watcher that's behind (first run, or recovering after downtime) catches
// up in bounded steps rather than one huge eth_getLogs/BlockByNumber burst.
const maxScanBatchBlocks = 2000

// WatchChain runs the watcher for one enabled, watchable chain until ctx is
// canceled: poll for new blocks, record deposits to known user addresses
// (native transfers by scanning block transactions, ERC-20 transfers via
// eth_getLogs), advance every pending deposit's confirmation count, and
// credit Stars for anything that reaches the chain's confirmation depth.
// Call it in its own goroutine per chain -- it blocks until ctx is done or
// an unrecoverable setup error occurs.
func (s *Service) WatchChain(ctx context.Context, chainKey string, pollInterval time.Duration, log *zap.Logger) error {
	if s == nil || s.store == nil {
		return domain.ErrDonationWalletNotConfigured
	}
	chain, found, err := s.store.DonationChain(ctx, chainKey)
	if err != nil {
		return err
	}
	if !found {
		return domain.ErrDonationChainNotFound
	}
	if !chain.Watchable() {
		return domain.ErrDonationChainDisabled
	}

	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	if log == nil {
		log = zap.NewNop()
	}
	log = log.With(zap.String("donation_chain", chain.Key))
	log.Info("donation watcher started", zap.Int64("chain_id", chain.ChainID), zap.Int("confirmations_required", chain.ConfirmationsRequired))

	// client is dialed lazily and re-dialed whenever the operator edits the
	// RPC endpoint, since the config below is re-read every pass.
	var client *ethclient.Client
	dialedRPC := ""
	defer func() {
		if client != nil {
			client.Close()
		}
	}()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		// Re-read chain config and tokens every pass rather than capturing
		// them once at startup: an operator fixing a wrong USD rate, adding
		// a stablecoin contract, deepening confirmations or repointing the
		// RPC in the admin panel must take effect on the next tick, not on
		// the next server restart. Getting this wrong is not a cosmetic
		// staleness bug -- a chain whose rate was still 0/too low prices
		// every deposit to zero Stars and silently refuses to credit it (see
		// refreshConfirmationsAndCredit), so the fix would appear to do
		// nothing until someone restarted the process.
		chain, found, err = s.store.DonationChain(ctx, chainKey)
		switch {
		case err != nil:
			log.Warn("reload donation chain config failed; keeping previous config for this pass", zap.Error(err))
		case !found:
			log.Info("donation chain removed; watcher stopping")
			return nil
		case !chain.Watchable():
			log.Info("donation chain disabled or misconfigured; watcher stopping")
			return nil
		}

		watchedTokens, err := s.watchableTokens(ctx, chainKey)
		if err != nil {
			log.Warn("reload donation tokens failed; skipping token scan this pass", zap.Error(err))
		}

		if client == nil || dialedRPC != chain.RPCURL {
			if client != nil {
				client.Close()
				client = nil
			}
			dialed, derr := ethclient.DialContext(ctx, chain.RPCURL)
			if derr != nil {
				if errors.Is(derr, context.Canceled) {
					return ctx.Err()
				}
				log.Warn("dial donation chain RPC failed; retrying next pass", zap.Error(derr))
			} else {
				client, dialedRPC = dialed, chain.RPCURL
			}
		}

		if client != nil {
			if err := s.scanOnce(ctx, client, chain, watchedTokens, log); err != nil && !errors.Is(err, context.Canceled) {
				log.Warn("donation scan pass failed", zap.Error(err))
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// watchableTokens lists the chain's stablecoin rows that actually have a
// contract address filled in.
func (s *Service) watchableTokens(ctx context.Context, chainKey string) ([]domain.DonationToken, error) {
	tokens, err := s.store.DonationTokens(ctx, chainKey)
	if err != nil {
		return nil, err
	}
	out := make([]domain.DonationToken, 0, len(tokens))
	for _, t := range tokens {
		if t.Watchable() {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *Service) scanOnce(ctx context.Context, client *ethclient.Client, chain domain.DonationChain, tokens []domain.DonationToken, log *zap.Logger) error {
	latest, err := client.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("get latest block: %w", err)
	}
	cursor, err := s.store.DonationChainCursor(ctx, chain.Key)
	if err != nil {
		return err
	}
	if cursor > int64(latest) {
		// The cursor is ahead of the chain's own tip -- impossible while
		// following one healthy chain, so the node behind this RPC is not
		// the one this cursor was built against (repointed endpoint, a
		// reset dev chain, a node still syncing from behind). Left alone,
		// every later pass computes an empty range and the watcher silently
		// stops detecting anything, forever. Resync to the tip instead and
		// say so.
		log.Warn("donation chain cursor is ahead of the chain tip; resyncing to tip",
			zap.Int64("cursor", cursor), zap.Uint64("latest_block", latest))
		if err := s.store.SetDonationChainCursor(ctx, chain.Key, int64(latest)); err != nil {
			return err
		}
		cursor = int64(latest)
	}
	from := uint64(cursor) + 1
	if cursor == 0 {
		// First run ever: don't replay the chain's entire history looking
		// for deposits to addresses that (mostly) didn't exist yet. Start
		// from the current tip.
		from = latest
	}
	to := latest
	if to < from {
		return s.refreshConfirmationsAndCredit(ctx, chain, latest, log)
	}
	if to-from > maxScanBatchBlocks {
		to = from + maxScanBatchBlocks
	}

	if err := s.scanNativeTransfers(ctx, client, chain, from, to, log); err != nil {
		return fmt.Errorf("scan native transfers: %w", err)
	}
	for _, token := range tokens {
		if err := s.scanTokenTransfers(ctx, client, chain, token, from, to, log); err != nil {
			return fmt.Errorf("scan %s transfers: %w", token.Symbol, err)
		}
	}
	if err := s.store.SetDonationChainCursor(ctx, chain.Key, int64(to)); err != nil {
		return err
	}
	return s.refreshConfirmationsAndCredit(ctx, chain, latest, log)
}

func (s *Service) scanNativeTransfers(ctx context.Context, client *ethclient.Client, chain domain.DonationChain, from, to uint64, log *zap.Logger) error {
	for bn := from; bn <= to; bn++ {
		block, err := client.BlockByNumber(ctx, new(big.Int).SetUint64(bn))
		if err != nil {
			return fmt.Errorf("fetch block %d: %w", bn, err)
		}
		for _, tx := range block.Transactions() {
			toAddr := tx.To()
			if toAddr == nil || tx.Value() == nil || tx.Value().Sign() <= 0 {
				continue // contract creation, or a zero-value call
			}
			userID, isOurs, err := s.store.DonationUserByAddress(ctx, normalizeAddress(toAddr.Hex()))
			if err != nil {
				return err
			}
			if !isOurs {
				continue
			}
			if err := s.recordDeposit(ctx, chain, userID, "", tx.Hash().Hex(), -1, int64(bn), tx.Value().String(), log); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) scanTokenTransfers(ctx context.Context, client *ethclient.Client, chain domain.DonationChain, token domain.DonationToken, from, to uint64, log *zap.Logger) error {
	logs, err := client.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(from),
		ToBlock:   new(big.Int).SetUint64(to),
		Addresses: []common.Address{common.HexToAddress(token.ContractAddress)},
		Topics:    [][]common.Hash{{erc20TransferSig}},
	})
	if err != nil {
		return fmt.Errorf("filter %s logs: %w", token.Symbol, err)
	}
	for _, lg := range logs {
		if lg.Removed || len(lg.Topics) < 3 || len(lg.Data) == 0 {
			continue // Removed: a reorg already dropped this log; incomplete: not a standard Transfer.
		}
		toAddr := common.HexToAddress(lg.Topics[2].Hex())
		userID, isOurs, err := s.store.DonationUserByAddress(ctx, normalizeAddress(toAddr.Hex()))
		if err != nil {
			return err
		}
		if !isOurs {
			continue
		}
		amount := new(big.Int).SetBytes(lg.Data)
		if amount.Sign() <= 0 {
			continue
		}
		if err := s.recordDeposit(ctx, chain, userID, token.Symbol, lg.TxHash.Hex(), int(lg.Index), int64(lg.BlockNumber), amount.String(), log); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) recordDeposit(ctx context.Context, chain domain.DonationChain, userID int64, tokenSymbol, txHash string, logIndex int, blockNumber int64, amountRaw string, log *zap.Logger) error {
	deposit := domain.DonationDeposit{
		UserID: userID, ChainKey: chain.Key, TokenSymbol: tokenSymbol,
		TxHash: strings.ToLower(txHash), LogIndex: logIndex, BlockNumber: blockNumber,
		AmountRaw: amountRaw, Status: domain.DonationDepositPending,
	}
	recorded, created, err := s.store.RecordDonationDeposit(ctx, deposit)
	if err != nil {
		return err
	}
	if created {
		log.Info("donation deposit detected", zap.Int64("user_id", userID), zap.String("tx_hash", recorded.TxHash),
			zap.String("token", tokenSymbol), zap.String("amount_raw", amountRaw))
	}
	return nil
}

// refreshConfirmationsAndCredit advances every still-pending deposit's
// confirmation count against the latest known block height, then credits
// Stars for anything that has just reached (or previously reached, e.g.
// after a restart) the chain's required depth.
func (s *Service) refreshConfirmationsAndCredit(ctx context.Context, chain domain.DonationChain, latest uint64, log *zap.Logger) error {
	pending, err := s.store.PendingDonationDeposits(ctx, chain.Key)
	if err != nil {
		return err
	}
	for _, d := range pending {
		if d.BlockNumber > int64(latest) {
			continue // a log/tx from a block this pass hasn't fully confirmed as latest yet
		}
		confirmations := int(int64(latest) - d.BlockNumber + 1)
		if _, err := s.store.UpdateDonationDepositConfirmations(ctx, chain.Key, d.TxHash, d.LogIndex, confirmations); err != nil {
			return err
		}
	}

	confirmed, err := s.store.ConfirmedUncreditedDeposits(ctx, chain.Key, 100)
	if err != nil {
		return err
	}
	if len(confirmed) == 0 {
		return nil
	}
	tokensByChain, err := s.store.DonationTokens(ctx, chain.Key)
	if err != nil {
		return err
	}
	tokenDecimals := make(map[string]int, len(tokensByChain))
	for _, t := range tokensByChain {
		tokenDecimals[t.Symbol] = t.Decimals
	}
	for _, d := range confirmed {
		decimals := chain.NativeDecimals
		if d.TokenSymbol != "" {
			decimals = tokenDecimals[d.TokenSymbol]
		}
		amount, ok := new(big.Int).SetString(d.AmountRaw, 10)
		if !ok {
			log.Error("donation deposit has unparseable amount, skipping credit", zap.Int64("deposit_id", d.ID), zap.String("amount_raw", d.AmountRaw))
			continue
		}
		// Stablecoins are pegged 1:1 to USD; native currency uses the
		// chain's manual admin-set rate until a Chainlink feed is wired in
		// (see domain.DonationChain.PriceFeedAddress).
		rateMicros := int64(microsPerWhole)
		if d.TokenSymbol == "" {
			rateMicros = chain.ManualUSDRateMicros
		}
		usdMicros := usdMicrosForAmount(amount, decimals, rateMicros)
		stars := s.starsForUSDMicros(usdMicros)
		if stars <= 0 {
			log.Warn("donation deposit priced to zero stars, skipping credit", zap.Int64("deposit_id", d.ID), zap.Int64("usd_micros", usdMicros))
			continue
		}
		credited, ok, err := s.store.CreditDonationDeposit(ctx, d.ID, usdMicros, stars, int(time.Now().Unix()))
		if err != nil {
			return err
		}
		if ok {
			log.Info("donation deposit credited", zap.Int64("user_id", d.UserID), zap.Int64("deposit_id", d.ID),
				zap.Int64("stars", credited.StarsCredited), zap.Int64("usd_micros", credited.USDValueMicros))
			if s.notifier != nil {
				assetSymbol := chain.NativeSymbol
				if d.TokenSymbol != "" {
					assetSymbol = d.TokenSymbol
				}
				notifyDeposit := d
				notifyDeposit.Status = domain.DonationDepositCredited
				notifyDeposit.StarsCredited = credited.StarsCredited
				notifyDeposit.USDValueMicros = credited.USDValueMicros
				s.notifier.NotifyDonationCredited(ctx, domain.DonationCreditNotice{
					UserID: d.UserID, ChainName: chain.Name, AssetSymbol: assetSymbol,
					AssetDecimals: decimals, Deposit: notifyDeposit,
				})
			}
		}
	}
	return nil
}
