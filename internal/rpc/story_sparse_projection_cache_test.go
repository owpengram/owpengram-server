package rpc

import (
	"context"
	"errors"
	"testing"

	appreadmodel "telesrv/internal/app/readmodel"
	"telesrv/internal/domain"
	"telesrv/internal/store"
)

type fakeStorySparseProjectionProvider struct {
	expirations map[domain.Peer]int
	hidden      map[int64][]domain.Peer
	activeCalls int
	hiddenCalls int
	err         error
}

func (f *fakeStorySparseProjectionProvider) ActiveStoryPeerExpirations(_ context.Context, peers []domain.Peer, _ int) (map[domain.Peer]int, error) {
	f.activeCalls++
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[domain.Peer]int, len(peers))
	for _, peer := range peers {
		if expireAt := f.expirations[peer]; expireAt != 0 {
			out[peer] = expireAt
		}
	}
	return out, nil
}

func (f *fakeStorySparseProjectionProvider) ListHiddenStoryPeers(_ context.Context, viewerUserID int64) ([]domain.Peer, error) {
	f.hiddenCalls++
	if f.err != nil {
		return nil, f.err
	}
	return append([]domain.Peer(nil), f.hidden[viewerUserID]...), nil
}

func storyPeerVersionKey(peer domain.Peer) store.ReadModelKey {
	return store.ReadModelKey{Model: appreadmodel.ModelStoryPeer, PeerType: peer.Type, PeerID: peer.ID}
}

func storyHiddenListVersionKey(viewerUserID int64) store.ReadModelKey {
	return store.ReadModelKey{
		Model: appreadmodel.ModelStoryHiddenList, OwnerUserID: viewerUserID,
		PeerType: domain.PeerTypeUser, PeerID: viewerUserID,
	}
}

func TestStorySparseProjectionCacheSharesNegativeCandidatesAndExpiresPositiveFacts(t *testing.T) {
	ctx := context.Background()
	active := domain.Peer{Type: domain.PeerTypeUser, ID: 11}
	inactive := domain.Peer{Type: domain.PeerTypeChannel, ID: 22}
	versions := &fakeRPCReadModelVersions{hashes: map[store.ReadModelKey]int64{
		storyPeerVersionKey(active):   101,
		storyPeerVersionKey(inactive): 102,
	}}
	provider := &fakeStorySparseProjectionProvider{expirations: map[domain.Peer]int{active: 200}}
	cache := newStorySparseProjectionCache(versions, 10, 10, 1024)

	for i := 0; i < 2; i++ {
		got, err := cache.activePeers(ctx, provider, []domain.Peer{active, inactive, active}, 100)
		if err != nil || len(got) != 1 || got[0] != active {
			t.Fatalf("activePeers(%d) = %+v, %v", i, got, err)
		}
	}
	if provider.activeCalls != 1 {
		t.Fatalf("shared positive/negative candidate loads = %d, want 1", provider.activeCalls)
	}

	delete(provider.expirations, active)
	got, err := cache.activePeers(ctx, provider, []domain.Peer{active, inactive}, 200)
	if err != nil || len(got) != 0 {
		t.Fatalf("activePeers at expire boundary = %+v, %v", got, err)
	}
	if provider.activeCalls != 2 {
		t.Fatalf("expired positive candidate loads = %d, want 2", provider.activeCalls)
	}
}

func TestStorySparseProjectionCacheVersionsHiddenViewerSnapshot(t *testing.T) {
	ctx := context.Background()
	const viewerID int64 = 77
	hiddenPeer := domain.Peer{Type: domain.PeerTypeUser, ID: 88}
	key := storyHiddenListVersionKey(viewerID)
	versions := &fakeRPCReadModelVersions{hashes: map[store.ReadModelKey]int64{key: 201}}
	provider := &fakeStorySparseProjectionProvider{hidden: map[int64][]domain.Peer{viewerID: {hiddenPeer}}}
	cache := newStorySparseProjectionCache(versions, 10, 10, 1024)

	for i := 0; i < 2; i++ {
		got, err := cache.hiddenPeers(ctx, provider, viewerID)
		if err != nil {
			t.Fatalf("hiddenPeers(%d): %v", i, err)
		}
		if _, ok := got[hiddenPeer]; !ok {
			t.Fatalf("hiddenPeers(%d) = %+v, want hidden peer", i, got)
		}
	}
	if provider.hiddenCalls != 1 {
		t.Fatalf("hidden snapshot loads = %d, want 1", provider.hiddenCalls)
	}

	provider.hidden[viewerID] = nil
	versions.hashes[key] = 202
	got, err := cache.hiddenPeers(ctx, provider, viewerID)
	if err != nil || len(got) != 0 {
		t.Fatalf("hiddenPeers after version bump = %+v, %v", got, err)
	}
	if provider.hiddenCalls != 2 {
		t.Fatalf("hidden snapshot loads after version bump = %d, want 2", provider.hiddenCalls)
	}
}

func TestStorySparseProjectionCacheDoesNotCacheBackendErrors(t *testing.T) {
	ctx := context.Background()
	peer := domain.Peer{Type: domain.PeerTypeUser, ID: 99}
	const viewerID int64 = 100
	versions := &fakeRPCReadModelVersions{hashes: map[store.ReadModelKey]int64{
		storyPeerVersionKey(peer):           301,
		storyHiddenListVersionKey(viewerID): 302,
	}}
	provider := &fakeStorySparseProjectionProvider{expirations: map[domain.Peer]int{peer: 500}, err: errors.New("backend unavailable")}
	cache := newStorySparseProjectionCache(versions, 10, 10, 1024)

	if _, err := cache.activePeers(ctx, provider, []domain.Peer{peer}, 100); err == nil {
		t.Fatal("active candidate error = nil")
	}
	if _, err := cache.hiddenPeers(ctx, provider, viewerID); err == nil {
		t.Fatal("hidden snapshot error = nil")
	}
	provider.err = nil
	if got, err := cache.activePeers(ctx, provider, []domain.Peer{peer}, 100); err != nil || len(got) != 1 {
		t.Fatalf("active candidate recovery = %+v, %v", got, err)
	}
	if got, err := cache.hiddenPeers(ctx, provider, viewerID); err != nil || len(got) != 0 {
		t.Fatalf("hidden snapshot recovery = %+v, %v", got, err)
	}
	if provider.activeCalls != 2 || provider.hiddenCalls != 2 {
		t.Fatalf("backend retries active=%d hidden=%d, want 2/2", provider.activeCalls, provider.hiddenCalls)
	}
}
