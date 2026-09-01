package userprojection

import (
	"context"

	"telesrv/internal/app/readmodel"
	"telesrv/internal/domain"
	"telesrv/internal/readmodelcache"
	"telesrv/internal/store"
)

type accountFreezeFact struct {
	value domain.AccountFreeze
	found bool
}

// DurableUserProjectionFacts caches only viewer-independent durable overlays.
// Contact/privacy/presence decisions remain outside and are evaluated after
// these facts are loaded.
type DurableUserProjectionFacts struct {
	freezes  AccountFreezeProvider
	versions store.ReadModelVersionStore

	freezeCache *readmodelcache.Cache[int64, accountFreezeFact]
}

func NewDurableUserProjectionFacts(
	freezes AccountFreezeProvider,
	versions store.ReadModelVersionStore,
	maxEntries int,
) *DurableUserProjectionFacts {
	return &DurableUserProjectionFacts{
		freezes:  freezes,
		versions: versions,
		freezeCache: readmodelcache.New[int64, accountFreezeFact](readmodelcache.Config[int64, accountFreezeFact]{
			MaxEntries: maxEntries,
		}),
	}
}

func (f *DurableUserProjectionFacts) AccountFreezes(ctx context.Context, userIDs []int64) (map[int64]domain.AccountFreeze, error) {
	out := make(map[int64]domain.AccountFreeze)
	ids := uniqueDurableFactUserIDs(userIDs)
	if f == nil || f.freezes == nil || len(ids) == 0 {
		return out, nil
	}
	hashes, err := f.factHashes(ctx, readmodel.ModelUserVisibility, ids)
	if err != nil {
		return nil, err
	}
	loaded, err := f.freezeCache.GetOrLoadBatch(ctx, ids,
		func(userID int64) (int64, bool) {
			hash := hashes[userID]
			return hash, f.versions != nil && hash != 0
		},
		func(ctx context.Context, missing []int64) (map[int64]accountFreezeFact, error) {
			values, err := f.freezes.AccountFreezes(ctx, missing)
			if err != nil {
				return nil, err
			}
			entries := make(map[int64]accountFreezeFact, len(missing))
			for _, userID := range missing {
				entry := accountFreezeFact{}
				if value, ok := values[userID]; ok {
					entry = accountFreezeFact{value: value, found: true}
				}
				entries[userID] = entry
			}
			return entries, nil
		})
	if err != nil {
		return nil, err
	}
	for userID, entry := range loaded {
		if entry.found {
			out[userID] = entry.value
		}
	}
	return out, nil
}

// AccountFreeze exposes the same versioned positive/negative cache to scalar
// RPC gates. It deliberately delegates to the batch path so gate reads and
// user/dialog projection cannot drift into separate cache semantics.
func (f *DurableUserProjectionFacts) AccountFreeze(ctx context.Context, userID int64) (domain.AccountFreeze, bool, error) {
	if userID == 0 {
		return domain.AccountFreeze{}, false, nil
	}
	items, err := f.AccountFreezes(ctx, []int64{userID})
	if err != nil {
		return domain.AccountFreeze{}, false, err
	}
	value, found := items[userID]
	return value, found, nil
}

func (f *DurableUserProjectionFacts) factHashes(ctx context.Context, model string, userIDs []int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(userIDs))
	if f == nil || f.versions == nil {
		return out, nil
	}
	keys := make([]store.ReadModelKey, 0, len(userIDs))
	for _, userID := range userIDs {
		keys = append(keys, store.ReadModelKey{Model: model, OwnerUserID: 0, PeerType: domain.PeerTypeUser, PeerID: userID})
	}
	rows, err := f.versions.ReadModelHashes(ctx, keys)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		out[key.PeerID] = rows[key]
	}
	return out, nil
}

func (f *DurableUserProjectionFacts) InvalidateAccountFreezeFact(userID int64) {
	if f != nil && userID != 0 {
		f.freezeCache.Invalidate(userID)
	}
}

func (f *DurableUserProjectionFacts) FlushUserProjectionFactReadModel() {
	if f != nil {
		f.freezeCache.Flush()
	}
}

func uniqueDurableFactUserIDs(ids []int64) []int64 {
	out := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
