package rpc

import (
	"testing"

	"github.com/iamxvbaba/td/clock"
	"go.uber.org/zap"
)

// fakeTURN is a minimal turnsrv.Service for exercising phoneCallConnections.
type fakeTURN struct {
	ip       string
	port     int
	enabled  bool
	credUser string
	credPass string
}

func (f *fakeTURN) Enabled() bool { return f.enabled }
func (f *fakeTURN) Credentials(string) (string, string, error) {
	return f.credUser, f.credPass, nil
}
func (f *fakeTURN) IP() string   { return f.ip }
func (f *fakeTURN) Port() int    { return f.port }
func (f *fakeTURN) Close() error { return nil }

func newTURNRouter(t *testing.T, turn *fakeTURN) *Router {
	t.Helper()
	return New(Config{}, Deps{TURN: turn}, zap.NewNop(), clock.System)
}

func TestPhoneCallConnectionsDisabledTURNReturnsNil(t *testing.T) {
	r := newTURNRouter(t, &fakeTURN{enabled: false})
	if conns := r.phoneCallConnections(1); conns != nil {
		t.Fatalf("disabled TURN: want nil, got %+v", conns)
	}
}

func TestPhoneCallConnectionsSplitStunAndTurn(t *testing.T) {
	r := newTURNRouter(t, &fakeTURN{
		enabled: true, ip: "89.28.58.29", port: 12400,
		credUser: "u", credPass: "p",
	})
	conns := r.phoneCallConnections(1)
	if len(conns) != 2 {
		t.Fatalf("want 2 conns (stun+turn), got %d: %+v", len(conns), conns)
	}
	// STUN and TURN must be SEPARATE entries (DrKLO's JNI ignores the stun flag
	// on a combined entry, dropping STUN).
	if !conns[0].Stun || conns[0].Turn || conns[0].ID != 1 || conns[0].IP != "89.28.58.29" {
		t.Fatalf("conn[0] should be STUN id1, got %+v", conns[0])
	}
	if !conns[1].Turn || conns[1].Stun || conns[1].ID != 2 || conns[1].Username != "u" || conns[1].Password != "p" {
		t.Fatalf("conn[1] should be TURN id2 with creds, got %+v", conns[1])
	}
}
