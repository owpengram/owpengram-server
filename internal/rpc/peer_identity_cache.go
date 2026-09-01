package rpc

import (
	"context"

	"telesrv/internal/domain"
	"telesrv/internal/readmodelcache"
	"telesrv/internal/store"
)

const (
	peerIdentityReadModel              = "peer_identity"
	defaultPeerIdentityCacheMaxEntries = 1_000_000
)

type peerIdentityEntry struct {
	hash               int64
	usernames          []domain.Username
	usernamesLoaded    bool
	verification       domain.CustomVerification
	verificationFound  bool
	verificationLoaded bool
}

// peerIdentityCache keeps both viewer-independent decorations under their one
// durable peer_identity token. A miss fills every configured facet even for a
// narrow update builder, so concurrent callers cannot overwrite one another
// with partial entries. The embedded hash is checked on every lookup; exact invalidation
// therefore never needs a process-wide load epoch that rejects unrelated peers.
type peerIdentityCache struct {
	cache *readmodelcache.Cache[domain.Peer, peerIdentityEntry]
}

func newPeerIdentityCache(max int) *peerIdentityCache {
	if max <= 0 {
		max = defaultPeerIdentityCacheMaxEntries
	}
	return &peerIdentityCache{cache: readmodelcache.New[domain.Peer, peerIdentityEntry](
		readmodelcache.Config[domain.Peer, peerIdentityEntry]{
			MaxEntries: max,
			Clone: func(in peerIdentityEntry) peerIdentityEntry {
				in.usernames = append([]domain.Username(nil), in.usernames...)
				return in
			},
		},
	)}
}

func (c *peerIdentityCache) lookup(peer domain.Peer, hash int64) (peerIdentityEntry, bool) {
	if c == nil || c.cache == nil || hash == 0 {
		return peerIdentityEntry{}, false
	}
	entry, ok := c.cache.Peek(peer)
	if !ok || entry.hash != hash {
		return peerIdentityEntry{}, false
	}
	return entry, true
}

func (c *peerIdentityCache) store(peer domain.Peer, entry peerIdentityEntry) {
	if c == nil || c.cache == nil || peer.ID == 0 || entry.hash == 0 {
		return
	}
	c.cache.Store(peer, entry)
}

func (c *peerIdentityCache) invalidate(peer domain.Peer) {
	if c != nil && c.cache != nil {
		c.cache.Invalidate(peer)
	}
}

func (c *peerIdentityCache) flush() {
	if c != nil && c.cache != nil {
		c.cache.Flush()
	}
}

func (r *Router) peerIdentityHashes(ctx context.Context, peers []domain.Peer) (map[domain.Peer]int64, error) {
	out := make(map[domain.Peer]int64, len(peers))
	if r == nil || r.deps.ReadModelVersions == nil || len(peers) == 0 {
		return out, nil
	}
	keys := make([]store.ReadModelKey, 0, len(peers))
	seen := make(map[domain.Peer]struct{}, len(peers))
	for _, peer := range peers {
		if peer.ID == 0 || (peer.Type != domain.PeerTypeUser && peer.Type != domain.PeerTypeChannel) {
			continue
		}
		if _, ok := seen[peer]; ok {
			continue
		}
		seen[peer] = struct{}{}
		keys = append(keys, store.ReadModelKey{
			Model:    peerIdentityReadModel,
			PeerType: peer.Type,
			PeerID:   peer.ID,
		})
	}
	rows, err := r.deps.ReadModelVersions.ReadModelHashes(ctx, keys)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		out[domain.Peer{Type: key.PeerType, ID: key.PeerID}] = rows[key]
	}
	return out, nil
}

