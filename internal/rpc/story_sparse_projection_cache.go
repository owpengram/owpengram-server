package rpc

import (
	"context"
	"errors"

	appreadmodel "telesrv/internal/app/readmodel"
	"telesrv/internal/domain"
	"telesrv/internal/readmodelcache"
	"telesrv/internal/store"
)

const (
	defaultStoryActivePeerCacheMaxEntries = 1_000_000
	defaultStoryHiddenListCacheMaxEntries = 100_000
	defaultStoryHiddenListCacheMaxBytes   = 64 << 20
	missingStoryReadModelHash             = int64(-1)
)

var errStorySparseProjectionUnavailable = errors.New("story sparse projection read model unavailable")

type storySparseProjectionProvider interface {
	ActiveStoryPeerExpirations(ctx context.Context, peers []domain.Peer, now int) (map[domain.Peer]int, error)
	ListHiddenStoryPeers(ctx context.Context, viewerUserID int64) ([]domain.Peer, error)
}

type activeStoryPeerFact struct {
	maxExpireAt int
}

type hiddenStoryPeerSet map[domain.Peer]struct{}

// storySparseProjectionCache avoids a viewer×peer negative matrix. Active
// existence is shared by peer; hidden preferences are stored once per viewer.
type storySparseProjectionCache struct {
	versions store.ReadModelVersionStore
	active   *readmodelcache.Cache[domain.Peer, activeStoryPeerFact]
	hidden   *readmodelcache.Cache[int64, hiddenStoryPeerSet]
}

func newStorySparseProjectionCache(
	versions store.ReadModelVersionStore,
	activeMaxEntries int,
	hiddenMaxEntries int,
	hiddenMaxBytes int64,
) *storySparseProjectionCache {
	if versions == nil {
		return nil
	}
	if activeMaxEntries <= 0 {
		activeMaxEntries = defaultStoryActivePeerCacheMaxEntries
	}
	if hiddenMaxEntries <= 0 {
		hiddenMaxEntries = defaultStoryHiddenListCacheMaxEntries
	}
	if hiddenMaxBytes <= 0 {
		hiddenMaxBytes = defaultStoryHiddenListCacheMaxBytes
	}
	return &storySparseProjectionCache{
		versions: versions,
		active: readmodelcache.New[domain.Peer, activeStoryPeerFact](readmodelcache.Config[domain.Peer, activeStoryPeerFact]{
			MaxEntries: activeMaxEntries,
		}),
		hidden: readmodelcache.New[int64, hiddenStoryPeerSet](readmodelcache.Config[int64, hiddenStoryPeerSet]{
			MaxEntries: hiddenMaxEntries,
			MaxWeight:  hiddenMaxBytes,
			Weight: func(value hiddenStoryPeerSet) int64 {
				return 64 + int64(len(value))*32
			},
			Clone: cloneHiddenStoryPeerSet,
		}),
	}
}

