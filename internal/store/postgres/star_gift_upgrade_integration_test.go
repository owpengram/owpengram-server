package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"

	"telesrv/internal/app/giftpack"
	stargiftsapp "telesrv/internal/app/stargifts"
	"telesrv/internal/domain"
	"telesrv/internal/seed/giftpacks"
)

type upgradeTestBlobs struct {
	mu    sync.Mutex
	store map[string][]byte
}

func (b *upgradeTestBlobs) Name() string { return string(domain.MediaBackendLocalFS) }
func (b *upgradeTestBlobs) Put(_ context.Context, data []byte) (string, error) {
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])
	b.mu.Lock()
	b.store[key] = append([]byte(nil), data...)
	b.mu.Unlock()
	return key, nil
}
func (b *upgradeTestBlobs) Get(_ context.Context, key string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.store[key]
	if !ok {
		return nil, fmt.Errorf("blob %q not found", key)
	}
	return data, nil
}

// TestStarGiftPrepaidUpgradeServiceMessageIsAuthoredByOwner pins who writes the
// upgrade service message: the owner who unpacked the gift, into their chat
// with the giver, with no from_id. Clients derive "You unpacked the gift that
// X helped to upgrade" / "Y unpacked the gift that you helped to upgrade" (and
// "You turned the gift from X ..." for a plain upgrade) from the message
// author, so authoring it as the giver swapped both sides.
func TestStarGiftPrepaidUpgradeServiceMessageIsAuthoredByOwner(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	giver, err := users.Create(ctx, domain.User{AccessHash: 91, Phone: "+1778" + suffix + "61", FirstName: "Giver"})
	if err != nil {
		t.Fatalf("create giver: %v", err)
	}
	owner, err := users.Create(ctx, domain.User{AccessHash: 92, Phone: "+1778" + suffix + "62", FirstName: "Owner"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	ownerPeer := domain.Peer{Type: domain.PeerTypeUser, ID: owner.ID}

	// A real upgradeable gift with a full collectible pool, imported the same
	// way the admin panel does. Collectible attributes are immutable once
	// published, so these rows stay in the dedicated test database.
	gifts := NewStarGiftStore(pool)
	svc := stargiftsapp.NewService(gifts, &upgradeTestBlobs{store: map[string][]byte{}}, 2)
	manifest, assets, ok := giftpacks.Manifest("grind-kit")
	if !ok {
		t.Fatal("grind-kit pack missing")
	}
	var phone giftpack.GiftSpec
	for _, g := range manifest.Gifts {
		if g.IDSlug == "phone" {
			phone = g
		}
	}
	phone.Title += " " + suffix
	phone.Upgrade.SlugPrefix = "phone-" + suffix
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

	// A second gift can't publish under the same slug prefix (its "<prefix>-1"
	// would collide with this gift's); the importer moves it to "-2" and the
	// rejected attempt leaves nothing behind.
	again := manifest
	again.Gifts = []giftpack.GiftSpec{phone}
	again.Gifts[0].Title += " again"
	reimported, err := giftpack.Import(ctx, svc, again, assets, giftpack.ImportOptions{})
	if err != nil || len(reimported.Gifts) != 1 || reimported.Gifts[0].Status != "created" {
		t.Fatalf("re-import with a taken prefix = %+v err %v", reimported, err)
	}
	if preview, found, err := svc.CollectiblePreview(ctx, reimported.Gifts[0].GiftID); err != nil || !found || preview.SlugPrefix != phone.Upgrade.SlugPrefix+"-2" {
		t.Fatalf("re-imported prefix = %q found %v err %v, want %q", preview.SlugPrefix, found, err, phone.Upgrade.SlugPrefix+"-2")
	}
	if _, err := gifts.PublishCollectibleRevision(ctx, domain.StarGiftCollectibleWrite{
		GiftID: reimported.Gifts[0].GiftID, SlugPrefix: phone.Upgrade.SlugPrefix,
	}); err == nil {
		t.Fatal("publishing another gift's slug prefix succeeded, want it rejected")
	}

	now := int(time.Now().Unix())
	charge := gift.Stars + gift.UpgradeStars
	if _, _, err := NewStarsStore(pool).EnsureGrant(ctx, giver.ID, charge*10, now); err != nil {
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
		CommandKey: fmt.Sprintf("purchase:%d", form.FormID), Date: now,
	})
	if err != nil {
		t.Fatalf("purchase with prepaid upgrade: %v", err)
	}

	upgrades := NewStarGiftUpgradeStore(pool, messages)
	req := domain.StarGiftUpgradeRequest{
		UserID: owner.ID, Ref: domain.SavedStarGiftRef{Owner: ownerPeer, MsgID: purchase.Saved.MsgID},
		RequirePrepaid: true, CommandKey: "upgrade-" + suffix, Date: now + 1,
	}
	result, err := upgrades.UpgradeStarGift(ctx, req)
	if err != nil {
		t.Fatalf("prepaid upgrade: %v", err)
	}

	sent := result.Send
	if sent.SenderMessage.OwnerUserID != owner.ID || !sent.SenderMessage.Out {
		t.Fatalf("upgrade message author box = owner %d out %v, want the owner's outgoing copy",
			sent.SenderMessage.OwnerUserID, sent.SenderMessage.Out)
	}
	if sent.RecipientMessage.OwnerUserID != giver.ID || sent.RecipientMessage.Out {
		t.Fatalf("upgrade message recipient box = owner %d out %v, want the giver's incoming copy",
			sent.RecipientMessage.OwnerUserID, sent.RecipientMessage.Out)
	}
	for _, copy := range []domain.Message{sent.SenderMessage, sent.RecipientMessage} {
		action := copy.Media.ServiceAction.StarGiftUnique
		if action == nil || !action.Upgrade || !action.PrepaidUpgrade || action.FromUserID != 0 {
			t.Fatalf("user %d upgrade action = %+v, want upgrade+prepaid_upgrade with no from_id", copy.OwnerUserID, action)
		}
	}
	if result.Saved.UpgradeMsgID != sent.SenderMessage.ID {
		t.Fatalf("saved upgrade_msg_id = %d, want the owner's box id %d", result.Saved.UpgradeMsgID, sent.SenderMessage.ID)
	}
	ownerEdited := false
	for _, edit := range result.SourceEdits {
		if edit.UserID == owner.ID && edit.Message.Media.ServiceAction.StarGift.UpgradeMsgID == sent.SenderMessage.ID {
			ownerEdited = true
		}
	}
	if !ownerEdited {
		t.Fatalf("source gift message was not linked to the owner's upgrade message: %+v", result.SourceEdits)
	}

	replay, err := upgrades.UpgradeStarGift(ctx, req)
	if err != nil || !replay.Duplicate || replay.Unique.ID != result.Unique.ID {
		t.Fatalf("replay = duplicate %v unique %d err %v, want the same collectible %d", replay.Duplicate, replay.Unique.ID, err, result.Unique.ID)
	}
}
