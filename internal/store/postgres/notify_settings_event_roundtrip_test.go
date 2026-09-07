package postgres

import (
	"context"
	"testing"

	"telesrv/internal/domain"
)

// TestNotifySettingsEventSurvivesPostgresRoundTrip is a regression test for a real production
// incident: RecordNotifySettings set domain.UpdateEvent.NotifyPeerSettings only in memory, but
// appendUserUpdateEvent/ListAfter never serialized that field to/from Postgres. The write
// itself succeeded, but every later read (outbox dispatch batching, updates.getDifference)
// got NotifyPeerSettings == nil back, so convert_updates.go's UpdateEventNotifySettings case
// silently produced no TL update ("non-noop outbox event produced no update"). Worse: the
// stuck outbox row blocked that account's entire dispatch lane, so unrelated updates (new
// messages) stopped reaching other sessions until a full resync. This test appends a real
// notify_settings event and reads it back through the same store, asserting the settings
// survive the round trip.
func TestNotifySettingsEventSurvivesPostgresRoundTrip(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{AccessHash: 51, Phone: "+1668" + suffix + "01", FirstName: "Owner"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	peerUser, err := users.Create(ctx, domain.User{AccessHash: 52, Phone: "+1668" + suffix + "02", FirstName: "Peer"})
	if err != nil {
		t.Fatalf("create peer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", []int64{owner.ID, peerUser.ID})
	})

	events := NewUpdateEventStore(pool)
	muteUntil := 2000000000
	showPreviews := false
	appended, err := events.AppendAllocatedWithDispatch(ctx, owner.ID, domain.UpdateEvent{
		Type: domain.UpdateEventNotifySettings,
		Peer: domain.Peer{Type: domain.PeerTypeUser, ID: peerUser.ID},
		NotifyPeerSettings: &domain.PeerNotifySettings{
			MuteUntil:    &muteUntil,
			ShowPreviews: &showPreviews,
		},
		PtsCount: 1,
	}, [8]byte{}, 0)
	if err != nil {
		t.Fatalf("AppendAllocatedWithDispatch: %v", err)
	}
	t.Logf("appended event = %+v", appended)
	if appended.NotifyPeerSettings == nil {
		t.Fatalf("appended.NotifyPeerSettings = nil immediately after append, want the settings we sent")
	}

	// Read it back exactly the way updates.getDifference does.
	replayed, err := events.ListAfter(ctx, owner.ID, appended.Pts-1, 10)
	if err != nil {
		t.Fatalf("ListAfter: %v", err)
	}
	t.Logf("replayed events = %+v", replayed)
	var found *domain.UpdateEvent
	for i := range replayed {
		if replayed[i].Type == domain.UpdateEventNotifySettings && replayed[i].Pts == appended.Pts {
			found = &replayed[i]
		}
	}
	if found == nil {
		t.Fatalf("notify_settings event at pts=%d not found in ListAfter result", appended.Pts)
	}
	if found.NotifyPeerSettings == nil {
		t.Fatalf("replayed event.NotifyPeerSettings = nil -- THE BUG: settings were lost across the Postgres round trip, so getDifference/outbox dispatch cannot build a TL update for this event")
	}
	if found.NotifyPeerSettings.MuteUntil == nil || *found.NotifyPeerSettings.MuteUntil != muteUntil {
		t.Fatalf("replayed MuteUntil = %+v, want %d", found.NotifyPeerSettings.MuteUntil, muteUntil)
	}
	if found.NotifyPeerSettings.ShowPreviews == nil || *found.NotifyPeerSettings.ShowPreviews != showPreviews {
		t.Fatalf("replayed ShowPreviews = %+v, want %v", found.NotifyPeerSettings.ShowPreviews, showPreviews)
	}
	if found.Peer != (domain.Peer{Type: domain.PeerTypeUser, ID: peerUser.ID}) {
		t.Fatalf("replayed Peer = %+v, want peer %d", found.Peer, peerUser.ID)
	}
}