func (c *storySparseProjectionCache) activePeers(
	ctx context.Context,
	provider storySparseProjectionProvider,
	peers []domain.Peer,
	now int,
) ([]domain.Peer, error) {
	if c == nil || c.versions == nil || c.active == nil || provider == nil {
		return nil, errStorySparseProjectionUnavailable
	}
	peers = uniqueStoryProjectionPeers(peers)
	if len(peers) == 0 {
		return nil, nil
	}
	for _, peer := range peers {
		if fact, ok := c.active.Peek(peer); ok && fact.maxExpireAt > 0 && fact.maxExpireAt <= now {
			c.active.Invalidate(peer)
		}
	}
	keys := make([]store.ReadModelKey, 0, len(peers))
	for _, peer := range peers {
		keys = append(keys, store.ReadModelKey{
			Model: appreadmodel.ModelStoryPeer, PeerType: peer.Type, PeerID: peer.ID,
		})
	}
	hashes, err := c.versions.ReadModelHashes(ctx, keys)
	if err != nil {
		return nil, err
	}
	peerHashes := make(map[domain.Peer]int64, len(peers))
	for _, key := range keys {
		hash := hashes[key]
		if hash == 0 {
			hash = missingStoryReadModelHash
		}
		peerHashes[domain.Peer{Type: key.PeerType, ID: key.PeerID}] = hash
	}
	values, err := c.active.GetOrLoadBatch(ctx, peers,
		func(peer domain.Peer) (int64, bool) { return peerHashes[peer], true },
		func(ctx context.Context, missing []domain.Peer) (map[domain.Peer]activeStoryPeerFact, error) {
			expirations, err := provider.ActiveStoryPeerExpirations(ctx, missing, now)
			if err != nil {
				return nil, err
			}
			out := make(map[domain.Peer]activeStoryPeerFact, len(missing))
			for _, peer := range missing {
				out[peer] = activeStoryPeerFact{maxExpireAt: expirations[peer]}
			}
			return out, nil
		})
	if err != nil {
		return nil, err
	}
	out := make([]domain.Peer, 0, len(peers))
	for _, peer := range peers {
		if values[peer].maxExpireAt > now {
			out = append(out, peer)
		}
	}
	return out, nil
}

func (c *storySparseProjectionCache) hiddenPeers(
	ctx context.Context,
	provider storySparseProjectionProvider,
	viewerUserID int64,
) (hiddenStoryPeerSet, error) {
	if c == nil || c.versions == nil || c.hidden == nil || provider == nil || viewerUserID == 0 {
		return nil, errStorySparseProjectionUnavailable
	}
	key := store.ReadModelKey{
		Model:       appreadmodel.ModelStoryHiddenList,
		OwnerUserID: viewerUserID,
		PeerType:    domain.PeerTypeUser,
		PeerID:      viewerUserID,
	}
	hash, _, err := c.versions.ReadModelHash(ctx, key.Model, key.OwnerUserID, key.PeerType, key.PeerID)
	if err != nil {
		return nil, err
	}
	if hash == 0 {
		hash = missingStoryReadModelHash
	}
	return c.hidden.GetOrLoadVersioned(ctx, viewerUserID, hash, func() (hiddenStoryPeerSet, error) {
		peers, err := provider.ListHiddenStoryPeers(ctx, viewerUserID)
		if err != nil {
			return nil, err
		}
		out := make(hiddenStoryPeerSet, len(peers))
		for _, peer := range peers {
			if peer.ID != 0 {
				out[peer] = struct{}{}
			}
		}
		return out, nil
	})
}

func (c *storySparseProjectionCache) DeletePeer(peer domain.Peer) {
	if c != nil && peer.ID != 0 {
		c.active.Invalidate(peer)
	}
}

func (c *storySparseProjectionCache) DeleteViewer(viewerUserID int64) {
	if c != nil && viewerUserID != 0 {
		c.hidden.Invalidate(viewerUserID)
	}
}

func (c *storySparseProjectionCache) Flush() {
	if c != nil {
		c.active.Flush()
		c.hidden.Flush()
	}
}

func cloneHiddenStoryPeerSet(in hiddenStoryPeerSet) hiddenStoryPeerSet {
	if in == nil {
		return hiddenStoryPeerSet{}
	}
	out := make(hiddenStoryPeerSet, len(in))
	for peer := range in {
		out[peer] = struct{}{}
	}
	return out
}

func uniqueStoryProjectionPeers(peers []domain.Peer) []domain.Peer {
	out := make([]domain.Peer, 0, len(peers))
	seen := make(map[domain.Peer]struct{}, len(peers))
	for _, peer := range peers {
		if peer.ID == 0 || (peer.Type != domain.PeerTypeUser && peer.Type != domain.PeerTypeChannel) {
			continue
		}
		if _, ok := seen[peer]; ok {
			continue
		}
		seen[peer] = struct{}{}
		out = append(out, peer)
	}
	return out
}
