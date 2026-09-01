package postgres

import (
	"testing"

	"telesrv/internal/domain"
)

type fakePeerIdentityReadModelCache struct {
	peers   []domain.Peer
	flushes int
}

func (f *fakePeerIdentityReadModelCache) InvalidatePeerIdentityReadModel(peer domain.Peer) {
	f.peers = append(f.peers, peer)
}

func (f *fakePeerIdentityReadModelCache) FlushPeerIdentityReadModel() {
	f.flushes++
}

func TestReadModelListenerSeparatesPeerIdentityInvalidationDomain(t *testing.T) {
	projections := &fakeRPCProjectionReadModelCache{}
	identities := &fakePeerIdentityReadModelCache{}
	listener := NewReadModelChangeListener("", ReadModelCacheSet{
		RPCProjections: projections,
		PeerIdentities: identities,
	}, nil)

	listener.handlePayload(`{"model":"user_base","peer_type":"user","peer_id":71}`)
	listener.handlePayload(`{"model":"channel_base","peer_type":"channel","peer_id":72}`)
	if len(identities.peers) != 0 {
		t.Fatalf("base projection events invalidated peer identity: %+v", identities.peers)
	}
	if len(projections.users) != 1 || projections.users[0] != 71 || len(projections.channels) != 1 || projections.channels[0] != 72 {
		t.Fatalf("base projection invalidations users=%v channels=%v", projections.users, projections.channels)
	}

	listener.handlePayload(`{"model":"peer_identity","peer_type":"user","peer_id":71}`)
	listener.handlePayload(`{"model":"peer_identity","peer_type":"channel","peer_id":72}`)
	want := []domain.Peer{{Type: domain.PeerTypeUser, ID: 71}, {Type: domain.PeerTypeChannel, ID: 72}}
	if len(identities.peers) != len(want) || identities.peers[0] != want[0] || identities.peers[1] != want[1] {
		t.Fatalf("peer identity invalidations=%+v want=%+v", identities.peers, want)
	}
	if len(projections.users) != 2 || len(projections.channels) != 2 {
		t.Fatalf("identity event did not invalidate enclosing projections users=%v channels=%v", projections.users, projections.channels)
	}

	listener.flush("test")
	if identities.flushes != 1 || projections.flushes != 1 {
		t.Fatalf("flushes identities=%d projections=%d", identities.flushes, projections.flushes)
	}
}
