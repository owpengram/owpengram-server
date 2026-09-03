package files

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// fakeEncryptedBlobStore is a minimal stateful store.MediaStore double for
// DeleteEncryptedFileBlob: a real in-memory map of location_key -> FileBlob
// (so GetFileBlob/DeleteFileBlobRow actually observe each other's effects).
// CountFileBlobRefs always reports "still referenced" so deleteOrphanedBlobs
// never reaches its physical-backend-delete path -- this test's Service has
// no real blob backend configured, and the point here is the file_blobs row
// deletion, which DeleteFileBlobRow already covers directly. Embeds a nil
// store.MediaStore for the rest of that large interface's methods, which
// this test never calls.
type fakeEncryptedBlobStore struct {
	store.MediaStore
	blobs          map[string]domain.FileBlob
	deletedRows    []string
	countRefsErr   error
	getFileBlobErr error
}

func (f *fakeEncryptedBlobStore) GetFileBlob(_ context.Context, key string) (domain.FileBlob, bool, error) {
	if f.getFileBlobErr != nil {
		return domain.FileBlob{}, false, f.getFileBlobErr
	}
	b, ok := f.blobs[key]
	return b, ok, nil
}

func (f *fakeEncryptedBlobStore) DeleteFileBlobRow(_ context.Context, key string) error {
	f.deletedRows = append(f.deletedRows, key)
	delete(f.blobs, key)
	return nil
}

func (f *fakeEncryptedBlobStore) CountFileBlobRefs(context.Context, string, string) (int, error) {
	if f.countRefsErr != nil {
		return 0, f.countRefsErr
	}
	// Reported as still-referenced (>0) so deleteOrphanedBlobs's physical
	// backend-delete path (this test's Service has no real backend
	// configured) is never reached -- this test only cares about the
	// file_blobs row itself, which DeleteFileBlobRow already removed by the
	// time deleteOrphanedBlobs runs.
	return 1, nil
}

// The rest of mediaRetentionStore's methods aren't part of store.MediaStore
// (kept out of the hot RPC-facing interface on purpose -- see that
// interface's own doc comment), so embedding store.MediaStore alone doesn't
// satisfy the type assertion DeleteEncryptedFileBlob performs. Stub them
// out; this test never exercises them.
func (f *fakeEncryptedBlobStore) ListOrphanedDocumentIDsOlderThan(context.Context, domain.MediaCategory, time.Time, int) ([]int64, error) {
	return nil, nil
}
func (f *fakeEncryptedBlobStore) ListOrphanedPhotoIDsOlderThan(context.Context, time.Time, int) ([]int64, error) {
	return nil, nil
}
func (f *fakeEncryptedBlobStore) ListAvatarOrphanedPhotoIDsOlderThan(context.Context, time.Time, int) ([]int64, error) {
	return nil, nil
}
func (f *fakeEncryptedBlobStore) DeleteDocumentAndBlobs(context.Context, int64) ([]domain.FileBlob, error) {
	return nil, nil
}
func (f *fakeEncryptedBlobStore) DeletePhotoAndBlobs(context.Context, int64) ([]domain.FileBlob, error) {
	return nil, nil
}
func (f *fakeEncryptedBlobStore) OrphanDocumentIfUnreferenced(context.Context, int64) (bool, error) {
	return false, nil
}
func (f *fakeEncryptedBlobStore) ListDocumentIDsForHardRetentionOlderThan(context.Context, domain.MediaCategory, *time.Time, int) ([]int64, error) {
	return nil, nil
}
func (f *fakeEncryptedBlobStore) ListPhotoIDsForHardRetentionOlderThan(context.Context, *time.Time, int) ([]int64, error) {
	return nil, nil
}
func (f *fakeEncryptedBlobStore) ListAvatarPhotoIDsForHardRetentionOlderThan(context.Context, *time.Time, int) ([]int64, error) {
	return nil, nil
}
func (f *fakeEncryptedBlobStore) CountDocumentsForHardRetention(context.Context, domain.MediaCategory, *time.Time) (int, error) {
	return 0, nil
}
func (f *fakeEncryptedBlobStore) CountPhotosForHardRetention(context.Context, *time.Time) (int, error) {
	return 0, nil
}
func (f *fakeEncryptedBlobStore) CountAvatarPhotosForHardRetention(context.Context, *time.Time) (int, error) {
	return 0, nil
}
func (f *fakeEncryptedBlobStore) DeleteFileBlobsForDocument(context.Context, int64) ([]domain.FileBlob, error) {
	return nil, nil
}
func (f *fakeEncryptedBlobStore) DeleteFileBlobsForPhoto(context.Context, int64) ([]domain.FileBlob, error) {
	return nil, nil
}
func (f *fakeEncryptedBlobStore) SumFileBlobBytes(context.Context) (int64, error) {
	return 0, nil
}
func (f *fakeEncryptedBlobStore) ListOldestMediaForEviction(context.Context, int) ([]domain.EvictionCandidate, error) {
	return nil, nil
}
func (f *fakeEncryptedBlobStore) ListMediaReferences(context.Context, domain.MediaKind, int64) ([]domain.MediaReference, error) {
	return nil, nil
}

