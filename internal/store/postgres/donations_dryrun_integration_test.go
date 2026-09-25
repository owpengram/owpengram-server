package postgres

import (
	"context"
	"errors"
	"testing"

	"telesrv/internal/app/donations"
	"telesrv/internal/domain"
)

// TestDonationChainDryRunWritesNothing pins that validating a chain is not
// the same as creating one.
//
// This shipped broken: the admin panel puts "Add network" behind a dry-run
// step, but the action had no dry-run branch, so pressing "Dry-run check"
// created the chain for real. The confirm that followed then failed with
// "chain already exists" -- on a network the operator had never added, which
// nonetheless appeared after a page refresh. The same hole applied to
// editing a chain (a dry run repointed a live RPC endpoint), removing one,
// and saving a token contract.
func TestDonationChainDryRunWritesNothing(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	store := NewDonationStore(pool)
	key, err := donations.ParseEncryptionKey(testDonationWalletKey)
	if err != nil {
		t.Fatalf("parse wallet key: %v", err)
	}
	svc, err := donations.NewService(ctx, store, key)
	if err != nil {
		t.Fatalf("new donations service: %v", err)
	}

	chainKey := "dryrun" + randomSuffix(t)[:6]
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM donation_tokens WHERE chain_key=$1", chainKey)
		_, _ = pool.Exec(ctx, "DELETE FROM donation_chains WHERE chain_key=$1", chainKey)
	})
	draft := domain.DonationChain{
		Key: chainKey, Name: "Dry Run Net", ChainID: 8453, NativeSymbol: "ETH", NativeDecimals: 18,
		RPCURL: "https://example.invalid", ConfirmationsRequired: 12,
		ManualUSDRateMicros: 2_700_000_000, Enabled: true,
	}

	// A preview of a brand new chain passes and stores nothing...
	if err := svc.PreviewCreateChain(ctx, draft); err != nil {
		t.Fatalf("PreviewCreateChain on a free key: %v", err)
	}
	if _, found, err := store.DonationChain(ctx, chainKey); err != nil || found {
		t.Fatalf("the dry run created the chain (found=%v err=%v)", found, err)
	}
	// ...and stays repeatable, which is what the panel's "Run dry-run again"
	// button does.
	if err := svc.PreviewCreateChain(ctx, draft); err != nil {
		t.Fatalf("second PreviewCreateChain: %v -- a dry run must not depend on its own side effects", err)
	}

	created, err := svc.CreateChain(ctx, draft)
	if err != nil {
		t.Fatalf("CreateChain: %v", err)
	}
	if created.Key != chainKey {
		t.Fatalf("created %q, want %q", created.Key, chainKey)
	}
	// Now the same preview must report the collision instead of hiding it.
	if err := svc.PreviewCreateChain(ctx, draft); !errors.Is(err, domain.ErrDonationChainAlreadyExists) {
		t.Fatalf("PreviewCreateChain on a taken key = %v, want ErrDonationChainAlreadyExists", err)
	}

	// Editing: a preview must not touch the live config.
	update := domain.DonationChainConfigUpdate{
		ChainKey: chainKey, RPCURL: "https://rewritten.invalid",
		ConfirmationsRequired: 3, ManualUSDRateMicros: 1_000_000, Enabled: true,
	}
	if err := svc.PreviewUpdateChainConfig(ctx, update); err != nil {
		t.Fatalf("PreviewUpdateChainConfig: %v", err)
	}
	reloaded, found, err := store.DonationChain(ctx, chainKey)
	if err != nil || !found {
		t.Fatalf("reload chain: %v (found=%v)", err, found)
	}
	if reloaded.RPCURL != draft.RPCURL || reloaded.ConfirmationsRequired != draft.ConfirmationsRequired {
		t.Fatalf("the dry run edited the live chain: rpc=%q confirmations=%d", reloaded.RPCURL, reloaded.ConfirmationsRequired)
	}

	// Tokens: a preview must not write the contract.
	token := domain.DonationToken{ChainKey: chainKey, Symbol: "USDC",
		ContractAddress: "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238", Decimals: 6}
	if err := svc.PreviewUpsertToken(ctx, token); err != nil {
		t.Fatalf("PreviewUpsertToken: %v", err)
	}
	tokens, err := svc.ChainTokens(ctx, chainKey)
	if err != nil {
		t.Fatalf("ChainTokens: %v", err)
	}
	if len(tokens) != 0 {
		t.Fatalf("the dry run saved %d token(s)", len(tokens))
	}

	// Removal: a preview must leave the chain in place, and must still catch
	// a key that isn't there.
	if err := svc.PreviewDeleteChain(ctx, chainKey); err != nil {
		t.Fatalf("PreviewDeleteChain: %v", err)
	}
	if _, found, err := store.DonationChain(ctx, chainKey); err != nil || !found {
		t.Fatalf("the dry run deleted the chain (found=%v err=%v)", found, err)
	}
	if err := svc.PreviewDeleteChain(ctx, chainKey+"nope"); !errors.Is(err, domain.ErrDonationChainNotFound) {
		t.Fatalf("PreviewDeleteChain on a missing chain = %v, want ErrDonationChainNotFound", err)
	}

	// And the validation a preview shares with the real write still bites.
	bad := draft
	bad.Key = chainKey + "b"
	bad.ManualUSDRateMicros = 0
	if err := svc.PreviewCreateChain(ctx, bad); err == nil {
		t.Fatal("PreviewCreateChain accepted an enabled chain with no USD rate")
	}
}
