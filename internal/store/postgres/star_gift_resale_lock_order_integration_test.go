package postgres

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"telesrv/internal/app/giftpack"
	stargiftsapp "telesrv/internal/app/stargifts"
	"telesrv/internal/domain"
	"telesrv/internal/seed/giftpacks"
)

// TestResaleListingAndPurchaseDoNotDeadlock is a regression/stress test for a
// lock-order bug: SetStarGiftListing (an owner listing, relisting or
// delisting their own gift) locks peer_star_gifts before star_gift_listings,
// while PurchaseResaleStarGift (a buyer checking out a resale listing found
// by slug) locked star_gift_listings/unique_star_gifts before
// peer_star_gifts -- the reverse order. A seller fiddling with their price
// while a buyer's client is mid-checkout on the very same gift hit a
// classic ABBA deadlock: Postgres aborts one side with "deadlock detected",
// which surfaced to the client as a listing/delisting or purchase that
// "just failed", repeatably, until the conflicting request cleared (in
// production this read as "server crash", fixed only by reconnecting).
//
// The fix takes a matching pg_advisory_xact_lock on the unique gift's id as
// the very first thing both paths do, before either row lock, so the two
// can never interleave into the deadlock shape. This test cannot force the
// exact timing that triggers a specific interleaving (no hook exists to
// pause a transaction mid-flight), so it relies on many concurrent
// relist/purchase attempts on the same gift to make the race likely if the
// ordering fix regresses. It is deterministic on the fixed code -- the
// advisory lock makes the deadlock structurally impossible -- and reliably
// reproduces a genuine Postgres "deadlock detected (SQLSTATE 40P01)" within
// a handful of runs when the fix is reverted (verified by hand).
func TestResaleListingAndPurchaseDoNotDeadlock(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	seller, err := users.Create(ctx, domain.User{AccessHash: 41, Phone: "+1778" + suffix + "41", FirstName: "Seller"})
	if err != nil {
		t.Fatalf("create seller: %v", err)
	}
	buyer, err := users.Create(ctx, domain.User{AccessHash: 42, Phone: "+1778" + suffix + "42", FirstName: "Buyer"})
	if err != nil {
		t.Fatalf("create buyer: %v", err)
	}
	sellerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: seller.ID}

	gifts := NewStarGiftStore(pool)
	svc := stargiftsapp.NewService(gifts, &upgradeTestBlobs{store: map[string][]byte{}}, 2)
	manifest, assets, ok := giftpacks.Manifest("first-pack")
	if !ok {
		t.Fatal("first-pack pack missing")
	}
	var phone giftpack.GiftSpec
	for _, g := range manifest.Gifts {
		if g.IDSlug == "phone" {
			phone = g
		}
	}
	phone.Title += " " + suffix
	phone.Upgrade.SlugPrefix = "phone-lock-" + suffix
	manifest.Gifts = []giftpack.GiftSpec{phone}
	imported, err := giftpack.Import(ctx, svc, manifest, assets, giftpack.ImportOptions{})
	if err != nil || len(imported.Gifts) != 1 || imported.Gifts[0].Status != "created" {
		t.Fatalf("import upgradeable gift = %+v err %v", imported, err)
	}
	var gift domain.StarGift
	catalog, err := svc.Catalog(ctx)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	for _, g := range catalog {
		if g.ID == imported.Gifts[0].GiftID {
			gift = g
		}
	}
	if gift.ID == 0 || gift.UpgradeStars <= 0 {
		t.Fatalf("imported gift = %+v, want an upgradeable catalog entry", gift)
	}

	now := int(time.Now().Unix())
	stars := NewStarsStore(pool)
	if _, _, err := stars.EnsureGrant(ctx, seller.ID, (gift.Stars+gift.UpgradeStars)*10, now); err != nil {
		t.Fatalf("grant seller stars: %v", err)
	}
	if _, _, err := stars.EnsureGrant(ctx, buyer.ID, 100_000, now); err != nil {
		t.Fatalf("grant buyer stars: %v", err)
	}

	messages := NewMessageStore(pool)
	lifecycle := NewStarGiftLifecycleStore(pool, messages, 0)
	charge := gift.Stars + gift.UpgradeStars
	form, err := lifecycle.IssueStarGiftPurchaseForm(ctx, domain.StarGiftPurchaseForm{
		BuyerUserID: seller.ID, To: sellerPeer, GiftID: gift.ID, RevisionID: gift.RevisionID,
		IncludeUpgrade: true, ChargeStars: charge, IssuedAt: now, ExpiresAt: now + 600,
	})
	if err != nil {
		t.Fatalf("issue purchase form: %v", err)
	}
	purchase, err := lifecycle.PurchaseStarGift(ctx, domain.StarGiftPurchaseRequest{
		BuyerUserID: seller.ID, To: sellerPeer, GiftID: gift.ID, RevisionID: gift.RevisionID,
		IncludeUpgrade: true, ChargeStars: charge, FormID: form.FormID,
		CommandKey: fmt.Sprintf("purchase:%d", form.FormID), Date: now,
	})
	if err != nil {
		t.Fatalf("seller self-purchase with prepaid upgrade: %v", err)
	}

	upgrades := NewStarGiftUpgradeStore(pool, messages)
	upgradeResult, err := upgrades.UpgradeStarGift(ctx, domain.StarGiftUpgradeRequest{
		UserID: seller.ID, Ref: domain.SavedStarGiftRef{Owner: sellerPeer, MsgID: purchase.Saved.MsgID},
		RequirePrepaid: true, CommandKey: "upgrade-" + suffix, Date: now + 1,
	})
	if err != nil {
		t.Fatalf("seller upgrade: %v", err)
	}
	ref := domain.SavedStarGiftRef{Owner: sellerPeer, MsgID: upgradeResult.Send.SenderMessage.ID}
	slug := upgradeResult.Unique.Slug

	// Seed a listing so the buyer side has something to race against from
	// the first tick.
	if _, err := lifecycle.SetStarGiftListing(ctx, domain.StarGiftListingRequest{
		ActorUserID: seller.ID, Ref: ref,
		Amount: &domain.StarGiftAmount{Currency: domain.StarGiftCurrencyStars, Amount: 1500}, Date: now + 2,
	}); err != nil {
		t.Fatalf("seed listing: %v", err)
	}

	const relistRounds = 40
	const parallelSellers = 4
	const parallelBuyers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	var sellerErrs, buyerErrs []string
	var mu sync.Mutex

	for s := 0; s < parallelSellers; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < relistRounds; i++ {
				amount := int64(1100)
				if i%2 == 0 {
					amount = 1500
				}
				t := int(time.Now().Unix()) + 3 + i
				// Alternate delisting and relisting at a different price, same
				// shape as the reported "remove from sale, then relist lower".
				// Several goroutines hammer the SAME ref concurrently purely to
				// raise contention -- only one owner exists in reality, but the
				// row lock itself doesn't care who's asking. Once the buyer
				// side actually wins the race, ownership moves and every
				// further call here legitimately fails on ownership -- expected,
				// only a deadlock is a bug.
				if _, err := lifecycle.SetStarGiftListing(ctx, domain.StarGiftListingRequest{
					ActorUserID: seller.ID, Ref: ref, Amount: nil, Date: t,
				}); err != nil && strings.Contains(strings.ToLower(err.Error()), "deadlock") {
					mu.Lock()
					sellerErrs = append(sellerErrs, err.Error())
					mu.Unlock()
				}
				if _, err := lifecycle.SetStarGiftListing(ctx, domain.StarGiftListingRequest{
					ActorUserID: seller.ID, Ref: ref,
					Amount: &domain.StarGiftAmount{Currency: domain.StarGiftCurrencyStars, Amount: amount}, Date: t,
				}); err != nil && strings.Contains(strings.ToLower(err.Error()), "deadlock") {
					mu.Lock()
					sellerErrs = append(sellerErrs, err.Error())
					mu.Unlock()
				}
			}
		}()
	}

	for b := 0; b < parallelBuyers; b++ {
		wg.Add(1)
		go func(b int) {
			defer wg.Done()
			<-start
			for i := 0; i < relistRounds; i++ {
				var currency string
				var amount int64
				err := pool.QueryRow(ctx, `SELECT currency,amount FROM star_gift_listings l
				JOIN unique_star_gifts u ON u.id=l.unique_gift_id WHERE lower(u.slug)=lower($1)`, slug).Scan(&currency, &amount)
				if err != nil {
					continue // nothing listed at this instant -- not a failure, just a miss.
				}
				_, err = lifecycle.PurchaseResaleStarGift(ctx, domain.StarGiftResalePurchaseRequest{
					BuyerUserID: buyer.ID, Slug: slug, To: domain.Peer{Type: domain.PeerTypeUser, ID: buyer.ID},
					Amount: domain.StarGiftAmount{Currency: domain.StarGiftCurrency(currency), Amount: amount},
					FormID: int64(1000 + i), CommandKey: fmt.Sprintf("resale-race-%s-%d-%d", suffix, b, i),
					Date: int(time.Now().Unix()) + 3 + i,
				})
				if err != nil && strings.Contains(strings.ToLower(err.Error()), "deadlock") {
					mu.Lock()
					buyerErrs = append(buyerErrs, err.Error())
					mu.Unlock()
				}
				// Any other outcome (success, or a business rejection because the
				// seller changed/removed the listing between the read above and
				// the lock inside PurchaseResaleStarGift, or the gift already
				// belongs to the buyer from an earlier winning round) is fine --
				// only a deadlock is a bug.
			}
		}(b)
	}

	close(start)
	wg.Wait()

	if len(sellerErrs) > 0 {
		t.Fatalf("seller relist hit a Postgres deadlock: %v", sellerErrs)
	}
	if len(buyerErrs) > 0 {
		t.Fatalf("buyer purchase hit a Postgres deadlock: %v", buyerErrs)
	}
}
