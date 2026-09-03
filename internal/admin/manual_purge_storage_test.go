package admin

import (
	"context"
	"testing"
	"time"

	"telesrv/internal/domain"
)

// fakeStorageService is a minimal StorageService double recording what
// ManualPurgeStorage actually asked it to count/purge, so tests can assert on
// category parsing (including "avatar" never becoming a domain.MediaCategory
// argument) without a real files.Service/Postgres.
type fakeStorageService struct {
	countCalls int
	purgeCalls int

	lastCategories     []domain.MediaCategory
	lastIncludeAvatars bool
	lastBefore         *time.Time

	countDocs, countPhotos   int
	purgedDocs, purgedPhotos int
	bytesReclaimed           int64
	countErr, purgeErr       error
}

func (f *fakeStorageService) CountManualPurgeCandidates(_ context.Context, categories []domain.MediaCategory, includeAvatars bool, before *time.Time) (int, int, error) {
	f.countCalls++
	f.lastCategories = categories
	f.lastIncludeAvatars = includeAvatars
	f.lastBefore = before
	return f.countDocs, f.countPhotos, f.countErr
}

func (f *fakeStorageService) ManualPurge(_ context.Context, categories []domain.MediaCategory, includeAvatars bool, before *time.Time, _ int) (int, int, int64, error) {
	f.purgeCalls++
	f.lastCategories = categories
	f.lastIncludeAvatars = includeAvatars
	f.lastBefore = before
	return f.purgedDocs, f.purgedPhotos, f.bytesReclaimed, f.purgeErr
}

func TestManualPurgeStorageDryRunReportsCounts(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryCommandRepo()
	storage := &fakeStorageService{countDocs: 3, countPhotos: 2}
	svc := NewService(Dependencies{Commands: repo, Storage: storage, Now: fixedNow})

	req := ManualPurgeStorageRequest{
		CommandMeta: CommandMeta{CommandID: "purge-dry-1", Actor: "ops", Reason: "cleanup", DryRun: true},
		Categories:  []string{"video", "Music"},
	}
	result, err := svc.ManualPurgeStorage(ctx, req)
	if err != nil {
		t.Fatalf("ManualPurgeStorage(dry-run): %v", err)
	}
	if !result.DryRun {
		t.Fatalf("result.DryRun = false, want true")
	}
	if result.Details["would_delete_documents"] != 3 || result.Details["would_delete_photos"] != 2 {
		t.Fatalf("details = %+v, want would_delete_documents=3, would_delete_photos=2", result.Details)
	}
	if storage.countCalls != 1 || storage.purgeCalls != 0 {
		t.Fatalf("countCalls=%d purgeCalls=%d, want 1/0 for a dry run", storage.countCalls, storage.purgeCalls)
	}
	want := map[domain.MediaCategory]bool{domain.MediaCategoryVideo: true, domain.MediaCategoryMusic: true}
	if len(storage.lastCategories) != 2 || !want[storage.lastCategories[0]] || !want[storage.lastCategories[1]] {
		t.Fatalf("lastCategories = %v, want [Video, Music] (case-insensitively parsed)", storage.lastCategories)
	}
	if storage.lastBefore != nil {
		t.Fatalf("lastBefore = %v, want nil (no created_before supplied)", storage.lastBefore)
	}
}

