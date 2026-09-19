package postgres

import (
	"context"
	"testing"

	"telesrv/internal/domain"
)

func TestProjectPrivateStarGiftPurchaseScopesViewerCapabilities(t *testing.T) {
	media := &domain.MessageMedia{
		Kind: domain.MessageMediaKindService,
		ServiceAction: &domain.MessageServiceAction{
			Kind: domain.MessageServiceActionStarGift,
			StarGift: &domain.MessageStarGiftAction{
				PeerUserID: 200, To: domain.Peer{Type: domain.PeerTypeUser, ID: 200},
				CanUpgrade: true, PrepaidUpgradeHash: "prepaid-upgrade-hash-0123456789",
			},
		},
	}
	req := &domain.SendPrivateTextRequest{SenderUserID: 100, RecipientUserID: 200, Media: media}
	projection, err := projectPrivateStarGiftPurchase(context.Background(), nil, req)
	if err != nil {
		t.Fatalf("project purchase: %v", err)
	}
	shared := privateStarGiftAction(projection.Shared)
	sender := privateStarGiftAction(projection.Sender)
	recipient := privateStarGiftAction(projection.Recipient)
	if shared == nil || shared.CanUpgrade || shared.PrepaidUpgradeHash != "" {
		t.Fatalf("shared projection retained viewer capability: %+v", shared)
	}
	if sender == nil || sender.CanUpgrade || sender.PrepaidUpgradeHash == "" {
		t.Fatalf("sender projection = %+v, want hash without can_upgrade", sender)
	}
	if recipient == nil || !recipient.CanUpgrade || recipient.PrepaidUpgradeHash != "" {
		t.Fatalf("recipient projection = %+v, want can_upgrade without hash", recipient)
	}
	if original := privateStarGiftAction(media); original == nil || !original.CanUpgrade || original.PrepaidUpgradeHash == "" {
		t.Fatalf("source projection was mutated: %+v", original)
	}

	selfReq := &domain.SendPrivateTextRequest{SenderUserID: 200, RecipientUserID: 200, Media: media}
	selfProjection, err := projectPrivateStarGiftPurchase(context.Background(), nil, selfReq)
	if err != nil {
		t.Fatalf("project self purchase: %v", err)
	}
	self := privateStarGiftAction(selfProjection.Sender)
	if self == nil || !self.CanUpgrade || self.PrepaidUpgradeHash != "" {
		t.Fatalf("self-owner projection = %+v, want can_upgrade without hash", self)
	}

	bad := *req
	bad.RecipientUserID = 300
	if _, err := projectPrivateStarGiftPurchase(context.Background(), nil, &bad); err == nil {
		t.Fatal("mismatched gift owner and recipient was accepted")
	}
}

func TestTransferUniqueActionSavedIDNamespace(t *testing.T) {
	saved := domain.SavedStarGift{SavedID: 42, CanCraftAt: 1_780_000_123}
	unique := domain.UniqueStarGift{ID: 7}
	user := domain.Peer{Type: domain.PeerTypeUser, ID: 100}
	channel := domain.Peer{Type: domain.PeerTypeChannel, ID: 200}

	if action := transferUniqueAction(unique, 1, user, saved); action.SavedID != 0 || action.CanCraftAt != saved.CanCraftAt {
		t.Fatalf("user transfer action leaked channel saved_id: %+v", action)
	}
	if action := transferUniqueAction(unique, 1, channel, saved); action.SavedID != saved.SavedID || action.CanCraftAt != 0 {
		t.Fatalf("channel transfer action lost channel saved_id: %+v", action)
	}
}

func TestStarGiftCraftReadyAt(t *testing.T) {
	const date = 1_780_000_000
	if got := starGiftCraftReadyAt(date, 0); got != date {
		t.Fatalf("zero-delay craft ready_at = %d, want %d", got, date)
	}
	if got := starGiftCraftReadyAt(date, 60); got != date+60 {
		t.Fatalf("delayed craft ready_at = %d, want %d", got, date+60)
	}
	if got := starGiftCraftReadyAt(0, 0); got != 0 {
		t.Fatalf("invalid-date craft ready_at = %d, want 0", got)
	}
	if got := starGiftCraftReadyAt(1<<31-10, 60); got != 1<<31-1 {
		t.Fatalf("overflow craft ready_at = %d, want max int32", got)
	}
	if got := starGiftCraftReadyAt(1<<31+10, 0); got != 1<<31-1 {
		t.Fatalf("oversized-date craft ready_at = %d, want max int32", got)
	}
}

