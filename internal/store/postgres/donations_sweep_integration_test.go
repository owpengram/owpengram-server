package postgres

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"

	"telesrv/internal/app/donations"
	"telesrv/internal/domain"
)

// TestDonationsSweepGanache proves the whole operator-triggered sweep end to
// end against a real Ganache node: derive a real deposit address, fund it
// with a real transaction, preview the sweep (must show the balance and
// sign/broadcast nothing), execute it for real, and confirm the destination
// actually received the funds and the source address is left near zero.
// Skips (not fails) if Ganache isn't reachable -- this is a real blockchain
// integration test, not a mock.
func TestDonationsSweepGanache(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	client, err := ethclient.DialContext(ctx, testGanacheURL())
	if err != nil {
		t.Skip("ganache not reachable at " + testGanacheURL() + ": " + err.Error())
	}
	if _, err := client.ChainID(ctx); err != nil {
		t.Skip("ganache not responding: " + err.Error())
	}

	ensureGanacheChain(t, ctx, pool)
	suffix := randomSuffix(t)
	testKey, err := donations.ParseEncryptionKey(testDonationWalletKey)
	if err != nil {
		t.Fatalf("parse test wallet key: %v", err)
	}
	donationStore := NewDonationStore(pool)
	svc, err := donations.NewService(ctx, donationStore, testKey)
	if err != nil {
		t.Fatalf("new donations service: %v", err)
	}
	if !svc.Ready() {
		if _, err := svc.EnsureWallet(ctx); err != nil {
			t.Fatalf("ensure wallet: %v", err)
		}
	}

	users := NewUserStore(pool)
	donor, err := users.Create(ctx, domain.User{AccessHash: 72, Phone: "+1778" + suffix + "72", FirstName: "SweepDonor"})
	if err != nil {
		t.Fatalf("create donor: %v", err)
	}
	address, err := svc.AddressForUser(ctx, donor.ID)
	if err != nil || address == "" {
		t.Fatalf("assign donation address: %v (address=%q)", err, address)
	}
	t.Logf("sweep source address: %s", address)

	var accounts []string
	if err := client.Client().CallContext(ctx, &accounts, "eth_accounts"); err != nil {
		t.Fatalf("eth_accounts: %v", err)
	}
	if len(accounts) < 2 {
		t.Fatal("ganache reports fewer than 2 accounts")
	}
	destination := accounts[1]
	destBefore, err := client.BalanceAt(ctx, common.HexToAddress(destination), nil)
	if err != nil {
		t.Fatalf("read destination balance before: %v", err)
	}

	// Fund the deposit address with a real 1 ETH transaction, exactly like a
	// donor would.
	const fundAmountWei = "0xDE0B6B3A7640000" // 1 ETH
	var fundTxHash string
	if err := client.Client().CallContext(ctx, &fundTxHash, "eth_sendTransaction", map[string]string{
		"from": accounts[0], "to": address, "value": fundAmountWei,
	}); err != nil {
		t.Fatalf("fund deposit address: %v", err)
	}
	t.Logf("funded deposit address in tx %s", fundTxHash)
	waitForNonzeroBalance(t, client, ctx, address)

	preview, err := svc.PreviewSweep(ctx, "ganache", destination)
	if err != nil {
		t.Fatalf("preview sweep: %v", err)
	}
	entry := findSweepEntry(preview.Entries, address, "")
	if entry == nil {
		t.Fatalf("preview sweep entries = %+v, want an entry for %s", preview.Entries, address)
	}
	if entry.Skipped || entry.TxHash != "" {
		t.Fatalf("preview entry = %+v, want not skipped and no tx hash (preview must sign/broadcast nothing)", entry)
	}
	previewAmount, ok := new(big.Int).SetString(entry.AmountRaw, 10)
	if !ok || previewAmount.Sign() <= 0 {
		t.Fatalf("preview amount = %q, want a positive integer", entry.AmountRaw)
	}
	t.Logf("preview would sweep %s wei", previewAmount)

	// Preview must not have moved anything for real.
	sourceAfterPreview, err := client.BalanceAt(ctx, common.HexToAddress(address), nil)
	if err != nil {
		t.Fatalf("read source balance after preview: %v", err)
	}
	fundedAmount, _ := new(big.Int).SetString("1000000000000000000", 10)
	if sourceAfterPreview.Cmp(fundedAmount) != 0 {
		t.Fatalf("source balance after preview = %s, want unchanged at %s wei (preview must not broadcast)", sourceAfterPreview, fundedAmount)
	}

	result, err := svc.Sweep(ctx, "ganache", destination)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	swept := findSweepEntry(result.Entries, address, "")
	if swept == nil || swept.Skipped || swept.TxHash == "" {
		t.Fatalf("sweep entry = %+v, want a broadcast tx hash", swept)
	}
	sweptAmount, ok := new(big.Int).SetString(swept.AmountRaw, 10)
	if !ok {
		t.Fatalf("swept amount = %q, not parseable", swept.AmountRaw)
	}
	if sweptAmount.Cmp(previewAmount) != 0 {
		t.Fatalf("swept amount %s != previewed amount %s", sweptAmount, previewAmount)
	}

	// >= rather than ==: Sweep moves every known donation address in one
	// call, not just this test's own -- other addresses left over from
	// earlier test runs against this same shared test database may hold
	// their own unswept balance and land in the same destination in the
	// same call, which only ever adds to what this address alone
	// contributed, never subtracts from it.
	deadline := time.Now().Add(15 * time.Second)
	for {
		destAfter, err := client.BalanceAt(ctx, common.HexToAddress(destination), nil)
		if err != nil {
			t.Fatalf("read destination balance after sweep: %v", err)
		}
		gained := new(big.Int).Sub(destAfter, destBefore)
		if gained.Cmp(sweptAmount) >= 0 {
			t.Logf("destination gained at least the swept amount: %s wei (>= %s)", gained, sweptAmount)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("destination balance gained %s, want at least the swept %s", gained, sweptAmount)
		}
		time.Sleep(200 * time.Millisecond)
	}

	sourceAfterSweep, err := client.BalanceAt(ctx, common.HexToAddress(address), nil)
	if err != nil {
		t.Fatalf("read source balance after sweep: %v", err)
	}
	if sourceAfterSweep.Sign() < 0 || sourceAfterSweep.Cmp(fundedAmount) >= 0 {
		t.Fatalf("source balance after sweep = %s, want much less than the original %s", sourceAfterSweep, fundedAmount)
	}

	// A second sweep finds nothing left worth moving.
	previewAgain, err := svc.PreviewSweep(ctx, "ganache", destination)
	if err != nil {
		t.Fatalf("second preview: %v", err)
	}
	if again := findSweepEntry(previewAgain.Entries, address, ""); again != nil && !again.Skipped {
		t.Fatalf("second preview still lists a sendable entry for %s: %+v, want it swept dry", address, again)
	}
}

