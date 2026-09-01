package userprojection

import (
	"context"
	"fmt"
	"sort"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

var _ store.SparseContactProjectionStore = (*CachedContactStore)(nil)

// ContactProjectionForViewerUserIDs keeps the pair cache useful for sparse
// outbox projection without ever broadening a cold read into viewers x targets.
func (c *CachedContactStore) ContactProjectionForViewerUserIDs(ctx context.Context, requested map[int64][]int64) (domain.ContactProjectionBatch, error) {
	pairs := canonicalContactProjectionPairs(requested)
	if len(pairs) == 0 {
		return emptyContactProjectionBatch(), nil
	}
	for {
		out := emptyContactProjectionBatch()
		readFence := c.captureCacheFence(sparseContactProjectionFenceIDs(pairs)...)
		now := c.now()
		cold := make(map[int64][]int64)
		for _, pair := range pairs {
			contactKnown := false
			if snap, ok := c.lookupContactSnapshot(pair.viewerUserID, now); ok {
				contactKnown = true
				if contact, found := snap.contacts[pair.contactUserID]; found {
					putContactProjectionContact(&out, pair.viewerUserID, pair.contactUserID, contact)
				} else {
					// Personal photos are rows on contacts and cannot exist when the
					// viewer has no contact row for this target.
					continue
				}
			}
			photoKnown := false
			if snap, ok := c.lookupPersonalPhotoSnapshot(pair.viewerUserID, now); ok {
				photoKnown = true
				if ref, found := snap.refs[pair.contactUserID]; found {
					putContactProjectionPersonalPhoto(&out, pair.viewerUserID, pair.contactUserID, ref)
				}
			}
			if contactKnown && photoKnown {
				continue
			}
			if snap, ok := c.lookupContactProjectionPair(pair.viewerUserID, pair.contactUserID, now); ok {
				if !contactKnown && snap.contactFound {
					putContactProjectionContact(&out, pair.viewerUserID, pair.contactUserID, snap.contact)
				}
				if !photoKnown && snap.personalPhotoFound {
					putContactProjectionPersonalPhoto(&out, pair.viewerUserID, pair.contactUserID, snap.personalPhoto)
				}
				continue
			}
			cold[pair.viewerUserID] = append(cold[pair.viewerUserID], pair.contactUserID)
		}
		if !c.cacheFenceCurrent(readFence) {
			if err := ctx.Err(); err != nil {
				return domain.ContactProjectionBatch{}, err
			}
			continue
		}
		if len(cold) == 0 {
			return out, nil
		}
		loaded, err := c.loadSparseContactProjection(ctx, cold)
		if err != nil {
			return domain.ContactProjectionBatch{}, err
		}
		if !c.cacheFenceCurrent(readFence) {
			if err := ctx.Err(); err != nil {
				return domain.ContactProjectionBatch{}, err
			}
			continue
		}
		mergeContactProjectionBatch(&out, loaded)
		return out, nil
	}
}

type sparseContactProjectionPair struct {
	viewerUserID  int64
	contactUserID int64
}

func canonicalContactProjectionPairs(requested map[int64][]int64) []sparseContactProjectionPair {
	seen := make(map[sparseContactProjectionPair]struct{})
	for viewerID, ids := range requested {
		if viewerID == 0 {
			continue
		}
		for _, id := range ids {
			if id != 0 {
				seen[sparseContactProjectionPair{viewerUserID: viewerID, contactUserID: id}] = struct{}{}
			}
		}
	}
	out := make([]sparseContactProjectionPair, 0, len(seen))
	for pair := range seen {
		out = append(out, pair)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].viewerUserID == out[j].viewerUserID {
			return out[i].contactUserID < out[j].contactUserID
		}
		return out[i].viewerUserID < out[j].viewerUserID
	})
	return out
}

func (c *CachedContactStore) loadSparseContactProjection(ctx context.Context, requested map[int64][]int64) (domain.ContactProjectionBatch, error) {
	pairs := canonicalContactProjectionPairs(requested)
	canonical := make(map[int64][]int64)
	for _, pair := range pairs {
		canonical[pair.viewerUserID] = append(canonical[pair.viewerUserID], pair.contactUserID)
	}
	sfKey := fmt.Sprintf("contact-projection-sparse:%v", pairs)
	for {
		v, err, _ := c.sf.Do(sfKey, func() (any, error) {
			loader, ok := c.inner.(store.SparseContactProjectionStore)
			if !ok {
				return contactProjectionLoadResult{}, fmt.Errorf("contact store does not support sparse projection")
			}
			loadFence := c.captureCacheFence(sparseContactProjectionFenceIDs(pairs)...)
			batch, err := loader.ContactProjectionForViewerUserIDs(ctx, canonical)
			if err != nil {
				return contactProjectionLoadResult{}, err
			}
			expireAt := c.now().Add(c.ttl)
			admitPairs := len(pairs) <= contactProjectionDenseAdmissionMaxCells
			c.mu.Lock()
			current := c.cacheFenceCurrentLocked(loadFence)
			if current && admitPairs {
				for _, pair := range pairs {
					contact, contactFound := batch.Contacts[pair.viewerUserID][pair.contactUserID]
					ref, photoFound := batch.PersonalPhotos[pair.viewerUserID][pair.contactUserID]
					c.storeContactProjectionPairLocked(
						contactProjectionKey{viewerUserID: pair.viewerUserID, contactUserID: pair.contactUserID},
						contact, contactFound, ref, photoFound, expireAt,
					)
				}
			}
			c.mu.Unlock()
			return contactProjectionLoadResult{batch: cloneContactProjectionBatch(batch), current: current}, nil
		})
		if err != nil {
			return domain.ContactProjectionBatch{}, err
		}
		result := v.(contactProjectionLoadResult)
		if result.current {
			return result.batch, nil
		}
		if err := ctx.Err(); err != nil {
			return domain.ContactProjectionBatch{}, err
		}
	}
}

func sparseContactProjectionFenceIDs(pairs []sparseContactProjectionPair) []int64 {
	ids := make([]int64, 0, len(pairs)*2)
	for _, pair := range pairs {
		ids = append(ids, pair.viewerUserID, pair.contactUserID)
	}
	return ids
}

func emptyContactProjectionBatch() domain.ContactProjectionBatch {
	return domain.ContactProjectionBatch{
		Contacts:       map[int64]map[int64]domain.Contact{},
		PersonalPhotos: map[int64]map[int64]domain.ProfilePhotoRef{},
	}
}
