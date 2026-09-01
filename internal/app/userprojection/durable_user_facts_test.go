package userprojection

import (
	"context"
	"errors"
	"sync"
	"testing"

	"telesrv/internal/app/readmodel"
	"telesrv/internal/domain"
	"telesrv/internal/store"
)

type durableFactVersions struct {
	mu     sync.Mutex
	hashes map[store.ReadModelKey]int64
}

func (v *durableFactVersions) ReadModelHash(_ context.Context, model string, ownerUserID int64, peerType domain.PeerType, peerID int64) (int64, bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	hash := v.hashes[store.ReadModelKey{Model: model, OwnerUserID: ownerUserID, PeerType: peerType, PeerID: peerID}]
	return hash, hash != 0, nil
}

func (v *durableFactVersions) ReadModelHashes(_ context.Context, keys []store.ReadModelKey) (map[store.ReadModelKey]int64, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make(map[store.ReadModelKey]int64, len(keys))
	for _, key := range keys {
		if hash := v.hashes[key]; hash != 0 {
			out[key] = hash
		}
	}
	return out, nil
}

func (v *durableFactVersions) set(model string, userID, hash int64) {
	v.mu.Lock()
	v.hashes[store.ReadModelKey{Model: model, OwnerUserID: 0, PeerType: domain.PeerTypeUser, PeerID: userID}] = hash
	v.mu.Unlock()
}

type countingDurableFreezeFacts struct {
	mu     sync.Mutex
	calls  int
	values map[int64]domain.AccountFreeze
	err    error
}

func (f *countingDurableFreezeFacts) AccountFreezes(_ context.Context, ids []int64) (map[int64]domain.AccountFreeze, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[int64]domain.AccountFreeze)
	for _, id := range ids {
		if value, ok := f.values[id]; ok {
			out[id] = value
		}
	}
	return out, nil
}

func TestDurableUserProjectionFactsCachesPositiveAndNegativeByVersion(t *testing.T) {
	ctx := context.Background()
	versions := &durableFactVersions{hashes: make(map[store.ReadModelKey]int64)}
	for _, id := range []int64{1, 2} {
		versions.set(readmodel.ModelUserVisibility, id, 10+id)
	}
	freezes := &countingDurableFreezeFacts{values: map[int64]domain.AccountFreeze{1: {UserID: 1, Frozen: true}}}
	facts := NewDurableUserProjectionFacts(freezes, versions, 10)

	for i := 0; i < 2; i++ {
		gotFreezes, err := facts.AccountFreezes(ctx, []int64{1, 2, 1})
		if err != nil || len(gotFreezes) != 1 || !gotFreezes[1].Frozen {
			t.Fatalf("AccountFreezes(%d) = %+v err=%v", i, gotFreezes, err)
		}
	}
	if freezes.calls != 1 {
		t.Fatalf("backend calls freezes = %d, want 1 including negative hits", freezes.calls)
	}

	versions.set(readmodel.ModelUserVisibility, 2, 32)
	freezes.values[2] = domain.AccountFreeze{UserID: 2, Frozen: true}
	gotFreezes, err := facts.AccountFreezes(ctx, []int64{1, 2})
	if err != nil || !gotFreezes[2].Frozen {
		t.Fatalf("AccountFreezes after version bump = %+v err=%v", gotFreezes, err)
	}
	if freezes.calls != 2 {
		t.Fatalf("backend calls after one-key bumps freezes = %d, want 2", freezes.calls)
	}
}

func TestDurableUserProjectionFactsScalarFreezeGateReusesVersionedFact(t *testing.T) {
	ctx := context.Background()
	versions := &durableFactVersions{hashes: make(map[store.ReadModelKey]int64)}
	versions.set(readmodel.ModelUserVisibility, 1, 11)
	versions.set(readmodel.ModelUserVisibility, 2, 12)
	freezes := &countingDurableFreezeFacts{values: map[int64]domain.AccountFreeze{
		1: {UserID: 1, Frozen: true},
	}}
	facts := NewDurableUserProjectionFacts(freezes, versions, 10)

	for i := 0; i < 3; i++ {
		freeze, found, err := facts.AccountFreeze(ctx, 1)
		if err != nil || !found || !freeze.Frozen {
			t.Fatalf("positive scalar gate %d = %+v found=%v err=%v", i, freeze, found, err)
		}
		freeze, found, err = facts.AccountFreeze(ctx, 2)
		if err != nil || found || freeze.Frozen {
			t.Fatalf("negative scalar gate %d = %+v found=%v err=%v", i, freeze, found, err)
		}
	}
	if freezes.calls != 2 {
		t.Fatalf("backend calls = %d, want one positive and one negative fill", freezes.calls)
	}
}

func TestDurableUserProjectionFactErrorsAreNotNegativeCached(t *testing.T) {
	ctx := context.Background()
	versions := &durableFactVersions{hashes: make(map[store.ReadModelKey]int64)}
	versions.set(readmodel.ModelUserVisibility, 1, 11)
	freezes := &countingDurableFreezeFacts{values: map[int64]domain.AccountFreeze{}, err: errors.New("freeze unavailable")}
	facts := NewDurableUserProjectionFacts(freezes, versions, 10)

	if _, err := facts.AccountFreezes(ctx, []int64{1}); err == nil {
		t.Fatal("AccountFreezes error = nil")
	}
	freezes.err = nil
	if _, err := facts.AccountFreezes(ctx, []int64{1}); err != nil {
		t.Fatalf("recovered AccountFreezes: %v", err)
	}
	if freezes.calls != 2 {
		t.Fatalf("backend calls freezes = %d, want retry", freezes.calls)
	}
}

func TestDurableUserProjectionFactExplicitInvalidation(t *testing.T) {
	ctx := context.Background()
	versions := &durableFactVersions{hashes: make(map[store.ReadModelKey]int64)}
	versions.set(readmodel.ModelUserVisibility, 1, 11)
	freezes := &countingDurableFreezeFacts{values: map[int64]domain.AccountFreeze{}}
	facts := NewDurableUserProjectionFacts(freezes, versions, 10)
	_, _ = facts.AccountFreezes(ctx, []int64{1})
	facts.InvalidateAccountFreezeFact(1)
	_, _ = facts.AccountFreezes(ctx, []int64{1})
	if freezes.calls != 2 {
		t.Fatalf("backend calls after invalidation freezes = %d, want 2", freezes.calls)
	}
}
