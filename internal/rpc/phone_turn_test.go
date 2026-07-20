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
	extra    []string
	enabled  bool
	credUser string
	credPass string
}

func (f *fakeTURN) Enabled() bool { return f.enabled }
func (f *fakeTURN) Credentials(string) (string, string, error) {
	return f.credUser, f.credPass, nil
}
func (f *fakeTURN) IP() string         { return f.ip }
func (f *fakeTURN) Port() int          { return f.port }
func (f *fakeTURN) ExtraIPs() []string { return f.extra }
func (f *fakeTURN) Close() error       { return nil }

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

func TestPhoneCallConnectionsSingleIP(t *testing.T) {
	r := newTURNRouter(t, &fakeTURN{
		enabled: true, ip: "89.28.58.29", port: 12400,
		credUser: "u", credPass: "p",
	})
	conns := r.phoneCallConnections(1)
	if len(conns) != 2 {
		t.Fatalf("single IP: want 2 conns (stun+turn), got %d: %+v", len(conns), conns)
	}
	if !conns[0].Stun || conns[0].Turn || conns[0].ID != 1 || conns[0].IP != "89.28.58.29" {
		t.Fatalf("conn[0] should be STUN id1 on public IP, got %+v", conns[0])
	}
	if !conns[1].Turn || conns[1].Stun || conns[1].ID != 2 || conns[1].Username != "u" || conns[1].Password != "p" {
		t.Fatalf("conn[1] should be TURN id2 with creds, got %+v", conns[1])
	}
}

func TestPhoneCallConnectionsExtraIPsAddCandidatesWithUniqueIDs(t *testing.T) {
	r := newTURNRouter(t, &fakeTURN{
		enabled: true, ip: "89.28.58.29", port: 12400,
		extra:    []string{"192.168.0.20", ""}, // empty entry must be skipped
		credUser: "u", credPass: "p",
	})
	conns := r.phoneCallConnections(1)
	// public (stun+turn) + LAN (stun+turn) = 4; the empty extra IP is skipped.
	if len(conns) != 4 {
		t.Fatalf("want 4 conns, got %d: %+v", len(conns), conns)
	}
	seenIDs := map[int64]bool{}
	for _, c := range conns {
		if seenIDs[c.ID] {
			t.Fatalf("duplicate connection id %d: %+v", c.ID, conns)
		}
		seenIDs[c.ID] = true
		if c.IP == "" {
			t.Fatalf("empty IP leaked into connections: %+v", conns)
		}
	}
	// IDs must be a contiguous 1..4 run (DrKLO maps reflectors by id).
	for id := int64(1); id <= 4; id++ {
		if !seenIDs[id] {
			t.Fatalf("missing contiguous id %d: %+v", id, conns)
		}
	}
	// The LAN IP must appear as both a STUN and a TURN candidate.
	var lanStun, lanTurn bool
	for _, c := range conns {
		if c.IP == "192.168.0.20" && c.Stun {
			lanStun = true
		}
		if c.IP == "192.168.0.20" && c.Turn {
			lanTurn = true
		}
	}
	if !lanStun || !lanTurn {
		t.Fatalf("LAN IP must yield both STUN and TURN candidates, got %+v", conns)
	}
}
