package donations

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"telesrv/internal/domain"
)

// nativeTransferGasLimit is exactly what a plain value transfer to an EOA
// costs on every EVM chain -- never more, never less. Sweeping to a
// contract address that needs more gas than this in its receive/fallback
// isn't supported; destination is expected to be an operator-owned wallet,
// not a contract.
const nativeTransferGasLimit = uint64(21000)

var (
	erc20TransferSelector  = common.FromHex("0xa9059cbb") // transfer(address,uint256)
	erc20BalanceOfSelector = common.FromHex("0x70a08231") // balanceOf(address)
)

// PreviewSweep computes exactly what Sweep would do -- every address's
// native and token balance on chainKey, the amount that would actually move
// after reserving gas, and which ones would be skipped for lack of gas --
// without signing or broadcasting a single transaction. This is the dry-run
// half of internal/admin.Service.SweepDonationChain's confirm flow.
func (s *Service) PreviewSweep(ctx context.Context, chainKey, destination string) (domain.DonationSweepResult, error) {
	return s.sweep(ctx, chainKey, destination, false)
}

// Sweep moves every deposit address's balance (native currency and any
// watchable token) on chainKey to destination: an operator-triggered,
// manual-only withdrawal (see docs/donations.md's custody notes) -- nothing
// in the automatic deposit-watching path ever calls this. Only ever call it
// from the confirm half of a dry-run/confirm flow; see PreviewSweep.
//
// Gas for a token transfer always comes out of that SAME address's own
// native balance, never the destination's or any other address's -- an
// address holding only a stablecoin and no native currency for gas is
// reported as skipped, not auto-funded (auto-funding would move MORE money
// through MORE transactions than the operator asked for). Tokens are swept
// before native on each address, since a token transfer's gas is spent out
// of the native balance that would otherwise be swept away first.
func (s *Service) Sweep(ctx context.Context, chainKey, destination string) (domain.DonationSweepResult, error) {
	return s.sweep(ctx, chainKey, destination, true)
}

