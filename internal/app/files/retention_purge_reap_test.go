package files

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// fakeReapStore is a minimal mediaRetentionStore fake exercising only
// notifyRetentionPurge's new reap step: ListMediaReferences reports one
// message_box reference, and orphanedDocument/orphanedPhoto control what
// OrphanDocumentIfUnreferenced/OrphanPhotoIfUnreferenced report -- simulating
// "the edit above dropped the last reference" (true) vs "something else
// still references it" (false, e.g. a live avatar or a failed edit). Every
// other mediaRetentionStore method is a stub never expected to be called by
// these tests -- but must still exist, or the type assertion
// s.media.(mediaRetentionStore) inside notifyRetentionPurge silently fails
// and the whole test becomes a no-op instead of exercising anything.
type fakeReapStore struct {
	store.MediaStore
	orphanedDocument   bool
	orphanedPhoto      bool
	deletedDocumentIDs []int64
	deletedPhotoIDs    []int64
}

func (f *fakeReapStore) ListMediaReferences(context.Context, domain.MediaKind, int64) ([]domain.MediaReference, error) {
	return []domain.MediaReference{{RefKind: domain.MediaRefKindMessageBox, RefKey: "user:1:box:2"}}, nil
}
func (f *fakeReapStore) OrphanDocumentIfUnreferenced(context.Context, int64) (bool, error) {
	return f.orphanedDocument, nil
}
func (f *fakeReapStore) OrphanPhotoIfUnreferenced(context.Context, int64) (bool, error) {
	return f.orphanedPhoto, nil
}
func (f *fakeReapStore) DeleteDocumentAndBlobs(_ context.Context, id int64) ([]domain.FileBlob, error) {
	f.deletedDocumentIDs = append(f.deletedDocumentIDs, id)
	return nil, nil
}
func (f *fakeReapStore) DeletePhotoAndBlobs(_ context.Context, id int64) ([]domain.FileBlob, error) {
	f.deletedPhotoIDs = append(f.deletedPhotoIDs, id)
	return nil, nil
}
func (f *fakeReapStore) ListOrphanedDocumentIDsOlderThan(context.Context, domain.MediaCategory, time.Time, int) ([]int64, error) {
	return nil, nil
}
func (f *fakeReapStore) ListOrphanedPhotoIDsOlderThan(context.Context, time.Time, int) ([]int64, error) {
	return nil, nil
}
func (f *fakeReapStore) ListAvatarOrphanedPhotoIDsOlderThan(context.Context, time.Time, int) ([]int64, error) {
	return nil, nil
}
func (f *fakeReapStore) CountFileBlobRefs(context.Context, string, string) (int, error) {
	return 0, nil
}
func (f *fakeReapStore) ListDocumentIDsForHardRetentionOlderThan(context.Context, domain.MediaCategory, *time.Time, int) ([]int64, error) {
	return nil, nil
}
func (f *fakeReapStore) ListPhotoIDsForHardRetentionOlderThan(context.Context, *time.Time, int) ([]int64, error) {
	return nil, nil
}
func (f *fakeReapStore) ListAvatarPhotoIDsForHardRetentionOlderThan(context.Context, *time.Time, int) ([]int64, error) {
	return nil, nil
}
func (f *fakeReapStore) CountDocumentsForHardRetention(context.Context, domain.MediaCategory, *time.Time) (int, error) {
	return 0, nil
}
func (f *fakeReapStore) CountPhotosForHardRetention(context.Context, *time.Time) (int, error) {
	return 0, nil
}
func (f *fakeReapStore) CountAvatarPhotosForHardRetention(context.Context, *time.Time) (int, error) {
	return 0, nil
}
func (f *fakeReapStore) DeleteFileBlobsForDocument(context.Context, int64) ([]domain.FileBlob, error) {
	return nil, nil
}
func (f *fakeReapStore) DeleteFileBlobsForPhoto(context.Context, int64) ([]domain.FileBlob, error) {
	return nil, nil
}
func (f *fakeReapStore) SumFileBlobBytes(context.Context) (int64, error) { return 0, nil }
func (f *fakeReapStore) ListOldestMediaForEviction(context.Context, int) ([]domain.EvictionCandidate, error) {
	return nil, nil
}

// fakeReapMessages is the minimal RetentionPurgeMessageEditor: GetMessages
// resolves a peer so notifyRetentionPurgeMessageBox can build the edit
// request, and EditMessage always succeeds.
type fakeReapMessages struct{}

func (fakeReapMessages) GetMessages(context.Context, int64, []int) (domain.MessageList, error) {
	return domain.MessageList{Messages: []domain.Message{{ID: 2, Peer: domain.Peer{Type: domain.PeerTypeUser, ID: 3}}}}, nil
}
func (fakeReapMessages) EditMessage(context.Context, int64, domain.EditMessageRequest) (domain.EditMessageResult, error) {
	return domain.EditMessageResult{}, nil
}

func newTestServiceForReap(media store.MediaStore) *Service {
	s := &Service{media: media, log: zap.NewNop(), blobCache: newBlobMetaCache(8)}
	s.SetRetentionPurgeNotifier(fakeReapMessages{}, nil)
	return s
}

// TestNotifyRetentionPurgeReapsDocumentWhenFullyUnreferenced guards the fix
// for "documents/photos rows pile up forever even after the message showing
// them was already converted to the purge notice": once nothing references a
// purged document any more, notifyRetentionPurge must delete the row for
// real instead of always keeping it.
func TestNotifyRetentionPurgeReapsDocumentWhenFullyUnreferenced(t *testing.T) {
	fake := &fakeReapStore{orphanedDocument: true}
	s := newTestServiceForReap(fake)

	s.notifyRetentionPurge(context.Background(), domain.MediaKindDocument, 42)

	if len(fake.deletedDocumentIDs) != 1 || fake.deletedDocumentIDs[0] != 42 {
		t.Fatalf("deleted document ids = %v, want [42]", fake.deletedDocumentIDs)
	}
}

// TestNotifyRetentionPurgeKeepsDocumentWhenStillReferenced is the negative
// case: OrphanDocumentIfUnreferenced reporting false (something else -- a
// live avatar, sticker set, or a failed edit -- still references it) must
// leave the row alone, exactly like the safe orphan-mode deletion this reuses.
func TestNotifyRetentionPurgeKeepsDocumentWhenStillReferenced(t *testing.T) {
	fake := &fakeReapStore{orphanedDocument: false}
	s := newTestServiceForReap(fake)

	s.notifyRetentionPurge(context.Background(), domain.MediaKindDocument, 42)

	if len(fake.deletedDocumentIDs) != 0 {
		t.Fatalf("deleted document ids = %v, want none", fake.deletedDocumentIDs)
	}
}

// TestNotifyRetentionPurgeReapsPhotoWhenFullyUnreferenced is the photo
// counterpart of the document test above.
func TestNotifyRetentionPurgeReapsPhotoWhenFullyUnreferenced(t *testing.T) {
	fake := &fakeReapStore{orphanedPhoto: true}
	s := newTestServiceForReap(fake)

	s.notifyRetentionPurge(context.Background(), domain.MediaKindPhoto, 99)

	if len(fake.deletedPhotoIDs) != 1 || fake.deletedPhotoIDs[0] != 99 {
		t.Fatalf("deleted photo ids = %v, want [99]", fake.deletedPhotoIDs)
	}
}
