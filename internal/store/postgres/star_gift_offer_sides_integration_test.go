package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"telesrv/internal/app/giftpack"
	stargiftsapp "telesrv/internal/app/stargifts"
	"telesrv/internal/domain"
	"telesrv/internal/seed/giftpacks"
)

// TestStarGiftOfferLandsOnTheOwnerSide pins which side of a purchase offer
// each copy of the service message belongs to.
//
// Clients decide what to draw from the message direction alone: tdesktop
// shows "An offer to buy this gift" plus the Accept/Reject buttons -- and
// therefore the sell confirmation -- for an INCOMING copy (`!out()`), and
// "You offered N for X" with no buttons for the outgoing one. So if the
// buyer's own copy were ever stored as incoming, the buyer would be invited
// to sell a gift they do not own.
//
// It also pins the invariant that makes that impossible upstream: an offer
// can only be addressed to the gift's current owner.
func TestStarGiftOfferLandsOnTheOwnerSide(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	giver, err := users.Create(ctx, domain.User{AccessHash: 71, Phone: "+1779" + suffix + "71", FirstName: "Giver"})
	if err != nil {
		t.Fatalf("create giver: %v", err)
	}
	owner, err := users.Create(ctx, domain.User{AccessHash: 72, Phone: "+1779" + suffix + "72", FirstName: "Owner"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	buyer, err := users.Create(ctx, domain.User{AccessHash: 73, Phone: "+1779" + suffix + "73", FirstName: "Buyer"})
	if err != nil {
		t.Fatalf("create buyer: %v", err)
	}
	ownerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}
	buyerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: buyer.ID}

	// A real collectible: import an upgradeable gift, buy it for the owner
	// with a prepaid upgrade, then unpack it.
	gifts := NewStarGiftStore(pool)
	svc := stargiftsapp.NewService(gifts, &upgradeTestBlobs{store: map[string][]byte{}}, 2)
	manifest, assets, ok := giftpacks.Manifest("grind-pack")
	if !ok {
		t.Fatal("grind-pack pack missing")
	}
	var spec giftpack.GiftSpec
	for _, g := range manifest.Gifts {
		if g.IDSlug == "phone" {
			spec = g
		}
	}
	if spec.Upgrade == nil {
		t.Fatal("the phone gift is not upgradeable any more")
	}
	spec.Title += " " + suffix
	spec.Upgrade.SlugPrefix = "offerphone-" + suffix
	manifest.Gifts = []giftpack.GiftSpec{spec}
	imported, err := giftpack.Import(ctx, svc, manifest, assets, giftpack.ImportOptions{})
	if err != nil || len(imported.Gifts) != 1 || imported.Gifts[0].Status != "created" {
		t.Fatalf("import = %+v err %v", imported, err)
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
	charge := gift.Stars + gift.UpgradeStars
	stars := NewStarsStore(pool)
	if _, _, err := stars.EnsureGrant(ctx, giver.ID, charge*10, now); err != nil {
		t.Fatalf("grant giver stars: %v", err)
	}
	messages := NewMessageStore(pool)
	lifecycle := NewStarGiftLifecycleStore(pool, messages, 0)
	form, err := lifecycle.IssueStarGiftPurchaseForm(ctx, domain.StarGiftPurchaseForm{
		BuyerUserID: giver.ID, To: ownerPeer, GiftID: gift.ID, RevisionID: gift.RevisionID,
		IncludeUpgrade: true, ChargeStars: charge, IssuedAt: now, ExpiresAt: now + 600,
	})
	if err != nil {
		t.Fatalf("issue purchase form: %v", err)
	}
	purchase, err := lifecycle.PurchaseStarGift(ctx, domain.StarGiftPurchaseRequest{
		BuyerUserID: giver.ID, To: ownerPeer, GiftID: gift.ID, RevisionID: gift.RevisionID,
		IncludeUpgrade: true, ChargeStars: charge, FormID: form.FormID,
		CommandKey: fmt.Sprintf("offer-purchase:%d", form.FormID), Date: now,
	})
	if err != nil {
		t.Fatalf("purchase: %v", err)
	}
	upgraded, err := NewStarGiftUpgradeStore(pool, messages).UpgradeStarGift(ctx, domain.StarGiftUpgradeRequest{
		UserID: owner.ID, Ref: domain.SavedStarGiftRef{Owner: ownerPeer, MsgID: purchase.Saved.MsgID},
		RequirePrepaid: true, CommandKey: "offer-upgrade-" + suffix, Date: now + 1,
	})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	unique := upgraded.Unique
	if unique.Owner != ownerPeer {
		t.Fatalf("collectible owner = %+v, want the owner %+v", unique.Owner, ownerPeer)
	}
	// Offers are only accepted on a gift whose owner opened them up to one.
	if _, err := pool.Exec(ctx, `UPDATE unique_star_gifts SET offer_min_stars=$2 WHERE id=$1`, unique.ID, 100); err != nil {
		t.Fatalf("open the gift to offers: %v", err)
	}

	const price = 2555
	if _, _, err := stars.EnsureGrant(ctx, buyer.ID, price*4, now); err != nil {
		t.Fatalf("grant buyer stars: %v", err)
	}

	// The invariant that keeps the sell prompt off a stranger's screen: an
	// offer may only be addressed to the gift's current owner.
	if _, err := lifecycle.SendStarGiftOffer(ctx, domain.StarGiftOfferRequest{
		BuyerUserID: giver.ID, Owner: buyerPeer, Slug: unique.Slug,
		Price:    domain.StarGiftAmount{Currency: domain.StarGiftCurrencyStars, Amount: price},
		Duration: 86400, RandomID: 981001, Date: now + 2,
	}); err == nil {
		t.Fatal("an offer addressed to a user who does not own the gift was accepted")
	}

	result, err := lifecycle.SendStarGiftOffer(ctx, domain.StarGiftOfferRequest{
		BuyerUserID: buyer.ID, Owner: ownerPeer, Slug: unique.Slug,
		Price:    domain.StarGiftAmount{Currency: domain.StarGiftCurrencyStars, Amount: price},
		Duration: 86400, RandomID: 981002, Date: now + 3,
	})
	if err != nil {
		t.Fatalf("send offer: %v", err)
	}

	buyerCopy := result.Send.SenderMessage
	ownerCopy := result.Send.RecipientMessage
	if buyerCopy.OwnerUserID != buyer.ID {
		t.Fatalf("sender box = user %d, want the buyer %d", buyerCopy.OwnerUserID, buyer.ID)
	}
	if !buyerCopy.Out {
		t.Fatal("the buyer's own copy is stored as incoming: their client would show " +
			"\"An offer to buy this gift\" with Accept/Reject and prompt them to sell a gift they do not own")
	}
	if ownerCopy.OwnerUserID != owner.ID {
		t.Fatalf("recipient box = user %d, want the owner %d", ownerCopy.OwnerUserID, owner.ID)
	}
	if ownerCopy.Out {
		t.Fatal("the owner's copy is stored as outgoing: their client would hide the Accept/Reject buttons")
	}
	if ownerCopy.From.Type != domain.PeerTypeUser || ownerCopy.From.ID != buyer.ID {
		t.Fatalf("owner's copy from_id = %+v, want the buyer %d", ownerCopy.From, buyer.ID)
	}
	for _, copy := range []domain.Message{buyerCopy, ownerCopy} {
		action := copy.Media.ServiceAction.StarGiftOffer
		if action == nil || action.Price.Amount != price {
			t.Fatalf("user %d offer action = %+v, want the %d-star offer", copy.OwnerUserID, action, price)
		}
		// The gift travels with its real owner, so a client that checks
		// ownership instead of direction reaches the same conclusion.
		if action.Gift.Owner != ownerPeer {
			t.Fatalf("user %d sees the gift owned by %+v, want %+v", copy.OwnerUserID, action.Gift.Owner, ownerPeer)
		}
	}

	// Resolving is keyed on (owner, owner's message id), so even a client
	// that drew the buttons on the wrong side cannot complete a sale.
	if _, err := lifecycle.ResolveStarGiftOffer(ctx, domain.StarGiftResolveOfferRequest{
		OwnerUserID: buyer.ID, OfferMsgID: buyerCopy.ID, Date: now + 4,
	}); err == nil {
		t.Fatal("the buyer resolved their own offer, selling a gift they do not own")
	}
}
