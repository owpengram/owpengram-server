package files

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// fakeManualPurgeStore is a mediaRetentionStore fake that mimics the SQL
// nullable-cutoff contract in Go: a nil cutoff matches every candidate
// regardless of its createdAt, a non-nil cutoff only matches candidates
// strictly older than it -- same semantics as the real
// ListDocumentIDsForHardRetentionOlderThan/ListPhotoIDsForHardRetentionOlderThan
// queries (see internal/store/postgres/queries/media.sql). Embeds a nil
// store.MediaStore so it satisfies that (large) interface for free, same
// trick as fakeCategorySweepStore in retention_category_zero_test.go.
type fakeManualPurgeStore struct {
	store.MediaStore

	docsByCategory map[domain.MediaCategory][]int64
	docCreatedAt   map[int64]time.Time
	photos         []int64
	photoCreatedAt map[int64]time.Time
	avatarPhotos   []int64
	avatarCreated  map[int64]time.Time

	deletedDocs   []int64
	deletedPhotos []int64
}

func matchCutoff(createdAt time.Time, cutoff *time.Time) bool {
	if cutoff == nil {
		return true
	}
	return createdAt.Before(*cutoff)
}

func (f *fakeManualPurgeStore) ListDocumentIDsForHardRetentionOlderThan(_ context.Context, category domain.MediaCategory, cutoff *time.Time, limit int) ([]int64, error) {
	var out []int64
	for _, id := range f.docsByCategory[category] {
		if !matchCutoff(f.docCreatedAt[id], cutoff) {
			continue
		}
		out = append(out, id)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeManualPurgeStore) CountDocumentsForHardRetention(ctx context.Context, category domain.MediaCategory, cutoff *time.Time) (int, error) {
	ids, err := f.ListDocumentIDsForHardRetentionOlderThan(ctx, category, cutoff, len(f.docsByCategory[category])+1)
	return len(ids), err
}

func (f *fakeManualPurgeStore) ListPhotoIDsForHardRetentionOlderThan(_ context.Context, cutoff *time.Time, limit int) ([]int64, error) {
	var out []int64
	for _, id := range f.photos {
		if !matchCutoff(f.photoCreatedAt[id], cutoff) {
			continue
		}
		out = append(out, id)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeManualPurgeStore) CountPhotosForHardRetention(ctx context.Context, cutoff *time.Time) (int, error) {
	ids, err := f.ListPhotoIDsForHardRetentionOlderThan(ctx, cutoff, len(f.photos)+1)
	return len(ids), err
}

func (f *fakeManualPurgeStore) ListAvatarPhotoIDsForHardRetentionOlderThan(_ context.Context, cutoff *time.Time, limit int) ([]int64, error) {
	var out []int64
	for _, id := range f.avatarPhotos {
		if !matchCutoff(f.avatarCreated[id], cutoff) {
			continue
		}
		out = append(out, id)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeManualPurgeStore) CountAvatarPhotosForHardRetention(ctx context.Context, cutoff *time.Time) (int, error) {
	ids, err := f.ListAvatarPhotoIDsForHardRetentionOlderThan(ctx, cutoff, len(f.avatarPhotos)+1)
	return len(ids), err
}

func (f *fakeManualPurgeStore) DeleteFileBlobsForDocument(_ context.Context, id int64) ([]domain.FileBlob, error) {
	f.deletedDocs = append(f.deletedDocs, id)
	return []domain.FileBlob{{
		LocationKey: fmt.Sprintf("doc:%d", id), Backend: domain.MediaBackendLocalFS,
		ObjectKey: fmt.Sprintf("obj-doc-%d", id), Size: 100,
	}}, nil
}

func (f *fakeManualPurgeStore) DeleteFileBlobsForPhoto(_ context.Context, id int64) ([]domain.FileBlob, error) {
	f.deletedPhotos = append(f.deletedPhotos, id)
	return []domain.FileBlob{{
		LocationKey: fmt.Sprintf("photo:%d", id), Backend: domain.MediaBackendLocalFS,
		ObjectKey: fmt.Sprintf("obj-photo-%d", id), Size: 50,
	}}, nil
}

// The rest of mediaRetentionStore isn't exercised by ManualPurge/
// CountManualPurgeCandidates -- stub it out, same as
// fakeCategorySweepStore.
func (f *fakeManualPurgeStore) ListOrphanedDocumentIDsOlderThan(context.Context, domain.MediaCategory, time.Time, int) ([]int64, error) {
	return nil, nil
}
func (f *fakeManualPurgeStore) ListOrphanedPhotoIDsOlderThan(context.Context, time.Time, int) ([]int64, error) {
	return nil, nil
}
func (f *fakeManualPurgeStore) ListAvatarOrphanedPhotoIDsOlderThan(context.Context, time.Time, int) ([]int64, error) {
	return nil, nil
}

// CountFileBlobRefs deliberately returns 1 (not 0): ManualPurge's deleted
// blobs must still be accounted for (deleteOrphanedBlobs's cache eviction),
// but this fake has no real BlobBackend wired up, so it reports the blob as
// still referenced elsewhere -- skipping the physical backend.Delete call
// that would otherwise need a working s.blobs.
func (f *fakeManualPurgeStore) CountFileBlobRefs(context.Context, string, string) (int, error) {
	return 1, nil
}
func (f *fakeManualPurgeStore) DeleteDocumentAndBlobs(context.Context, int64) ([]domain.FileBlob, error) {
	return nil, nil
}
func (f *fakeManualPurgeStore) DeletePhotoAndBlobs(context.Context, int64) ([]domain.FileBlob, error) {
	return nil, nil
}
func (f *fakeManualPurgeStore) OrphanDocumentIfUnreferenced(context.Context, int64) (bool, error) {
	return false, nil
}
func (f *fakeManualPurgeStore) SumFileBlobBytes(context.Context) (int64, error) { return 0, nil }
func (f *fakeManualPurgeStore) ListOldestMediaForEviction(context.Context, int) ([]domain.EvictionCandidate, error) {
	return nil, nil
}
func (f *fakeManualPurgeStore) ListMediaReferences(context.Context, domain.MediaKind, int64) ([]domain.MediaReference, error) {
	return nil, nil
}

func newManualPurgeTestService(fake *fakeManualPurgeStore) *Service {
	return &Service{
		media:     fake,
		log:       zap.NewNop(),
		blobCache: newBlobMetaCache(64),
		byteCache: newBlobBytesCache(1 << 20),
	}
}

// TestManualPurgeOnlyTouchesSelectedCategory guards ManualPurge's per-category
// scoping: purging just "Video" must never touch a Music document sitting in
// the same fake store, mirroring the real query's category filter (see
// TestHardRetentionCategoryFilterOnlyReturnsMatchingCategory in
// internal/store/postgres).
func TestManualPurgeOnlyTouchesSelectedCategory(t *testing.T) {
	fake := &fakeManualPurgeStore{
		docsByCategory: map[domain.MediaCategory][]int64{
			domain.MediaCategoryVideo: {1},
			domain.MediaCategoryMusic: {2},
		},
		docCreatedAt: map[int64]time.Time{
			1: time.Now().Add(-100 * 24 * time.Hour),
			2: time.Now().Add(-100 * 24 * time.Hour),
		},
	}
	s := newManualPurgeTestService(fake)

	purgedDocs, purgedPhotos, bytes, err := s.ManualPurge(context.Background(),
		[]domain.MediaCategory{domain.MediaCategoryVideo}, false, nil, 100)
	if err != nil {
		t.Fatalf("ManualPurge: %v", err)
	}
	if purgedDocs != 1 || purgedPhotos != 0 {
		t.Fatalf("purgedDocs=%d purgedPhotos=%d, want 1/0", purgedDocs, purgedPhotos)
	}
	if bytes != 100 {
		t.Fatalf("bytesReclaimed = %d, want 100", bytes)
	}
	if len(fake.deletedDocs) != 1 || fake.deletedDocs[0] != 1 {
		t.Fatalf("deletedDocs = %v, want only [1] (the Video document)", fake.deletedDocs)
	}
}

// TestManualPurgeNilBeforeIgnoresAge is the unit-level counterpart of the
// Postgres integration test TestManualPurgeNilCutoffIgnoresAge: a document
// created "just now" must still be a purge candidate when before is nil,
// unlike the automatic sweep which always filters by a configured age.
func TestManualPurgeNilBeforeIgnoresAge(t *testing.T) {
	fake := &fakeManualPurgeStore{
		docsByCategory: map[domain.MediaCategory][]int64{
			domain.MediaCategoryFile: {42},
		},
		docCreatedAt: map[int64]time.Time{
			42: time.Now(), // created just now
		},
	}
	s := newManualPurgeTestService(fake)

	// Dry-run: nil before must count the fresh document.
	docs, photos, err := s.CountManualPurgeCandidates(context.Background(),
		[]domain.MediaCategory{domain.MediaCategoryFile}, false, nil)
	if err != nil {
		t.Fatalf("CountManualPurgeCandidates(nil before): %v", err)
	}
	if docs != 1 || photos != 0 {
		t.Fatalf("CountManualPurgeCandidates(nil before) = (%d, %d), want (1, 0)", docs, photos)
	}

	// A real cutoff strictly before "now" must exclude the fresh document.
	past := time.Now().Add(-time.Hour)
	docs, photos, err = s.CountManualPurgeCandidates(context.Background(),
		[]domain.MediaCategory{domain.MediaCategoryFile}, false, &past)
	if err != nil {
		t.Fatalf("CountManualPurgeCandidates(past before): %v", err)
	}
	if docs != 0 || photos != 0 {
		t.Fatalf("CountManualPurgeCandidates(past before) = (%d, %d), want (0, 0)", docs, photos)
	}

	// Confirm: nil before must actually purge the fresh document.
	purgedDocs, _, _, err := s.ManualPurge(context.Background(),
		[]domain.MediaCategory{domain.MediaCategoryFile}, false, nil, 100)
	if err != nil {
		t.Fatalf("ManualPurge(nil before): %v", err)
	}
	if purgedDocs != 1 {
		t.Fatalf("ManualPurge(nil before) purgedDocs = %d, want 1", purgedDocs)
	}
}

// TestManualPurgePhotoCategoryUsesPhotoQuery guards that selecting the
// "Photo" category (unlike the other categories, which are documents.category
// buckets) routes through the dedicated photo query rather than being
// silently dropped or misrouted to the document path.
func TestManualPurgePhotoCategoryUsesPhotoQuery(t *testing.T) {
	fake := &fakeManualPurgeStore{
		photos:         []int64{7},
		photoCreatedAt: map[int64]time.Time{7: time.Now()},
	}
	s := newManualPurgeTestService(fake)

	purgedDocs, purgedPhotos, _, err := s.ManualPurge(context.Background(),
		[]domain.MediaCategory{domain.MediaCategoryPhoto}, false, nil, 100)
	if err != nil {
		t.Fatalf("ManualPurge: %v", err)
	}
	if purgedDocs != 0 || purgedPhotos != 1 {
		t.Fatalf("purgedDocs=%d purgedPhotos=%d, want 0/1", purgedDocs, purgedPhotos)
	}
	if len(fake.deletedPhotos) != 1 || fake.deletedPhotos[0] != 7 {
		t.Fatalf("deletedPhotos = %v, want only [7]", fake.deletedPhotos)
	}
}

// TestManualPurgeIncludeAvatarsIsIndependentOfCategories guards that
// includeAvatars purges avatar photos even when Photo is not among the
// selected categories (and vice versa: selecting Photo alone must not also
// sweep avatars) -- the same split the automatic sweep already has between
// categoryRetentionAge(Photo) and avatarRetentionAge().
func TestManualPurgeIncludeAvatarsIsIndependentOfCategories(t *testing.T) {
	fake := &fakeManualPurgeStore{
		avatarPhotos:  []int64{9},
		avatarCreated: map[int64]time.Time{9: time.Now()},
	}
	s := newManualPurgeTestService(fake)

	// No categories selected, but includeAvatars=true must still purge it.
	purgedDocs, purgedPhotos, _, err := s.ManualPurge(context.Background(), nil, true, nil, 100)
	if err != nil {
		t.Fatalf("ManualPurge: %v", err)
	}
	if purgedDocs != 0 || purgedPhotos != 1 {
		t.Fatalf("purgedDocs=%d purgedPhotos=%d, want 0/1", purgedDocs, purgedPhotos)
	}
	if len(fake.deletedPhotos) != 1 || fake.deletedPhotos[0] != 9 {
		t.Fatalf("deletedPhotos = %v, want only [9]", fake.deletedPhotos)
	}
}

// TestManualPurgeRejectsUnknownCategory guards validateManualPurgeCategories:
// an operator who fat-fingers a category should get an error, not a purge
// that quietly did less than they asked for.
func TestManualPurgeRejectsUnknownCategory(t *testing.T) {
	fake := &fakeManualPurgeStore{}
	s := newManualPurgeTestService(fake)

	invalid := domain.MediaCategory(99)
	if _, _, _, err := s.ManualPurge(context.Background(), []domain.MediaCategory{invalid}, false, nil, 100); err == nil {
		t.Fatal("ManualPurge with an unsupported category = nil error, want an error")
	}
	if _, _, err := s.CountManualPurgeCandidates(context.Background(), []domain.MediaCategory{invalid}, false, nil); err == nil {
		t.Fatal("CountManualPurgeCandidates with an unsupported category = nil error, want an error")
	}
	// MediaCategoryNone is a real category, but not one ManualPurge accepts
	// (see manualPurgeCategories's doc comment) -- it must be rejected too,
	// not silently treated as "match nothing"/"match everything".
	if _, _, _, err := s.ManualPurge(context.Background(), []domain.MediaCategory{domain.MediaCategoryNone}, false, nil, 100); err == nil {
		t.Fatal("ManualPurge with MediaCategoryNone = nil error, want an error")
	}
}
