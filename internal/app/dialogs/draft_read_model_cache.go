package dialogs

import (
	"context"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/readmodelcache"
)

const (
	defaultDialogDraftReadModelTTL              = 24 * time.Hour
	defaultDialogDraftReadModelMaxEntries       = 1000000
	defaultDialogDraftReadModelMaxBytes   int64 = 256 << 20
)

type dialogDraftCacheEntry struct {
	draft domain.DialogDraft
	found bool
}

type dialogDraftReadModelCache struct {
	cache *readmodelcache.Cache[dialogPeerCacheKey, dialogDraftCacheEntry]
}

func newDialogDraftReadModelCache(maxEntries int, maxBytes int64, ttl time.Duration) *dialogDraftReadModelCache {
	if ttl <= 0 {
		ttl = defaultDialogDraftReadModelTTL
	}
	return &dialogDraftReadModelCache{cache: readmodelcache.New[dialogPeerCacheKey, dialogDraftCacheEntry](readmodelcache.Config[dialogPeerCacheKey, dialogDraftCacheEntry]{
		MaxEntries: maxEntries,
		MaxWeight:  maxBytes,
		Weight:     dialogDraftEntryApproxBytes,
		TTL:        ttl,
		Clone:      cloneDialogDraftCacheEntry,
	})}
}

func (s *Service) dialogDraftsReadModel(ctx context.Context, userID int64, peers []domain.Peer) (map[domain.Peer]dialogDraftCacheEntry, error) {
	out := make(map[domain.Peer]dialogDraftCacheEntry, len(peers))
	if s == nil || s.dialogs == nil || userID == 0 || len(peers) == 0 {
		return out, nil
	}
	unique := uniqueDialogPeers(peers)
	if len(unique) == 0 {
		return out, nil
	}
	keys := make([]dialogPeerCacheKey, 0, len(unique))
	for _, peer := range unique {
		keys = append(keys, dialogPeerCacheKey{userID: userID, peer: peer})
	}
	hashes := map[domain.Peer]int64{}
	if s.versions != nil {
		var err error
		hashes, err = s.dialogHashes(ctx, userID, unique)
		if err != nil {
			return nil, err
		}
	}
	var cache *readmodelcache.Cache[dialogPeerCacheKey, dialogDraftCacheEntry]
	if s.draftCache != nil {
		cache = s.draftCache.cache
	}
	loaded, err := cache.GetOrLoadBatch(ctx, keys,
		func(key dialogPeerCacheKey) (int64, bool) {
			hash := hashes[key.peer]
			return hash, s.versions != nil && hash != 0
		},
		func(ctx context.Context, missing []dialogPeerCacheKey) (map[dialogPeerCacheKey]dialogDraftCacheEntry, error) {
			requested := make([]domain.Peer, 0, len(missing))
			for _, key := range missing {
				requested = append(requested, key.peer)
			}
			drafts, err := s.dialogs.ListDraftsByPeers(ctx, userID, requested)
			if err != nil {
				return nil, err
			}
			entries := make(map[dialogPeerCacheKey]dialogDraftCacheEntry, len(missing))
			for _, key := range missing {
				entries[key] = dialogDraftCacheEntry{}
			}
			for _, draft := range drafts {
				if draft.TopMessageID != 0 {
					continue
				}
				key := dialogPeerCacheKey{userID: userID, peer: draft.Peer}
				if _, ok := entries[key]; ok {
					entries[key] = dialogDraftCacheEntry{draft: cloneDraft(draft), found: true}
				}
			}
			return entries, nil
		})
	if err != nil {
		return nil, err
	}
	for key, entry := range loaded {
		out[key.peer] = entry
	}
	return out, nil
}

func uniqueDialogPeers(peers []domain.Peer) []domain.Peer {
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

func (c *dialogDraftReadModelCache) invalidate(key dialogPeerCacheKey) {
	if c != nil {
		c.cache.Invalidate(key)
	}
}

func (c *dialogDraftReadModelCache) flush() {
	if c != nil {
		c.cache.Flush()
	}
}

func cloneDialogDraftCacheEntry(entry dialogDraftCacheEntry) dialogDraftCacheEntry {
	if entry.found {
		entry.draft = cloneDraft(entry.draft)
	}
	return entry
}

func dialogDraftEntryApproxBytes(entry dialogDraftCacheEntry) int64 {
	if !entry.found {
		return 64
	}
	draft := entry.draft
	weight := int64(256 + len(draft.Message) + len(draft.Entities)*64)
	if draft.ReplyTo != nil {
		weight += int64(128 + len(draft.ReplyTo.QuoteText) + len(draft.ReplyTo.QuoteEntities)*64)
	}
	if draft.WebPage != nil {
		weight += int64(64 + len(draft.WebPage.URL))
	}
	if draft.RichMessage != nil {
		weight += int64(len(draft.RichMessage.Blocks) + len(draft.RichMessage.BotAPIProjection) + len(draft.RichMessage.Photos)*256 + len(draft.RichMessage.Documents)*256)
	}
	return weight
}
