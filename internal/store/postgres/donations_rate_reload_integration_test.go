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

// TestDonationsWatcherPicksUpRateFixWithoutRestart is the regression proof
// for the second half of an operator-reported failure: a chain enabled with
// a USD rate too low to be worth a single Star prices every deposit to 0
// Stars, and the watcher deliberately refuses to credit zero -- so the
// deposit sits at "confirmed". Fixing the rate in the admin panel then has
// to take effect on the watcher's very next poll; before WatchChain re-read
// its chain config each pass it captured the rate once at startup, so the
// corrected rate did nothing until the whole server was restarted and the
// deposit looked permanently stuck.
func TestDonationsWatcherPicksUpRateFixWithoutRestart(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	client, err := ethclient.DialContext(ctx, testGanacheURL())
	if err != nil {
		t.Skip("ganache not reachable at " + testGanacheURL() + ": " + err.Error())
	}
	if _, err := client.ChainID(ctx); err != nil {
		t.Skip("ganache not responding: " + err.Error())
	}

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

	watcherCtx, cancelWatchers := context.WithCancel(ctx)
	defer cancelWatchers()
	if err := svc.StartWatchers(watcherCtx, 200*time.Millisecond, zaptest.NewLogger(t)); err != nil {
		t.Fatalf("start watchers: %v", err)
	}

	// $0.000001 per whole ETH: enough to pass the "enabled needs a positive
	// rate" guard, far too little for a 0.1 ETH deposit to reach one Star.
	chainKey := "ratefix" + suffix[:8]
	if _, err := svc.CreateChain(ctx, domain.DonationChain{
		Key: chainKey, Name: "Rate Fix Test", ChainID: 1337,
		RPCURL: testGanacheURL(), NativeSymbol: "ETH", NativeDecimals: 18,
		ConfirmationsRequired: 1, ManualUSDRateMicros: 1, Enabled: true,
	}); err != nil {
		t.Fatalf("create chain: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM donation_deposits WHERE chain_key = $1", chainKey)
		_, _ = pool.Exec(ctx, "DELETE FROM donation_chain_cursors WHERE chain_key = $1", chainKey)
		_, _ = pool.Exec(ctx, "DELETE FROM donation_chains WHERE chain_key = $1", chainKey)
	})

	users := NewUserStore(pool)
	donor, err := users.Create(ctx, domain.User{AccessHash: 75, Phone: "+1778" + suffix + "75", FirstName: "RateFixDonor"})
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
	if err := client.Client().CallContext(ctx, new(string), "eth_sendTransaction", map[string]string{
		"from": accounts[0], "to": address, "value": "0x16345785D8A0000", // 0.1 ETH
	}); err != nil {
		t.Fatalf("fund deposit address: %v", err)
	}

	donationStore := NewDonationStore(pool)
	// The deposit must be detected and reach "confirmed", but never be
	// credited at this rate. Only this chain's own row matters: another
	// chain row may point at the same local node and see the same
	// transaction, which is an operator misconfiguration, not this test's
	// subject.
	deadline := time.Now().Add(10 * time.Second)
	for {
		deposit, found := depositForChain(t, ctx, donationStore, donor.ID, chainKey)
		if found && deposit.Status == domain.DonationDepositConfirmed {
			break
		}
		if found && deposit.Status == domain.DonationDepositCredited {
			t.Fatalf("deposit was credited at a rate that prices it below one Star: %+v", deposit)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for the deposit to reach confirmed (found=%v, %+v)", found, deposit)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Operator fixes the rate in the admin panel: $2000 per ETH. No
	// restart, no watcher bounce -- the running watcher must pick it up.
	if _, err := svc.UpdateChainConfig(ctx, domain.DonationChainConfigUpdate{
		ChainKey: chainKey, RPCURL: testGanacheURL(), ConfirmationsRequired: 1,
		ManualUSDRateMicros: 2_000_000_000, Enabled: true,
	}); err != nil {
		t.Fatalf("update chain rate: %v", err)
	}

	deadline = time.Now().Add(10 * time.Second)
	for {
		deposit, found := depositForChain(t, ctx, donationStore, donor.ID, chainKey)
		if found && deposit.Status == domain.DonationDepositCredited {
			if deposit.StarsCredited <= 0 {
				t.Fatalf("credited deposit has no Stars: %+v", deposit)
			}
			t.Logf("stuck deposit credited after the rate fix, with no restart: %d Stars", deposit.StarsCredited)
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out: the running watcher never picked up the corrected USD rate (it is still using the config it captured at startup)")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// depositForChain returns the donor's deposit on exactly one chain, so a
// second chain row pointing at the same local node can't satisfy an
// assertion meant for this test's own chain.
func depositForChain(t *testing.T, ctx context.Context, store *DonationStore, userID int64, chainKey string) (domain.DonationDeposit, bool) {
	t.Helper()
	deposits, err := store.UserDonationDeposits(ctx, userID, 20)
	if err != nil {
		t.Fatalf("list deposits: %v", err)
	}
	for _, d := range deposits {
		if d.ChainKey == chainKey {
			return d, true
		}
	}
	return domain.DonationDeposit{}, false
}
