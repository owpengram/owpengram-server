package donations

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"

	"telesrv/internal/domain"
)

// ChainBalance reads the live, on-chain sum of native currency and every
// watchable token across every known deposit address on chainKey -- one
// RPC round trip per address per asset, so it reflects exactly what's
// actually sitting on-chain right now, not what donation_deposits has
// gotten around to crediting. Read-only: never touches the wallet's
// private keys, so it works even before EnsureWallet/Ready.
func (s *Service) ChainBalance(ctx context.Context, chainKey string) (domain.DonationChainBalance, error) {
	if s == nil || s.store == nil {
		return domain.DonationChainBalance{}, domain.ErrDonationWalletNotConfigured
	}
	chain, found, err := s.store.DonationChain(ctx, chainKey)
	if err != nil {
		return domain.DonationChainBalance{}, err
	}
	if !found || !chain.Watchable() {
		return domain.DonationChainBalance{}, domain.ErrDonationChainDisabled
	}
	tokens, err := s.store.DonationTokens(ctx, chainKey)
	if err != nil {
		return domain.DonationChainBalance{}, err
	}
	watchedTokens := make([]domain.DonationToken, 0, len(tokens))
	for _, t := range tokens {
		if t.Watchable() {
			watchedTokens = append(watchedTokens, t)
		}
	}

	addresses, err := s.store.ListDonationAddresses(ctx)
	if err != nil {
		return domain.DonationChainBalance{}, err
	}

	client, err := ethclient.DialContext(ctx, chain.RPCURL)
	if err != nil {
		return domain.DonationChainBalance{}, fmt.Errorf("donations: dial %s: %w", chain.Key, err)
	}
	defer client.Close()

	nativeTotal := new(big.Int)
	nativeAddrCount := 0
	tokenTotals := make([]*big.Int, len(watchedTokens))
	tokenAddrCounts := make([]int, len(watchedTokens))
	for i := range tokenTotals {
		tokenTotals[i] = new(big.Int)
	}

	for _, addr := range addresses {
		a := common.HexToAddress(addr.Address)
		bal, err := client.BalanceAt(ctx, a, nil)
		if err != nil {
			return domain.DonationChainBalance{}, fmt.Errorf("donations: read native balance for %s: %w", addr.Address, err)
		}
		if bal.Sign() > 0 {
			nativeTotal.Add(nativeTotal, bal)
			nativeAddrCount++
		}
		for i, token := range watchedTokens {
			tbal, err := erc20BalanceOf(ctx, client, common.HexToAddress(token.ContractAddress), a)
			if err != nil {
				return domain.DonationChainBalance{}, fmt.Errorf("donations: read %s balance for %s: %w", token.Symbol, addr.Address, err)
			}
			if tbal.Sign() > 0 {
				tokenTotals[i].Add(tokenTotals[i], tbal)
				tokenAddrCounts[i]++
			}
		}
	}

	result := domain.DonationChainBalance{ChainKey: chain.Key}
	nativeUSD := usdMicrosForAmount(nativeTotal, chain.NativeDecimals, chain.ManualUSDRateMicros)
	result.Assets = append(result.Assets, domain.DonationChainAssetBalance{
		Symbol: chain.NativeSymbol, Decimals: chain.NativeDecimals, TotalRaw: nativeTotal.String(),
		AddressCount: nativeAddrCount, USDValueMicros: nativeUSD,
	})
	result.TotalUSDValueMicros += nativeUSD
	for i, token := range watchedTokens {
		// Stablecoins are priced 1:1 to USD, same as crediting (see pricing.go).
		tokenUSD := usdMicrosForAmount(tokenTotals[i], token.Decimals, microsPerWhole)
		result.Assets = append(result.Assets, domain.DonationChainAssetBalance{
			Symbol: token.Symbol, Decimals: token.Decimals, TotalRaw: tokenTotals[i].String(),
			AddressCount: tokenAddrCounts[i], USDValueMicros: tokenUSD,
		})
		result.TotalUSDValueMicros += tokenUSD
	}
	return result, nil
}