func TestStarGiftCraftInputAvailable(t *testing.T) {
	owner := domain.Peer{Type: domain.PeerTypeUser, ID: 100}
	base := domain.UniqueStarGift{
		Owner:               owner,
		CraftChancePermille: 250,
	}

	tests := []struct {
		name     string
		gift     domain.UniqueStarGift
		survivor bool
		want     bool
	}{
		{name: "plain survivor", gift: base, survivor: true, want: true},
		{name: "addressed survivor", gift: func() domain.UniqueStarGift {
			gift := base
			gift.GiftAddress = "EQcraft"
			return gift
		}(), survivor: true, want: false},
		{name: "addressed burn-only input", gift: func() domain.UniqueStarGift {
			gift := base
			gift.GiftAddress = "EQburn"
			return gift
		}(), survivor: false, want: true},
		{name: "on-chain owner", gift: func() domain.UniqueStarGift {
			gift := base
			gift.OwnerAddress = "EQowner"
			return gift
		}(), survivor: false, want: false},
		{name: "burned", gift: func() domain.UniqueStarGift {
			gift := base
			gift.Burned = true
			return gift
		}(), survivor: false, want: false},
		{name: "wrong owner", gift: func() domain.UniqueStarGift {
			gift := base
			gift.Owner.ID++
			return gift
		}(), survivor: false, want: false},
		{name: "no chance", gift: func() domain.UniqueStarGift {
			gift := base
			gift.CraftChancePermille = 0
			return gift
		}(), survivor: false, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := starGiftCraftInputAvailable(tt.gift, owner, tt.survivor); got != tt.want {
				t.Fatalf("starGiftCraftInputAvailable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEncodeSharedPrivateStarGiftMediaOmitsUserBoxLocalRefs(t *testing.T) {
	ordinary := &domain.MessageMedia{
		Kind: domain.MessageMediaKindService,
		ServiceAction: &domain.MessageServiceAction{
			Kind: domain.MessageServiceActionStarGift,
			StarGift: &domain.MessageStarGiftAction{
				PeerUserID: 9,
				SavedID:    10, GiftMsgID: 11, UpgradeMsgID: 12,
			},
		},
	}
	encoded, err := encodeSharedPrivateStarGiftMedia(ordinary)
	if err != nil {
		t.Fatalf("encode ordinary shared projection: %v", err)
	}
	sharedOrdinary, err := decodeMessageMedia(string(encoded))
	if err != nil {
		t.Fatalf("decode ordinary shared projection: %v", err)
	}
	ordinaryAction := sharedOrdinary.ServiceAction.StarGift
	if ordinaryAction.SavedID != 0 || ordinaryAction.GiftMsgID != 0 || ordinaryAction.UpgradeMsgID != 0 {
		t.Fatalf("ordinary shared projection retained box-local refs: %+v", ordinaryAction)
	}
	if original := ordinary.ServiceAction.StarGift; original.SavedID != 10 || original.GiftMsgID != 11 || original.UpgradeMsgID != 12 {
		t.Fatalf("ordinary source projection was mutated: %+v", original)
	}

	unique := &domain.MessageMedia{
		Kind: domain.MessageMediaKindService,
		ServiceAction: &domain.MessageServiceAction{
			Kind: domain.MessageServiceActionStarGiftUnique,
			StarGiftUnique: &domain.MessageStarGiftUniqueAction{
				Peer: domain.Peer{Type: domain.PeerTypeUser, ID: 9}, SavedID: 13,
			},
		},
	}
	encoded, err = encodeSharedPrivateStarGiftMedia(unique)
	if err != nil {
		t.Fatalf("encode unique shared projection: %v", err)
	}
	sharedUnique, err := decodeMessageMedia(string(encoded))
	if err != nil {
		t.Fatalf("decode unique shared projection: %v", err)
	}
	if action := sharedUnique.ServiceAction.StarGiftUnique; action.SavedID != 0 {
		t.Fatalf("unique shared projection retained user saved_id: %+v", action)
	}
	if unique.ServiceAction.StarGiftUnique.SavedID != 13 {
		t.Fatalf("unique source projection was mutated: %+v", unique.ServiceAction.StarGiftUnique)
	}
}

func TestEncodeSharedPrivateStarGiftMediaPreservesChannelSavedID(t *testing.T) {
	media := &domain.MessageMedia{
		Kind: domain.MessageMediaKindService,
		ServiceAction: &domain.MessageServiceAction{
			Kind: domain.MessageServiceActionStarGiftUnique,
			StarGiftUnique: &domain.MessageStarGiftUniqueAction{
				Peer: domain.Peer{Type: domain.PeerTypeChannel, ID: 9}, SavedID: 14,
			},
		},
	}
	encoded, err := encodeSharedPrivateStarGiftMedia(media)
	if err != nil {
		t.Fatalf("encode channel shared projection: %v", err)
	}
	shared, err := decodeMessageMedia(string(encoded))
	if err != nil {
		t.Fatalf("decode channel shared projection: %v", err)
	}
	if action := shared.ServiceAction.StarGiftUnique; action.SavedID != 14 {
		t.Fatalf("channel shared projection lost saved_id: %+v", action)
	}
}
