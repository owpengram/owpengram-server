package dialogs

import (
	"testing"

	"telesrv/internal/domain"
)

func TestDialogOwnerSnapshotStructuralHashCoversMaterializedOwnerFacts(t *testing.T) {
	base := domain.Dialog{
		Peer:                      domain.Peer{Type: domain.PeerTypeChannel, ID: 10},
		TopMessage:                20,
		TopMessageDate:            30,
		Pts:                       40,
		HistoryClearAnchorID:      50,
		HistoryClearAnchorDate:    60,
		TopMessageUnreadProjected: true,
		DefaultSendAs:             &domain.Peer{Type: domain.PeerTypeChannel, ID: 70},
	}
	wantDifferent := []domain.Dialog{
		func() domain.Dialog { out := cloneDialog(base); out.Pts++; return out }(),
		func() domain.Dialog { out := cloneDialog(base); out.HistoryClearAnchorID++; return out }(),
		func() domain.Dialog { out := cloneDialog(base); out.TopMessageUnreadProjected = false; return out }(),
		func() domain.Dialog { out := cloneDialog(base); out.DefaultSendAs.ID++; return out }(),
	}
	baseHash := dialogOwnerSnapshotStructuralHash([]domain.Dialog{base}, 0)
	for index, changed := range wantDifferent {
		if got := dialogOwnerSnapshotStructuralHash([]domain.Dialog{changed}, 0); got == baseHash {
			t.Fatalf("materialized owner fact case %d did not change structural hash", index)
		}
	}
}

func TestDialogListSnapshotHashCoversMaterializedDraft(t *testing.T) {
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: 10}
	without := newDialogListSnapshot(domain.DialogList{Dialogs: []domain.Dialog{{Peer: peer}}})
	with := newDialogListSnapshot(domain.DialogList{Dialogs: []domain.Dialog{{
		Peer: peer, Draft: &domain.DialogDraft{Peer: peer, Date: 20, Message: "draft"},
	}}})
	if without.hash == with.hash {
		t.Fatalf("draft did not change snapshot hash: %d", without.hash)
	}
}
