package userprojection

import (
	"context"
	"reflect"
	"testing"

	privacyapp "telesrv/internal/app/privacy"
	"telesrv/internal/domain"
	"telesrv/internal/store"
	"telesrv/internal/store/memory"
)

type recordingSparseContactStore struct {
	store.ContactStore
	sparseCalls int
	denseCalls  int
	requested   map[int64][]int64
}

func (s *recordingSparseContactStore) ContactProjectionForViewers(ctx context.Context, viewers, owners []int64) (domain.ContactProjectionBatch, error) {
	s.denseCalls++
	return s.ContactStore.ContactProjectionForViewers(ctx, viewers, owners)
}

func (s *recordingSparseContactStore) ContactProjectionForViewerUserIDs(ctx context.Context, requested map[int64][]int64) (domain.ContactProjectionBatch, error) {
	s.sparseCalls++
	s.requested = make(map[int64][]int64, len(requested))
	for viewerID, ids := range requested {
		s.requested[viewerID] = append([]int64(nil), ids...)
	}
	return s.ContactStore.(store.SparseContactProjectionStore).ContactProjectionForViewerUserIDs(ctx, requested)
}

func TestForViewerUserIDsUsesActualPairsAndMatchesScalarProjection(t *testing.T) {
	ctx := context.Background()
	const (
		viewerA = int64(1101)
		viewerB = int64(1102)
		ownerA  = int64(2101)
		ownerB  = int64(2102)
	)
	contacts := memory.NewContactStore()
	// Seed both requested and cross-viewer rows. A dense matrix would expose the
	// cross aliases/photos; the sparse request must never ask for those pairs.
	for _, input := range []struct {
		viewer int64
		owner  int64
		name   string
		photo  int64
	}{
		{viewerA, ownerA, "A for viewer A", 9101},
		{viewerA, ownerB, "B cross leak", 9191},
		{viewerB, ownerB, "B for viewer B", 9102},
		{viewerB, ownerA, "A cross leak", 9192},
		// Reverse rows are the privacy ViewerIsContact facts.
		{ownerA, viewerA, "viewer A", 0},
		{ownerB, viewerB, "viewer B", 0},
	} {
		if _, err := contacts.Upsert(ctx, input.viewer, domain.ContactInput{ContactUserID: input.owner, FirstName: input.name}); err != nil {
			t.Fatalf("upsert %d->%d: %v", input.viewer, input.owner, err)
		}
		if input.photo != 0 {
			if _, _, err := contacts.SetPersonalPhoto(ctx, input.viewer, input.owner, input.photo, 100); err != nil {
				t.Fatalf("personal photo %d->%d: %v", input.viewer, input.owner, err)
			}
		}
	}
	recording := &recordingSparseContactStore{ContactStore: contacts}
	privacy := privacyapp.NewService(memory.NewPrivacyStore(), recording)
	projector := New(WithContactStore(recording), WithPrivacyEvaluator(privacy))
	base := []domain.User{
		{ID: ownerA, AccessHash: 31, Phone: "15552101", FirstName: "Owner A"},
		{ID: ownerB, AccessHash: 32, Phone: "15552102", FirstName: "Owner B"},
	}
	wantA, err := projector.ForViewer(ctx, viewerA, base[:1])
	if err != nil {
		t.Fatalf("scalar viewer A: %v", err)
	}
	wantB, err := projector.ForViewer(ctx, viewerB, base[1:])
	if err != nil {
		t.Fatalf("scalar viewer B: %v", err)
	}
	recording.sparseCalls = 0
	recording.denseCalls = 0
	recording.requested = nil

	got, err := projector.ForViewerUserIDs(ctx, map[int64][]int64{
		viewerA: {ownerA},
		viewerB: {ownerB},
	}, base)
	if err != nil {
		t.Fatalf("ForViewerUserIDs: %v", err)
	}
	if !reflect.DeepEqual(got[viewerA], wantA) || !reflect.DeepEqual(got[viewerB], wantB) {
		t.Fatalf("sparse projection = %+v, want scalar A=%+v B=%+v", got, wantA, wantB)
	}
	if recording.sparseCalls != 1 || recording.denseCalls != 0 {
		t.Fatalf("contact projection calls = sparse %d dense %d, want 1/0", recording.sparseCalls, recording.denseCalls)
	}
	wantPairs := map[int64][]int64{
		viewerA: {ownerA},
		viewerB: {ownerB},
		ownerA:  {viewerA},
		ownerB:  {viewerB},
	}
	for viewerID, ids := range wantPairs {
		if !reflect.DeepEqual(recording.requested[viewerID], ids) {
			t.Fatalf("requested[%d] = %v, want %v (all=%+v)", viewerID, recording.requested[viewerID], ids, recording.requested)
		}
	}
	if len(recording.requested) != len(wantPairs) {
		t.Fatalf("requested pairs = %+v, contains unexpected cross-viewer edges", recording.requested)
	}
}
