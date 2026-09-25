package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"go.uber.org/zap/zaptest"

	"telesrv/internal/app/donations"
	"telesrv/internal/domain"
)

// TestDonationsCreateChainStartsWatcherWithoutRestart is the regression
// proof for the exact bug an operator hit in production: adding a chain
// through the admin panel's "Add chain" menu while the server was already
// running left it permanently unwatched, because watcher goroutines used to
// be started only once, at boot, from whatever chains existed at that
// moment. StartWatchers now remembers its own ctx/pollInterval/logger, and
// CreateChain calls back into it -- this proves a chain created AFTER
// StartWatchers has already run gets a real, working watcher with no
// further action, by sending it a real Ganache transaction and watching
// Stars actually land.
func TestDonationsCreateChainStartsWatcherWithoutRestart(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	client, err := ethclient.DialContext(ctx, "http://127.0.0.1:7545")
	if err != nil {
		t.Skip("ganache not reachable at 127.0.0.1:7545: " + err.Error())
	}
	if _, err := client.ChainID(ctx); err != nil {
		t.Skip("ganache not responding: " + err.Error())
	}

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

	// Simulate "server already running": start watchers for whatever's
	// currently enabled (may or may not include a chain at this key --
	// either way, the chain this test creates below doesn't exist yet).
	watcherCtx, cancelWatchers := context.WithCancel(ctx)
	defer cancelWatchers()
	if err := svc.StartWatchers(watcherCtx, 200*time.Millisecond, zaptest.NewLogger(t)); err != nil {
		t.Fatalf("start watchers: %v", err)
	}

	chainKey := "dynwatch" + suffix[:8]
	chain, err := svc.CreateChain(ctx, domain.DonationChain{
		Key: chainKey, Name: "Dynamic Watch Test", ChainID: 1337,
		RPCURL: "http://127.0.0.1:7545", NativeSymbol: "ETH", NativeDecimals: 18,
		ConfirmationsRequired: 1, ManualUSDRateMicros: 2_000_000_000, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create chain: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM donation_deposits WHERE chain_key = $1", chainKey)
		_, _ = pool.Exec(ctx, "DELETE FROM donation_chain_cursors WHERE chain_key = $1", chainKey)
		_, _ = pool.Exec(ctx, "DELETE FROM donation_chains WHERE chain_key = $1", chainKey)
	})
	if !chain.Watchable() {
		t.Fatalf("created chain = %+v, want watchable", chain)
	}

	users := NewUserStore(pool)
	donor, err := users.Create(ctx, domain.User{AccessHash: 74, Phone: "+1778" + suffix + "74", FirstName: "DynWatchDonor"})
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

	starsStore := NewStarsStore(pool)
	before, err := starsStore.GetBalance(ctx, donor.ID)
	if err != nil {
		t.Fatalf("read starting balance: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		balance, err := starsStore.GetBalance(ctx, donor.ID)
		if err != nil {
			t.Fatalf("read balance: %v", err)
		}
		if balance.Balance > before.Balance {
			t.Logf("credited via dynamically started watcher: balance %d -> %d", before.Balance, balance.Balance)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for the dynamically created chain's watcher to credit the deposit (balance still %d) -- CreateChain did not actually start a watcher", balance.Balance)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
