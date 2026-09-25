package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	botsapp "telesrv/internal/app/bots"
	"telesrv/internal/app/donations"
	"telesrv/internal/domain"
)

// testDonationWalletKey is a fixed, obviously-not-secret 32-byte AES key
// (64 hex chars) used only by this test -- production keys come from
// TELESRV_DONATION_WALLET_KEY and are never checked into the repo.
const testDonationWalletKey = "0101010101010101010101010101010101010101010101010101010101010101" // 64 hex chars

// testGanacheURL is the local dev chain every donations integration test
// dials. Overridable so a second, throwaway Ganache can be used without
// disturbing whichever one a developer already has wired into their own
// running server on the default port.
func testGanacheURL() string {
	if url := strings.TrimSpace(os.Getenv("TELESRV_TEST_GANACHE_URL")); url != "" {
		return url
	}
	return "http://127.0.0.1:7545"
}

// ensureGanacheChain points the shared "ganache" chain row at whatever
// testGanacheURL() resolves to, with a confirmation depth of 1 and a
// sane USD rate. Without this a test would silently run against whatever
// endpoint a previous run (or a developer's own server) happened to leave
// in the scratch database.
func ensureGanacheChain(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
INSERT INTO donation_chains (chain_key, name, chain_id, rpc_url, ws_url, native_symbol, native_decimals,
    confirmations_required, price_feed_address, manual_usd_rate_micros, enabled)
VALUES ('ganache', 'Ganache (local)', 1337, $1, '', 'ETH', 18, 1, '', 2000000000, true)
ON CONFLICT (chain_key) DO UPDATE SET rpc_url = EXCLUDED.rpc_url, confirmations_required = 1,
    manual_usd_rate_micros = 2000000000, enabled = true`, testGanacheURL()); err != nil {
		t.Fatalf("ensure ganache chain row: %v", err)
	}
	// Drop any cursor left over from a previous run against a different
	// (or since-reset) local chain, so each run starts cleanly from the
	// current tip rather than from a height this chain may never reach.
	if _, err := pool.Exec(ctx, `DELETE FROM donation_chain_cursors WHERE chain_key = 'ganache'`); err != nil {
		t.Fatalf("reset ganache cursor: %v", err)
	}
}

// TestDonationsGanacheDepositCreditsStars is the end-to-end proof the whole
// donations pipeline actually works: derive a real per-user address, send a
// real transaction to it on a local Ganache node, run the watcher's scan
// pass, and see Stars land in the user's balance. Skips (not fails) if
// Ganache isn't reachable -- this is a real blockchain
// integration test, not a mock.
func TestDonationsGanacheDepositCreditsStars(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	client, err := ethclient.DialContext(ctx, testGanacheURL())
	if err != nil {
		t.Skip("ganache not reachable at " + testGanacheURL() + ": " + err.Error())
	}
	chainID, err := client.ChainID(ctx)
	if err != nil {
		t.Skip("ganache not responding: " + err.Error())
	}
	t.Logf("connected to chain id %s", chainID)

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
	donor, err := users.Create(ctx, domain.User{AccessHash: 71, Phone: "+1778" + suffix + "71", FirstName: "Donor"})
	if err != nil {
		t.Fatalf("create donor: %v", err)
	}

	// Wire a real @premiumbot service as the credit notifier, the same way
	// cmd/telesrv does (donationsService.SetNotifier(botsService)) -- this
	// proves the "you got credited" chat message actually gets sent, not
	// just that the ledger row changes.
	botsService := botsapp.NewService(users, NewBotStore(pool), NewMessageStore(pool))
	svc.SetNotifier(botsService)

	address, err := svc.AddressForUser(ctx, donor.ID)
	if err != nil {
		t.Fatalf("assign donation address: %v", err)
	}
	if address == "" {
		t.Fatal("assigned address is empty")
	}
	t.Logf("donor %d deposit address: %s", donor.ID, address)

	before, err := NewStarsStore(pool).GetBalance(ctx, donor.ID)
	if err != nil {
		t.Fatalf("read starting balance: %v", err)
	}

	// Ganache's first well-known unlocked dev account funds the deposit.
	var senders []string
	if err := client.Client().CallContext(ctx, &senders, "eth_accounts"); err != nil {
		t.Fatalf("eth_accounts: %v", err)
	}
	if len(senders) == 0 {
		t.Fatal("ganache reports no accounts")
	}
	const sendAmountWei = "0xDE0B6B3A7640000" // 1 ETH
	var txHash string
	if err := client.Client().CallContext(ctx, &txHash, "eth_sendTransaction", map[string]string{
		"from":  senders[0],
		"to":    address,
		"value": sendAmountWei,
	}); err != nil {
		t.Fatalf("eth_sendTransaction: %v", err)
	}
	t.Logf("sent 1 ETH donation in tx %s", txHash)

	log := zaptest.NewLogger(t)
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := runOneDonationScanPass(t, svc, ctx, "ganache", log); err != nil {
			t.Fatalf("scan pass: %v", err)
		}
		balance, err := NewStarsStore(pool).GetBalance(ctx, donor.ID)
		if err != nil {
			t.Fatalf("read balance: %v", err)
		}
		if balance.Balance > before.Balance {
			t.Logf("credited: balance %d -> %d", before.Balance, balance.Balance)
			deposits, err := donationStore.UserDonationDeposits(ctx, donor.ID, 5)
			if err != nil {
				t.Fatalf("list deposits: %v", err)
			}
			if len(deposits) != 1 {
				t.Fatalf("deposits = %d, want 1", len(deposits))
			}
			if deposits[0].Status != domain.DonationDepositCredited {
				t.Fatalf("deposit status = %q, want credited", deposits[0].Status)
			}
			if deposits[0].StarsCredited <= 0 {
				t.Fatalf("stars credited = %d, want > 0", deposits[0].StarsCredited)
			}
			if deposits[0].TokenSymbol != "" {
				t.Fatalf("token symbol = %q, want empty (native deposit)", deposits[0].TokenSymbol)
			}

			list, err := NewMessageStore(pool).ListByUser(ctx, donor.ID, domain.MessageFilter{
				HasPeer: true, Peer: domain.Peer{Type: domain.PeerTypeUser, ID: domain.PremiumBotUserID}, Limit: 100,
			})
			if err != nil {
				t.Fatalf("list premiumbot history: %v", err)
			}
			var notice domain.Message
			for _, msg := range list.Messages {
				if msg.From.ID == domain.PremiumBotUserID && msg.ID > notice.ID {
					notice = msg
				}
			}
			if notice.ID == 0 {
				t.Fatal("no @premiumbot notification was sent after the deposit was credited")
			}
			wantStars := fmt.Sprintf("+%d OwpenGram Stars", deposits[0].StarsCredited)
			if !strings.Contains(notice.Body, wantStars) || !strings.Contains(notice.Body, "Ganache") || !strings.Contains(notice.Body, "ETH") {
				t.Fatalf("premiumbot notification = %q, want it to mention %q, the chain and the asset", notice.Body, wantStars)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for deposit to be credited (balance still %d)", balance.Balance)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// runOneDonationScanPass drives one Service.scanOnce-equivalent pass:
// WatchChain itself loops forever on a ticker, so this runs it against a
// short-lived context and treats the resulting deadline/cancel as success.
func runOneDonationScanPass(t *testing.T, svc *donations.Service, parent context.Context, chainKey string, log *zap.Logger) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	err := svc.WatchChain(ctx, chainKey, 200*time.Millisecond, log)
	if err == context.DeadlineExceeded || err == context.Canceled {
		return nil
	}
	return err
}