func (s *Service) sweep(ctx context.Context, chainKey, destination string, execute bool) (domain.DonationSweepResult, error) {
	if s == nil || s.store == nil {
		return domain.DonationSweepResult{}, domain.ErrDonationWalletNotConfigured
	}
	if execute && !s.Ready() {
		return domain.DonationSweepResult{}, domain.ErrDonationWalletNotConfigured
	}
	destination = strings.ToLower(strings.TrimSpace(destination))
	if !common.IsHexAddress(destination) {
		return domain.DonationSweepResult{}, domain.ErrDonationSweepDestination
	}
	destAddr := common.HexToAddress(destination)

	chain, found, err := s.store.DonationChain(ctx, chainKey)
	if err != nil {
		return domain.DonationSweepResult{}, err
	}
	if !found || !chain.Watchable() {
		return domain.DonationSweepResult{}, domain.ErrDonationChainDisabled
	}
	tokens, err := s.store.DonationTokens(ctx, chainKey)
	if err != nil {
		return domain.DonationSweepResult{}, err
	}
	watchedTokens := make([]domain.DonationToken, 0, len(tokens))
	for _, t := range tokens {
		if !t.Watchable() {
			continue
		}
		// Refuse to sweep INTO a token's own contract address -- almost
		// certainly a typo, and would burn whatever was sent.
		if strings.EqualFold(t.ContractAddress, destination) {
			return domain.DonationSweepResult{}, domain.ErrDonationSweepDestination
		}
		watchedTokens = append(watchedTokens, t)
	}

	addresses, err := s.store.ListDonationAddresses(ctx)
	if err != nil {
		return domain.DonationSweepResult{}, err
	}

	client, err := ethclient.DialContext(ctx, chain.RPCURL)
	if err != nil {
		return domain.DonationSweepResult{}, fmt.Errorf("donations: dial %s: %w", chain.Key, err)
	}
	defer client.Close()

	chainIDBig := big.NewInt(chain.ChainID)
	signer := types.NewEIP155Signer(chainIDBig)
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return domain.DonationSweepResult{}, fmt.Errorf("donations: suggest gas price: %w", err)
	}
	nativeGasCost := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(nativeTransferGasLimit))

	result := domain.DonationSweepResult{ChainKey: chain.Key, Destination: destination}
	for _, addr := range addresses {
		fromAddr := common.HexToAddress(addr.Address)
		if fromAddr == destAddr {
			continue // sweeping an address into itself is a no-op
		}

		var privKey *ecdsa.PrivateKey
		loadSigner := func() (*ecdsa.PrivateKey, error) {
			if privKey != nil {
				return privKey, nil
			}
			hexKey, err := s.wallet.PrivateKeyHex(addr.DerivationIndex)
			if err != nil {
				return nil, err
			}
			pk, err := crypto.HexToECDSA(hexKey)
			if err != nil {
				return nil, fmt.Errorf("donations: parse derived private key: %w", err)
			}
			privKey = pk
			return pk, nil
		}
		// Nonces start from the last MINED one, not the pending one, so a
		// re-run replaces a sweep still stuck in the mempool instead of
		// queueing another one behind it.
		//
		// A sweep sends an address's whole balance, so two queued sweeps can
		// never both be funded: the node counts the pending one's cost
		// against the balance and rejects the new one with "insufficient
		// funds ... queued cost <balance>, overshot <balance>". With a
		// pending nonce that rejection is permanent -- every later attempt
		// queues yet another doomed transaction. Reusing the stuck nonce
		// replaces it instead, which is what an operator pressing Sweep
		// again actually means. Within one run the counter still increments
		// locally, so an address's token transfer and its native transfer
		// keep their order.
		var nonce uint64
		haveNonce := false
		replacing := false
		nextNonce := func() (uint64, error) {
			if !haveNonce {
				mined, err := client.NonceAt(ctx, fromAddr, nil)
				if err != nil {
					return 0, err
				}
				pending, err := client.PendingNonceAt(ctx, fromAddr)
				if err != nil {
					return 0, err
				}
				replacing = pending > mined
				nonce, haveNonce = mined, true
			}
			n := nonce
			nonce++
			return n, nil
		}
		// gasPriceFor prices one transaction. A replacement must outbid the
		// transaction it replaces or the node refuses it outright
		// ("replacement transaction underpriced"), and the stuck one's own
		// price is unknown here, so a replacement is deliberately overbid.
		gasPriceFor := func() *big.Int {
			if !replacing {
				return gasPrice
			}
			bumped := new(big.Int).Mul(gasPrice, big.NewInt(125))
			return bumped.Div(bumped, big.NewInt(100))
		}

		// Tokens first: their gas comes out of this address's OWN native
		// balance, which the native sweep below would otherwise take all of.
		for _, token := range watchedTokens {
			tokenAddr := common.HexToAddress(token.ContractAddress)
			tokenBalance, err := erc20BalanceOf(ctx, client, tokenAddr, fromAddr)
			if err != nil {
				return domain.DonationSweepResult{}, fmt.Errorf("donations: read %s balance for %s: %w", token.Symbol, addr.Address, err)
			}
			if tokenBalance.Sign() <= 0 {
				continue
			}
			transferData := erc20TransferCallData(destAddr, tokenBalance)
			gasLimit, err := client.EstimateGas(ctx, ethereum.CallMsg{From: fromAddr, To: &tokenAddr, Data: transferData})
			if err != nil {
				// Most often means "not enough native balance to even try" --
				// go-ethereum's estimator itself checks the caller can cover
				// gas*price. Report and move on rather than failing the
				// whole sweep over one address.
				result.Entries = append(result.Entries, domain.DonationSweepEntry{
					Address: addr.Address, TokenSymbol: token.Symbol, AmountRaw: tokenBalance.String(),
					Skipped: true, Reason: "gas estimation failed (likely insufficient native balance for gas): " + err.Error(),
				})
				continue
			}
			tokenGasCost := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasLimit))
			nativeBalance, err := client.BalanceAt(ctx, fromAddr, nil)
			if err != nil {
				return domain.DonationSweepResult{}, fmt.Errorf("donations: read native balance for %s: %w", addr.Address, err)
			}
			if nativeBalance.Cmp(tokenGasCost) < 0 {
				result.Entries = append(result.Entries, domain.DonationSweepEntry{
					Address: addr.Address, TokenSymbol: token.Symbol, AmountRaw: tokenBalance.String(),
					Skipped: true, Reason: "not enough native balance at this address to pay for the transfer's gas",
				})
				continue
			}
			if !execute {
				result.Entries = append(result.Entries, domain.DonationSweepEntry{
					Address: addr.Address, TokenSymbol: token.Symbol, AmountRaw: tokenBalance.String(),
				})
				continue
			}
			pk, err := loadSigner()
			if err != nil {
				return domain.DonationSweepResult{}, err
			}
			n, err := nextNonce()
			if err != nil {
				return domain.DonationSweepResult{}, err
			}
			tx := types.NewTx(&types.LegacyTx{
				Nonce: n, To: &tokenAddr, Value: big.NewInt(0), Gas: gasLimit, GasPrice: gasPriceFor(), Data: transferData,
			})
			signed, err := types.SignTx(tx, signer, pk)
			if err != nil {
				return domain.DonationSweepResult{}, fmt.Errorf("donations: sign %s sweep for %s: %w", token.Symbol, addr.Address, err)
			}
			if err := client.SendTransaction(ctx, signed); err != nil {
				return domain.DonationSweepResult{}, fmt.Errorf("donations: broadcast %s sweep for %s: %w", token.Symbol, addr.Address, err)
			}
			result.Entries = append(result.Entries, domain.DonationSweepEntry{
				Address: addr.Address, TokenSymbol: token.Symbol, AmountRaw: tokenBalance.String(), TxHash: signed.Hash().Hex(),
			})
		}

		// Native last: read fresh, since a token sweep just above may have
		// spent some of it on gas.
		nativeBalance, err := client.BalanceAt(ctx, fromAddr, nil)
		if err != nil {
			return domain.DonationSweepResult{}, fmt.Errorf("donations: read native balance for %s: %w", addr.Address, err)
		}
		if nativeBalance.Sign() <= 0 {
			continue
		}
		// The preview is priced at the plain suggestion; only an execution
		// knows whether it is replacing a stuck transaction, and finding that
		// out costs two RPC calls per address.
		if preview := new(big.Int).Sub(nativeBalance, nativeGasCost); preview.Sign() <= 0 {
			result.Entries = append(result.Entries, domain.DonationSweepEntry{
				Address: addr.Address, AmountRaw: nativeBalance.String(),
				Skipped: true, Reason: "balance doesn't cover its own transfer gas",
			})
			continue
		} else if !execute {
			result.Entries = append(result.Entries, domain.DonationSweepEntry{Address: addr.Address, AmountRaw: preview.String()})
			continue
		}
		pk, err := loadSigner()
		if err != nil {
			return domain.DonationSweepResult{}, err
		}
		// nextNonce before gasPriceFor: it is what resolves whether this is a
		// replacement, and a replacement is priced higher.
		n, err := nextNonce()
		if err != nil {
			return domain.DonationSweepResult{}, err
		}
		nativePrice := gasPriceFor()
		gasCost := new(big.Int).Mul(nativePrice, new(big.Int).SetUint64(nativeTransferGasLimit))
		sendable := new(big.Int).Sub(nativeBalance, gasCost)
		if sendable.Sign() <= 0 {
			result.Entries = append(result.Entries, domain.DonationSweepEntry{
				Address: addr.Address, AmountRaw: nativeBalance.String(),
				Skipped: true, Reason: "balance doesn't cover its own transfer gas at the replacement price",
			})
			continue
		}
		to := destAddr
		tx := types.NewTx(&types.LegacyTx{Nonce: n, To: &to, Value: sendable, Gas: nativeTransferGasLimit, GasPrice: nativePrice})
		signed, err := types.SignTx(tx, signer, pk)
		if err != nil {
			return domain.DonationSweepResult{}, fmt.Errorf("donations: sign native sweep for %s: %w", addr.Address, err)
		}
		if err := client.SendTransaction(ctx, signed); err != nil {
			return domain.DonationSweepResult{}, fmt.Errorf("donations: broadcast native sweep for %s: %w", addr.Address, err)
		}
		result.Entries = append(result.Entries, domain.DonationSweepEntry{
			Address: addr.Address, AmountRaw: sendable.String(), TxHash: signed.Hash().Hex(),
		})
	}
	return result, nil
}

func erc20TransferCallData(to common.Address, amount *big.Int) []byte {
	data := make([]byte, 4+32+32)
	copy(data[0:4], erc20TransferSelector)
	copy(data[4+12:4+32], to.Bytes())
	amount.FillBytes(data[36:68])
	return data
}

func erc20BalanceOf(ctx context.Context, client *ethclient.Client, token, owner common.Address) (*big.Int, error) {
	data := make([]byte, 4+32)
	copy(data[0:4], erc20BalanceOfSelector)
	copy(data[4+12:4+32], owner.Bytes())
	out, err := client.CallContract(ctx, ethereum.CallMsg{To: &token, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return big.NewInt(0), nil
	}
	return new(big.Int).SetBytes(out), nil
}