// peerIdentityMaps resolves the requested facets in at most two batched backend
// reads, preserves positive and negative results, and revalidates the durable
// hash before admitting them. Username and verification failures are isolated:
// one optional backend cannot suppress the other decoration.
func (r *Router) peerIdentityMaps(
	ctx context.Context,
	peers []domain.Peer,
	wantUsernames bool,
	wantVerification bool,
) (map[domain.Peer][]domain.Username, map[domain.Peer]domain.CustomVerification) {
	usernames := make(map[domain.Peer][]domain.Username)
	verifications := make(map[domain.Peer]domain.CustomVerification)
	if len(peers) == 0 || (!wantUsernames && !wantVerification) {
		return usernames, verifications
	}
	projectUsernames := wantUsernames && r.deps.Usernames != nil
	projectVerification := wantVerification && r.deps.BotVerifications != nil
	if !projectUsernames && !projectVerification {
		return usernames, verifications
	}
	// A cache miss fills every configured facet, even when a narrow caller only
	// asks for one. This keeps a single immutable value per peer and prevents two
	// concurrent narrow builders from overwriting each other's partial entry.
	loadUsernames := r.deps.Usernames != nil
	loadVerification := r.deps.BotVerifications != nil

	// Without the durable token, load fresh data for this response but never
	// reuse it. A token read failure is fail-closed for the cache as well.
	if r.peerIdentityCache == nil || r.deps.ReadModelVersions == nil {
		if projectUsernames {
			if loaded, err := r.loadUsernameRegistryMap(ctx, peers); err == nil {
				usernames = loaded
			}
		}
		if projectVerification {
			if loaded, err := r.loadBotVerificationMap(ctx, peers); err == nil {
				verifications = loaded
			}
		}
		return usernames, verifications
	}

	hashes, err := r.peerIdentityHashes(ctx, peers)
	if err != nil {
		return usernames, verifications
	}
	entries := make(map[domain.Peer]peerIdentityEntry, len(peers))
	usernameMisses := make([]domain.Peer, 0, len(peers))
	verificationMisses := make([]domain.Peer, 0, len(peers))
	seen := make(map[domain.Peer]struct{}, len(peers))
	for _, peer := range peers {
		if _, ok := seen[peer]; ok || peer.ID == 0 {
			continue
		}
		seen[peer] = struct{}{}
		entry, _ := r.peerIdentityCache.lookup(peer, hashes[peer])
		entry.hash = hashes[peer]
		entries[peer] = entry
		if loadUsernames && !entry.usernamesLoaded {
			usernameMisses = append(usernameMisses, peer)
		}
		if loadVerification && !entry.verificationLoaded {
			verificationMisses = append(verificationMisses, peer)
		}
	}

	if len(usernameMisses) > 0 {
		if loaded, loadErr := r.loadUsernameRegistryMap(ctx, usernameMisses); loadErr == nil {
			for _, peer := range usernameMisses {
				entry := entries[peer]
				entry.usernames = append([]domain.Username(nil), loaded[peer]...)
				entry.usernamesLoaded = true
				entries[peer] = entry
			}
		}
	}
	if len(verificationMisses) > 0 {
		if loaded, loadErr := r.loadBotVerificationMap(ctx, verificationMisses); loadErr == nil {
			for _, peer := range verificationMisses {
				entry := entries[peer]
				entry.verification, entry.verificationFound = loaded[peer]
				entry.verificationLoaded = true
				entries[peer] = entry
			}
		}
	}

	// The version store is itself an exact-key L1 cache, so this revalidation
	// adds no warm PostgreSQL trip. If a concurrent peer_identity mutation was
	// observed, do not admit or project the pre-mutation value in this response.
	currentHashes, err := r.peerIdentityHashes(ctx, peers)
	if err != nil {
		return usernames, verifications
	}
	for peer, entry := range entries {
		if entry.hash == 0 || currentHashes[peer] != entry.hash {
			continue
		}
		complete := (!loadUsernames || entry.usernamesLoaded) && (!loadVerification || entry.verificationLoaded)
		if complete {
			r.peerIdentityCache.store(peer, entry)
		}
		if projectUsernames && entry.usernamesLoaded && len(entry.usernames) > 0 {
			usernames[peer] = append([]domain.Username(nil), entry.usernames...)
		}
		if projectVerification && entry.verificationLoaded && entry.verificationFound {
			verifications[peer] = entry.verification
		}
	}
	return usernames, verifications
}

func (r *Router) InvalidatePeerIdentityReadModel(peer domain.Peer) {
	r.peerIdentityCache.invalidate(peer)
}

func (r *Router) FlushPeerIdentityReadModel() {
	r.peerIdentityCache.flush()
}
