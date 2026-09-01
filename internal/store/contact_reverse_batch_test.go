package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
	"telesrv/internal/store/memory"
)

type recordingSparseReverseStore struct {
	store.ContactStore

	mu        sync.Mutex
	calls     int
	pairCount int
}

func (s *recordingSparseReverseStore) GetReverseContactsForViewerUserIDs(
	ctx context.Context,
	requested map[int64][]int64,
) (map[int64]map[int64]domain.Contact, error) {
	out := make(map[int64]map[int64]domain.Contact, len(requested))
	pairs := 0
	for ownerID, viewerIDs := range requested {
		for _, viewerID := range viewerIDs {
			pairs++
			contact, found, err := s.ContactStore.Get(ctx, ownerID, viewerID)
			if err != nil {
				return nil, err
			}
			if found {
				if out[ownerID] == nil {
					out[ownerID] = make(map[int64]domain.Contact)
				}
				out[ownerID][viewerID] = contact
			}
		}
	}
	s.mu.Lock()
	s.calls++
	s.pairCount += pairs
	s.mu.Unlock()
	return out, nil
}

func (s *recordingSparseReverseStore) stats() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.pairCount
}

func TestBatchedReverseContactStoreCombinesExactPairs(t *testing.T) {
	ctx := context.Background()
	base := memory.NewContactStore()
	const requestCount = 32
	for index := 0; index < requestCount; index++ {
		viewerID := int64(10_000 + index)
		ownerID := int64(20_000 + index)
		if _, err := base.Upsert(ctx, ownerID, domain.ContactInput{
			ContactUserID: viewerID,
			FirstName:     "viewer",
		}); err != nil {
			t.Fatal(err)
		}
		if index%2 == 0 {
			if _, err := base.SetCloseFriends(ctx, ownerID, []int64{viewerID}); err != nil {
				t.Fatal(err)
			}
		}
	}
	recording := &recordingSparseReverseStore{ContactStore: base}
	batched, err := store.NewBatchedReverseContactStore(recording, store.ReverseContactBatchConfig{
		MaxPairs: 128, MaxWait: 10 * time.Millisecond, QueueSize: 64, QueryTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(batched.Close)

	start := make(chan struct{})
	errs := make(chan error, requestCount)
	var wg sync.WaitGroup
	for index := 0; index < requestCount; index++ {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			viewerID := int64(10_000 + index)
			ownerID := int64(20_000 + index)
			contacts, getErr := batched.GetReverseContacts(ctx, viewerID, []int64{ownerID, 99_999, ownerID})
			if getErr != nil {
				errs <- getErr
				return
			}
			contact, found := contacts[ownerID]
			if !found || contact.User.ID != viewerID || contact.CloseFriend != (index%2 == 0) {
				errs <- errors.New("batched reverse-contact result mismatch")
			}
			if _, found := contacts[99_999]; found {
				errs <- errors.New("negative reverse-contact pair returned a value")
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	calls, pairs := recording.stats()
	if calls <= 0 || calls > 4 {
		t.Fatalf("sparse reverse calls = %d, want 1..4 for %d concurrent requests", calls, requestCount)
	}
	// Every request contributes exactly one positive and one negative pair;
	// duplicate owner ids must be canonicalized before queue admission.
	if pairs != requestCount*2 {
		t.Fatalf("queried pairs = %d, want %d exact pairs", pairs, requestCount*2)
	}

	batched.Close()
	if _, err := batched.GetReverseContacts(ctx, 10_000, []int64{20_000}); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetReverseContacts after close err = %v", err)
	}
}

func TestNewBatchedReverseContactStoreRejectsInvalidConfig(t *testing.T) {
	base := &recordingSparseReverseStore{ContactStore: memory.NewContactStore()}
	for _, cfg := range []store.ReverseContactBatchConfig{
		{},
		{MaxPairs: 1, MaxWait: 11 * time.Millisecond, QueueSize: 1, QueryTimeout: time.Second},
		{MaxPairs: 1, MaxWait: time.Microsecond, QueueSize: 0, QueryTimeout: time.Second},
		{MaxPairs: 1, MaxWait: time.Microsecond, QueueSize: 1, QueryTimeout: 31 * time.Second},
	} {
		batcher, err := store.NewBatchedReverseContactStore(base, cfg)
		if err == nil {
			batcher.Close()
			t.Fatalf("invalid config accepted: %+v", cfg)
		}
	}
}
