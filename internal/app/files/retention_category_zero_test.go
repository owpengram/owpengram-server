package files

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// fakeCategorySweepStore records which category/photo/avatar queries
// DeleteBlobBytesForMediaOlderThan/DeleteOrphanedOlderThan actually issue, so
// the zero-age skip guard can be checked without a live database. Embeds a
// nil store.MediaStore so it satisfies that (large) interface for free via
// promoted methods that must never actually be called here -- only the
// mediaRetentionStore subset overridden below is exercised.
type fakeCategorySweepStore struct {
	store.MediaStore
	queriedDocCategories []domain.MediaCategory
	queriedPhotos        bool
	queriedAvatars       bool
}

func (f *fakeCategorySweepStore) ListDocumentIDsForHardRetentionOlderThan(_ context.Context, category domain.MediaCategory, _ *time.Time, _ int) ([]int64, error) {
	f.queriedDocCategories = append(f.queriedDocCategories, category)
	return nil, nil
}

func (f *fakeCategorySweepStore) ListPhotoIDsForHardRetentionOlderThan(context.Context, *time.Time, int) ([]int64, error) {
	f.queriedPhotos = true
	return nil, nil
}

func (f *fakeCategorySweepStore) ListAvatarPhotoIDsForHardRetentionOlderThan(context.Context, *time.Time, int) ([]int64, error) {
	f.queriedAvatars = true
	return nil, nil
}

func (f *fakeCategorySweepStore) CountDocumentsForHardRetention(context.Context, domain.MediaCategory, *time.Time) (int, error) {
	return 0, nil
}
func (f *fakeCategorySweepStore) CountPhotosForHardRetention(context.Context, *time.Time) (int, error) {
	return 0, nil
}
func (f *fakeCategorySweepStore) CountAvatarPhotosForHardRetention(context.Context, *time.Time) (int, error) {
	return 0, nil
}

func (f *fakeCategorySweepStore) ListOrphanedDocumentIDsOlderThan(_ context.Context, category domain.MediaCategory, _ time.Time, _ int) ([]int64, error) {
	f.queriedDocCategories = append(f.queriedDocCategories, category)
	return nil, nil
}

func (f *fakeCategorySweepStore) ListOrphanedPhotoIDsOlderThan(context.Context, time.Time, int) ([]int64, error) {
	f.queriedPhotos = true
	return nil, nil
}

func (f *fakeCategorySweepStore) ListAvatarOrphanedPhotoIDsOlderThan(context.Context, time.Time, int) ([]int64, error) {
	f.queriedAvatars = true
	return nil, nil
}

// The rest of mediaRetentionStore's methods aren't part of store.MediaStore
// (see that interface's own doc comment -- they're kept out of the hot
// RPC-facing interface on purpose), so embedding store.MediaStore alone
// doesn't provide them. Stub them out; this test never exercises them since
// every List* above returns no candidates.
func (f *fakeCategorySweepStore) CountFileBlobRefs(context.Context, string, string) (int, error) {
	return 0, nil
}
func (f *fakeCategorySweepStore) DeleteDocumentAndBlobs(context.Context, int64) ([]domain.FileBlob, error) {
	return nil, nil
}
func (f *fakeCategorySweepStore) DeletePhotoAndBlobs(context.Context, int64) ([]domain.FileBlob, error) {
	return nil, nil
}
func (f *fakeCategorySweepStore) OrphanDocumentIfUnreferenced(context.Context, int64) (bool, error) {
	return false, nil
}
func (f *fakeCategorySweepStore) DeleteFileBlobsForDocument(context.Context, int64) ([]domain.FileBlob, error) {
	return nil, nil
}
func (f *fakeCategorySweepStore) DeleteFileBlobsForPhoto(context.Context, int64) ([]domain.FileBlob, error) {
	return nil, nil
}
func (f *fakeCategorySweepStore) SumFileBlobBytes(context.Context) (int64, error) {
	return 0, nil
}
func (f *fakeCategorySweepStore) ListOldestMediaForEviction(context.Context, int) ([]domain.EvictionCandidate, error) {
	return nil, nil
}
func (f *fakeCategorySweepStore) ListMediaReferences(context.Context, domain.MediaKind, int64) ([]domain.MediaReference, error) {
	return nil, nil
}

// TestZeroGlobalRetentionAgeSkipsCategoriesWithoutOverride guards the bug
// reported live: setting the shared Retention age to 0 (meaning "only clean
// up categories I explicitly override") used to be indistinguishable from
// "everything is older than right now" at the per-category cutoff math,
// which would have hard-purged every uncategorized/photo/avatar item
// immediately. The sweep must instead skip any category whose *effective*
// age (its own override, or the 0 global) is <=0 entirely -- no query issued
// for it at all -- while still sweeping a category that has a real,
// positive override.
func TestZeroGlobalRetentionAgeSkipsCategoriesWithoutOverride(t *testing.T) {
	fake := &fakeCategorySweepStore{}
	s := &Service{
		media:                        fake,
		log:                          zap.NewNop(),
		storageRetentionGlobalMaxAge: 0,
		storageRetentionCategoryAges: map[domain.MediaCategory]time.Duration{
			domain.MediaCategoryVideo: 24 * time.Hour,
		},
		storageRetentionAvatarMaxAge: 0,
	}

	if _, err := s.DeleteBlobBytesForMediaOlderThan(context.Background(), time.Now(), 50); err != nil {
		t.Fatalf("DeleteBlobBytesForMediaOlderThan: %v", err)
	}

	if len(fake.queriedDocCategories) != 1 || fake.queriedDocCategories[0] != domain.MediaCategoryVideo {
		t.Fatalf("queried document categories = %v, want only [Video]", fake.queriedDocCategories)
	}
	if fake.queriedPhotos {
		t.Fatal("queried regular photos with no photo override and global age 0, want skipped")
	}
	if fake.queriedAvatars {
		t.Fatal("queried avatar photos with no avatar override and global age 0, want skipped")
	}
}

// TestZeroGlobalRetentionAgeSkipsOrphanSweepCategoriesWithoutOverride is the
// orphan-mode counterpart of the hard-mode test above.
func TestZeroGlobalRetentionAgeSkipsOrphanSweepCategoriesWithoutOverride(t *testing.T) {
	fake := &fakeCategorySweepStore{}
	s := &Service{
		media:                        fake,
		log:                          zap.NewNop(),
		storageRetentionGlobalMaxAge: 0,
		storageRetentionCategoryAges: map[domain.MediaCategory]time.Duration{
			domain.MediaCategoryVoice: 48 * time.Hour,
		},
		storageRetentionAvatarMaxAge: 0,
	}

	if _, err := s.DeleteOrphanedOlderThan(context.Background(), time.Now(), 50); err != nil {
		t.Fatalf("DeleteOrphanedOlderThan: %v", err)
	}

	if len(fake.queriedDocCategories) != 1 || fake.queriedDocCategories[0] != domain.MediaCategoryVoice {
		t.Fatalf("queried document categories = %v, want only [Voice]", fake.queriedDocCategories)
	}
	if fake.queriedPhotos {
		t.Fatal("queried regular photos with no photo override and global age 0, want skipped")
	}
	if fake.queriedAvatars {
		t.Fatal("queried avatar photos with no avatar override and global age 0, want skipped")
	}
}
