package rpc

import (
	"testing"

	"github.com/iamxvbaba/td/tg"

	"telesrv/internal/domain"
)

// TestStarGiftUniqueActionForViewerFillsFromIDForOwner pins the Android "From
// <you>" header-pill bug fix: the upgrade message's from_id is intentionally
// left unset so TDesktop's own fallback (resolve to the chat's other peer for
// your own message) lands on the original giver -- but DrKLO Android has no
// such fallback and instead reads the message's own author, landing on the
// owner. Filling from_id explicitly for the owner's own view, from the
// unique gift's preserved original-details, fixes Android without touching
// TDesktop's wording (which already resolves to the same person either way).
func TestStarGiftUniqueActionForViewerFillsFromIDForOwner(t *testing.T) {
	const ownerID, giverID = 1780243214, 1780243858
	action := &domain.MessageStarGiftUniqueAction{
		Upgrade: true,
		Gift: domain.UniqueStarGift{
			ID: 900, Owner: domain.Peer{Type: domain.PeerTypeUser, ID: ownerID},
			KeepOriginalDetails: true, OriginalFromUserID: giverID,
		},
	}

	owner := tgMessageActionStarGiftUniqueForViewer(action, ownerID)
	unique, ok := owner.(*tg.MessageActionStarGiftUnique)
	if !ok {
		t.Fatalf("owner projection = %T, want *tg.MessageActionStarGiftUnique", owner)
	}
	fromID, hasFrom := unique.GetFromID()
	fromUser, isUser := fromID.(*tg.PeerUser)
	if !hasFrom || !isUser || fromUser.UserID != giverID {
		t.Fatalf("owner view from_id = (present %v, value %+v), want the original giver %d", hasFrom, fromID, giverID)
	}

	giver := tgMessageActionStarGiftUniqueForViewer(action, giverID)
	giverUnique, ok := giver.(*tg.MessageActionStarGiftUnique)
	if !ok {
		t.Fatalf("giver projection = %T, want *tg.MessageActionStarGiftUnique", giver)
	}
	if _, hasFrom := giverUnique.GetFromID(); hasFrom {
		t.Fatalf("giver view carries from_id, want it left unset (their own fallback already resolves correctly)")
	}
}

// TestStarGiftUniqueActionForViewerRespectsAnonymousGiver checks the from_id
// fill-in defers to the original giver's own hide-name choice: an anonymous
// gift must not leak its giver into the owner's own message copy either.
func TestStarGiftUniqueActionForViewerRespectsAnonymousGiver(t *testing.T) {
	const ownerID, giverID = 1780243214, 1780243858
	action := &domain.MessageStarGiftUniqueAction{
		Gift: domain.UniqueStarGift{
			ID: 901, Owner: domain.Peer{Type: domain.PeerTypeUser, ID: ownerID},
			KeepOriginalDetails: true, OriginalFromUserID: giverID, OriginalNameHidden: true,
		},
	}
	owner := tgMessageActionStarGiftUniqueForViewer(action, ownerID)
	unique, ok := owner.(*tg.MessageActionStarGiftUnique)
	if !ok {
		t.Fatalf("owner projection = %T, want *tg.MessageActionStarGiftUnique", owner)
	}
	if _, hasFrom := unique.GetFromID(); hasFrom {
		t.Fatalf("owner view carries from_id for an anonymous giver, want it left unset")
	}
}