func TestManualPurgeStorageConfirmReportsPurgedCounts(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryCommandRepo()
	storage := &fakeStorageService{purgedDocs: 5, purgedPhotos: 1, bytesReclaimed: 4096}
	svc := NewService(Dependencies{Commands: repo, Storage: storage, Now: fixedNow})

	before := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	req := ManualPurgeStorageRequest{
		CommandMeta:   CommandMeta{CommandID: "purge-confirm-1", Actor: "ops", Reason: "cleanup"},
		Categories:    []string{"file"},
		CreatedBefore: &before,
	}
	result, err := svc.ManualPurgeStorage(ctx, req)
	if err != nil {
		t.Fatalf("ManualPurgeStorage(confirm): %v", err)
	}
	if result.Details["purged_documents"] != 5 || result.Details["purged_photos"] != 1 || result.Details["bytes_reclaimed"] != int64(4096) {
		t.Fatalf("details = %+v, want purged_documents=5, purged_photos=1, bytes_reclaimed=4096", result.Details)
	}
	if storage.purgeCalls != 1 || storage.countCalls != 0 {
		t.Fatalf("purgeCalls=%d countCalls=%d, want 1/0 for a confirm", storage.purgeCalls, storage.countCalls)
	}
	if storage.lastBefore == nil || !storage.lastBefore.Equal(before) {
		t.Fatalf("lastBefore = %v, want %v", storage.lastBefore, before)
	}
}

// TestManualPurgeStorageAvatarIsNotAMediaCategory guards the "avatar" key's
// special handling: it must flip IncludeAvatars and must NEVER appear in the
// categories slice handed to the files.Service layer, since Avatar is not a
// domain.MediaCategory (see files.Service.ManualPurge's doc comment).
func TestManualPurgeStorageAvatarIsNotAMediaCategory(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryCommandRepo()
	storage := &fakeStorageService{}
	svc := NewService(Dependencies{Commands: repo, Storage: storage, Now: fixedNow})

	req := ManualPurgeStorageRequest{
		CommandMeta:    CommandMeta{CommandID: "purge-avatar-1", Actor: "ops", Reason: "cleanup", DryRun: true},
		Categories:     []string{"photo"},
		IncludeAvatars: true,
	}
	if _, err := svc.ManualPurgeStorage(ctx, req); err != nil {
		t.Fatalf("ManualPurgeStorage: %v", err)
	}
	if !storage.lastIncludeAvatars {
		t.Fatal("lastIncludeAvatars = false, want true")
	}
	if len(storage.lastCategories) != 1 || storage.lastCategories[0] != domain.MediaCategoryPhoto {
		t.Fatalf("lastCategories = %v, want exactly [Photo]", storage.lastCategories)
	}
}

// TestManualPurgeStorageRejectsUnknownCategory guards the admin-boundary
// validation: an unknown category key must be rejected with a clear error,
// not silently dropped, and must never reach the underlying service.
func TestManualPurgeStorageRejectsUnknownCategory(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryCommandRepo()
	storage := &fakeStorageService{}
	svc := NewService(Dependencies{Commands: repo, Storage: storage, Now: fixedNow})

	req := ManualPurgeStorageRequest{
		CommandMeta: CommandMeta{CommandID: "purge-bad-1", Actor: "ops", Reason: "cleanup", DryRun: true},
		Categories:  []string{"sticker"},
	}
	if _, err := svc.ManualPurgeStorage(ctx, req); err == nil {
		t.Fatal("ManualPurgeStorage with an unknown category = nil error, want an error")
	}
	if storage.countCalls != 0 {
		t.Fatalf("countCalls = %d, want 0 (validation must fail before reaching the service)", storage.countCalls)
	}
}

// TestManualPurgeStorageRequiresACategoryOrAvatars guards against a request
// that would purge nothing at all (no categories, avatars not included)
// being silently accepted as a no-op success.
func TestManualPurgeStorageRequiresACategoryOrAvatars(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryCommandRepo()
	storage := &fakeStorageService{}
	svc := NewService(Dependencies{Commands: repo, Storage: storage, Now: fixedNow})

	req := ManualPurgeStorageRequest{
		CommandMeta: CommandMeta{CommandID: "purge-empty-1", Actor: "ops", Reason: "cleanup", DryRun: true},
	}
	if _, err := svc.ManualPurgeStorage(ctx, req); err == nil {
		t.Fatal("ManualPurgeStorage with no categories and no avatars = nil error, want an error")
	}
}