// TestDonationsChainBalanceGanache proves ChainBalance reflects a real
// on-chain deposit: fund a fresh derived address with a real transaction,
// then confirm the native-currency total it reports is at least that
// amount (>=, not ==, since other addresses left over from earlier tests
// against this same shared test database may also hold an unswept
// balance -- see TestDonationsSweepGanache's identical caveat).
func TestDonationsChainBalanceGanache(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	client, err := ethclient.DialContext(ctx, testGanacheURL())
	if err != nil {
		t.Skip("ganache not reachable at " + testGanacheURL() + ": " + err.Error())
	}
	if _, err := client.ChainID(ctx); err != nil {
		t.Skip("ganache not responding: " + err.Error())
	}

	ensureGanacheChain(t, ctx, pool)
	suffix := randomSuffix(t)
	testKey, err := donations.ParseEncryptionKey(testDonationWalletKey)
	if err != nil {
		t.Fatalf("parse test wallet key: %v", err)
	}
	svc, err := donations.NewService(ctx, NewDonationStore(pool), testKey)
	if err != nil {
		t.Fatalf("new donations service: %v", err)
	}
	if !svc.Ready() {
		if _, err := svc.EnsureWallet(ctx); err != nil {
			t.Fatalf("ensure wallet: %v", err)
		}
	}

	users := NewUserStore(pool)
	donor, err := users.Create(ctx, domain.User{AccessHash: 73, Phone: "+1778" + suffix + "73", FirstName: "BalanceDonor"})
	if err != nil {
		t.Fatalf("create donor: %v", err)
	}
	address, err := svc.AddressForUser(ctx, donor.ID)
	if err != nil || address == "" {
		t.Fatalf("assign donation address: %v (address=%q)", err, address)
	}

	var accounts []string
	if err := client.Client().CallContext(ctx, &accounts, "eth_accounts"); err != nil {
		t.Fatalf("eth_accounts: %v", err)
	}
	const fundAmountWei = "0xDE0B6B3A7640000" // 1 ETH
	if err := client.Client().CallContext(ctx, new(string), "eth_sendTransaction", map[string]string{
		"from": accounts[0], "to": address, "value": fundAmountWei,
	}); err != nil {
		t.Fatalf("fund deposit address: %v", err)
	}
	waitForNonzeroBalance(t, client, ctx, address)

	balance, err := svc.ChainBalance(ctx, "ganache")
	if err != nil {
		t.Fatalf("chain balance: %v", err)
	}
	if balance.ChainKey != "ganache" {
		t.Fatalf("ChainKey = %q, want ganache", balance.ChainKey)
	}
	if len(balance.Assets) == 0 || balance.Assets[0].Symbol != "ETH" {
		t.Fatalf("Assets = %+v, want native ETH listed first", balance.Assets)
	}
	native := balance.Assets[0]
	total, ok := new(big.Int).SetString(native.TotalRaw, 10)
	oneETH, _ := new(big.Int).SetString("1000000000000000000", 10)
	if !ok || total.Cmp(oneETH) < 0 {
		t.Fatalf("native TotalRaw = %q, want at least 1 ETH (%s)", native.TotalRaw, oneETH)
	}
	if native.AddressCount < 1 {
		t.Fatalf("native AddressCount = %d, want at least 1", native.AddressCount)
	}
	if native.USDValueMicros <= 0 {
		t.Fatalf("native USDValueMicros = %d, want positive (ganache has a manual USD rate configured)", native.USDValueMicros)
	}
	if balance.TotalUSDValueMicros < native.USDValueMicros {
		t.Fatalf("TotalUSDValueMicros = %d, want at least the native asset's %d", balance.TotalUSDValueMicros, native.USDValueMicros)
	}
}

func findSweepEntry(entries []domain.DonationSweepEntry, address, tokenSymbol string) *domain.DonationSweepEntry {
	for i := range entries {
		if entries[i].Address == address && entries[i].TokenSymbol == tokenSymbol {
			return &entries[i]
		}
	}
	return nil
}

func waitForNonzeroBalance(t *testing.T, client *ethclient.Client, ctx context.Context, address string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		bal, err := client.BalanceAt(ctx, common.HexToAddress(address), nil)
		if err != nil {
			t.Fatalf("read balance while waiting for funding: %v", err)
		}
		if bal.Sign() > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for funding transaction to land")
		}
		time.Sleep(200 * time.Millisecond)
	}
}