func newTestServiceForSecretChatFiles(media store.MediaStore, enabled bool) *Service {
	return &Service{
		media:                             media,
		log:                               zap.NewNop(),
		blobCache:                         newBlobMetaCache(8),
		secretChatDeleteFileAfterDownload: enabled,
	}
}

func TestDeleteEncryptedFileBlobNoOpWhenDisabled(t *testing.T) {
	fake := &fakeEncryptedBlobStore{blobs: map[string]domain.FileBlob{
		"enc:1": {LocationKey: "enc:1", Backend: domain.MediaBackendS3, ObjectKey: "obj-1", Size: 42},
	}}
	s := newTestServiceForSecretChatFiles(fake, false)

	if err := s.DeleteEncryptedFileBlob(context.Background(), "enc:1"); err != nil {
		t.Fatalf("DeleteEncryptedFileBlob: %v", err)
	}
	if len(fake.deletedRows) != 0 {
		t.Fatalf("deleted rows = %v, want none (feature disabled)", fake.deletedRows)
	}
	if _, ok := fake.blobs["enc:1"]; !ok {
		t.Fatal("blob was removed from the store despite the feature being disabled")
	}
}

func TestDeleteEncryptedFileBlobDeletesWhenEnabled(t *testing.T) {
	fake := &fakeEncryptedBlobStore{blobs: map[string]domain.FileBlob{
		"enc:1": {LocationKey: "enc:1", Backend: domain.MediaBackendS3, ObjectKey: "obj-1", Size: 42},
	}}
	s := newTestServiceForSecretChatFiles(fake, true)

	if err := s.DeleteEncryptedFileBlob(context.Background(), "enc:1"); err != nil {
		t.Fatalf("DeleteEncryptedFileBlob: %v", err)
	}
	if len(fake.deletedRows) != 1 || fake.deletedRows[0] != "enc:1" {
		t.Fatalf("deleted rows = %v, want [enc:1]", fake.deletedRows)
	}
	if _, ok := fake.blobs["enc:1"]; ok {
		t.Fatal("blob row still present after DeleteEncryptedFileBlob")
	}
}

func TestDeleteEncryptedFileBlobNoOpWhenAlreadyGone(t *testing.T) {
	fake := &fakeEncryptedBlobStore{blobs: map[string]domain.FileBlob{}}
	s := newTestServiceForSecretChatFiles(fake, true)

	// A racing duplicate final-chunk request for the same file: the blob is
	// already deleted. Must not error.
	if err := s.DeleteEncryptedFileBlob(context.Background(), "enc:404"); err != nil {
		t.Fatalf("DeleteEncryptedFileBlob for a missing blob: %v", err)
	}
	if len(fake.deletedRows) != 0 {
		t.Fatalf("deleted rows = %v, want none", fake.deletedRows)
	}
}

func TestDeleteEncryptedFileBlobPropagatesLookupError(t *testing.T) {
	fake := &fakeEncryptedBlobStore{
		blobs:          map[string]domain.FileBlob{"enc:1": {LocationKey: "enc:1"}},
		getFileBlobErr: errors.New("db unavailable"),
	}
	s := newTestServiceForSecretChatFiles(fake, true)

	if err := s.DeleteEncryptedFileBlob(context.Background(), "enc:1"); err == nil {
		t.Fatal("DeleteEncryptedFileBlob = nil error, want the underlying lookup failure surfaced")
	}
	if len(fake.deletedRows) != 0 {
		t.Fatalf("deleted rows = %v, want none (lookup failed before any delete)", fake.deletedRows)
	}
}
