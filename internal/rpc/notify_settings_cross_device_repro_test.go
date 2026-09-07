package rpc

import (
	"context"
	"testing"

	"github.com/iamxvbaba/td/clock"
	"github.com/iamxvbaba/td/tg"
	"go.uber.org/zap/zaptest"

	appaccount "telesrv/internal/app/account"
	appupdates "telesrv/internal/app/updates"
	appusers "telesrv/internal/app/users"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

// TestNotifySettingsMuteFromOneDeviceReachesAnotherViaDifference reproduces the reported
// symptom: muting a chat from one device (PC) does not apply on another device of the SAME
// account (phone), even after a full app restart (which does updates.getDifference, not a
// live push). This test drives the real durable pipeline end to end (real appupdates.Service +
// appaccount.Service, in-memory stores) instead of the captureUpdates fake, to catch anything
// the fake's simplistic recording hides.
func TestNotifySettingsMuteFromOneDeviceReachesAnotherViaDifference(t *testing.T) {
	passwordStore := memory.NewPasswordStore()
	updateStateStore := memory.NewUpdateStateStore()
	updateEventStore := memory.NewUpdateEventStore()
	userStore := memory.NewUserStore()
	r := New(Config{}, Deps{
		Account: appaccount.NewService(passwordStore, appaccount.WithNotifySettings(passwordStore)),
		Updates: appupdates.NewService(updateStateStore, updateEventStore),
		Users:   appusers.NewService(userStore),
	}, zaptest.NewLogger(t), clock.System)

	peerUser, err := userStore.Create(context.Background(), domain.User{AccessHash: 44, Phone: "15550009001", FirstName: "Peer"})
	if err != nil {
		t.Fatalf("create peer user: %v", err)
	}
	ownerUser, err := userStore.Create(context.Background(), domain.User{AccessHash: 45, Phone: "15550009002", FirstName: "Owner"})
	if err != nil {
		t.Fatalf("create owner user: %v", err)
	}
	peerID := peerUser.ID
	owner := ownerUser.ID

	// Phone is "at" pts=0 (fresh install / just opened), before the PC mutes anything.
	phoneCtx := WithAuthKeyID(WithUserID(context.Background(), owner), [8]byte{2, 2, 2})
	baseline, err := r.onUpdatesGetDifference(phoneCtx, &tg.UpdatesGetDifferenceRequest{Pts: 0})
	if err != nil {
		t.Fatalf("phone baseline getDifference: %v", err)
	}
	t.Logf("phone baseline = %#v", baseline)
	var baselinePts int
	switch d := baseline.(type) {
	case *tg.UpdatesDifference:
		baselinePts = d.State.Pts
	case *tg.UpdatesDifferenceEmpty:
		baselinePts = 0
	case *tg.UpdatesDifferenceSlice:
		baselinePts = d.IntermediateState.Pts
	default:
		t.Fatalf("unexpected baseline type %T", baseline)
	}

	// PC (different session, same user) mutes a specific peer.
	pcCtx := WithSessionID(WithAuthKeyID(WithUserID(context.Background(), owner), [8]byte{1, 1, 1}), 77)
	in := tg.InputPeerNotifySettings{}
	in.SetMuteUntil(2000000000)
	peerInput := &tg.InputNotifyPeer{Peer: &tg.InputPeerUser{UserID: peerID}}
	if ok, err := r.onAccountUpdateNotifySettings(pcCtx, &tg.AccountUpdateNotifySettingsRequest{Peer: peerInput, Settings: in}); err != nil || !ok {
		t.Fatalf("PC mute = ok %v err %v", ok, err)
	}

	// Phone reconnects / restarts and asks for what changed since its baseline.
	diff, err := r.onUpdatesGetDifference(phoneCtx, &tg.UpdatesGetDifferenceRequest{Pts: baselinePts})
	if err != nil {
		t.Fatalf("phone catch-up getDifference: %v", err)
	}
	t.Logf("phone catch-up diff = %#v", diff)
	d, ok := diff.(*tg.UpdatesDifference)
	if !ok {
		t.Fatalf("phone catch-up diff type = %T, want *tg.UpdatesDifference containing the mute", diff)
	}
	t.Logf("other updates = %#v", d.OtherUpdates)
	found := false
	for _, u := range d.OtherUpdates {
		if ns, ok := u.(*tg.UpdateNotifySettings); ok {
			t.Logf("found UpdateNotifySettings: %#v peer=%#v settings=%#v", ns, ns.Peer, ns.NotifySettings)
			if np, ok := ns.Peer.(*tg.NotifyPeer); ok {
				if pu, ok := np.Peer.(*tg.PeerUser); ok && pu.UserID == peerID {
					if mu, hasMu := ns.NotifySettings.GetMuteUntil(); hasMu && mu == 2000000000 {
						found = true
					}
				}
			}
		}
	}
	if !found {
		t.Fatalf("phone's getDifference does not contain the PC's mute of peer %d -- this is the reported bug", peerID)
	}
}
