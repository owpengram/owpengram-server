package editionmigrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"testing"

	filesapp "telesrv/internal/app/files"
	"telesrv/internal/domain"
	"telesrv/internal/store/postgres"
)

type memStore struct{ objects map[string][]byte }

func newMemStore() *memStore { return &memStore{objects: map[string][]byte{}} }

func (m *memStore) PutReader(_ context.Context, r io.Reader) (string, int64, []byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", 0, nil, err
	}
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])
	m.objects[key] = data
	return key, int64(len(data)), sum[:], nil
}

func (m *memStore) GetRange(_ context.Context, key string, offset, limit int64) ([]byte, int64, error) {
	data, ok := m.objects[key]
	if !ok {
		return nil, 0, fmt.Errorf("open %s: %w", key, fs.ErrNotExist)
	}
	total := int64(len(data))
	if offset >= total {
		return nil, total, nil
	}
	end := total
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return data[offset:end], total, nil
}

type memLedger struct {
	rows map[string]domain.MediaBackend // object key -> backend
	meta map[string]postgres.BlobMigrationObject
}

func (l *memLedger) ListBlobMigrationObjects(_ context.Context, backend domain.MediaBackend, after string, limit int) ([]postgres.BlobMigrationObject, error) {
	var keys []string
	for key, b := range l.rows {
		if b == backend && key > after {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	out := make([]postgres.BlobMigrationObject, 0, len(keys))
	for _, key := range keys {
		out = append(out, l.meta[key])
	}
	return out, nil
}

func (l *memLedger) MoveFileBlobBackendForObject(_ context.Context, from, to domain.MediaBackend, key string, rows int64) error {
	if l.rows[key] != from || l.meta[key].LocationRows != rows {
		return errors.New("unexpected ledger state")
	}
	l.rows[key] = to
	return nil
}

func addBlob(t *testing.T, src *memStore, l *memLedger, data []byte, rows int64) string {
	t.Helper()
	key, size, sum, err := src.PutReader(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	l.rows[key] = domain.MediaBackendS3
	l.meta[key] = postgres.BlobMigrationObject{ObjectKey: key, Size: size, SHA256: sum, LocationRows: rows}
	return key
}

func newLedger() *memLedger {
	return &memLedger{rows: map[string]domain.MediaBackend{}, meta: map[string]postgres.BlobMigrationObject{}}
}

func TestCopyBlobsMovesEverythingAndRelabels(t *testing.T) {
	src, dst, ledger := newMemStore(), newMemStore(), newLedger()
	big := bytes.Repeat([]byte("x"), blobChunkSize*2+123) // spans several ranges
	keys := []string{
		addBlob(t, src, ledger, []byte("hello"), 1),
		addBlob(t, src, ledger, big, 3),
		addBlob(t, src, ledger, nil, 1), // empty object
	}
	// Enough extra objects to need more than one list page.
	for i := 0; i < blobListPage+5; i++ {
		keys = append(keys, addBlob(t, src, ledger, []byte(strings.Repeat("a", i+1)), 1))
	}

	res, err := CopyBlobs(context.Background(), ledger, domain.MediaBackendS3, domain.MediaBackendLocalFS, src, dst, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != len(keys) || len(res.Skipped) != 0 {
		t.Fatalf("result = %+v, want %d moved", res, len(keys))
	}
	for _, key := range keys {
		if ledger.rows[key] != domain.MediaBackendLocalFS {
			t.Fatalf("%s still labelled %s", key, ledger.rows[key])
		}
		if !bytes.Equal(dst.objects[key], src.objects[key]) {
			t.Fatalf("%s bytes differ at destination", key)
		}
		if _, ok := src.objects[key]; !ok {
			t.Fatalf("%s was removed from the source", key)
		}
	}
}

// A source whose bytes are gone is skipped by default: it is already
// unreadable, and aborting would trap the install on its current edition.
func TestCopyBlobsSkipsMissingSourceObject(t *testing.T) {
	src, dst, ledger := newMemStore(), newMemStore(), newLedger()
	good := addBlob(t, src, ledger, []byte("good"), 1)
	missing := addBlob(t, src, ledger, []byte("missing"), 1)
	delete(src.objects, missing)

	res, err := CopyBlobs(context.Background(), ledger, domain.MediaBackendS3, domain.MediaBackendLocalFS, src, dst, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != 1 || len(res.Skipped) != 1 || res.Skipped[0] != missing {
		t.Fatalf("result = %+v", res)
	}
	if ledger.rows[good] != domain.MediaBackendLocalFS || ledger.rows[missing] != domain.MediaBackendS3 {
		t.Fatalf("labels = %v", ledger.rows)
	}
}

// Anything other than "not there" could be transient or hide readable
// media, so it aborts unless the caller opted into skipping.
func TestCopyBlobsAbortsOnOtherSourceErrors(t *testing.T) {
	ledger := newLedger()
	src, dst := newMemStore(), newMemStore()
	key := addBlob(t, src, ledger, []byte("data"), 1)
	flaky := errStore{src, errors.New("connection refused")}

	_, err := CopyBlobs(context.Background(), ledger, domain.MediaBackendS3, domain.MediaBackendLocalFS, flaky, dst, false, nil)
	if err == nil || !strings.Contains(err.Error(), key) {
		t.Fatalf("err = %v, want an abort naming %s", err, key)
	}
	if ledger.rows[key] != domain.MediaBackendS3 {
		t.Fatal("a failed object must stay labelled with its source backend")
	}

	res, err := CopyBlobs(context.Background(), ledger, domain.MediaBackendS3, domain.MediaBackendLocalFS, flaky, dst, true, nil)
	if err != nil || len(res.Skipped) != 1 {
		t.Fatalf("tolerant run: res=%+v err=%v", res, err)
	}
}

type errStore struct {
	*memStore
	err error
}

func (e errStore) GetRange(context.Context, string, int64, int64) ([]byte, int64, error) {
	return nil, 0, e.err
}

// The real LocalFS reports a missing file as fs.ErrNotExist, which is what
// isMissingObject keys on.
func TestCopyBlobsSkipsMissingFileOnRealLocalFS(t *testing.T) {
	ctx := context.Background()
	srcFS, err := filesapp.NewLocalFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dstFS, err := filesapp.NewLocalFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ledger := newLedger()
	sum := sha256.Sum256([]byte("never written"))
	key := hex.EncodeToString(sum[:])
	ledger.rows[key] = domain.MediaBackendLocalFS
	ledger.meta[key] = postgres.BlobMigrationObject{ObjectKey: key, Size: 13, SHA256: sum[:], LocationRows: 1}

	res, err := CopyBlobs(ctx, ledger, domain.MediaBackendLocalFS, domain.MediaBackendS3, srcFS, dstFS, false, nil)
	if err != nil || len(res.Skipped) != 1 {
		t.Fatalf("res=%+v err=%v, want the missing file skipped", res, err)
	}
}

func TestCopyBlobsRejectsCorruptSourceObject(t *testing.T) {
	src, dst, ledger := newMemStore(), newMemStore(), newLedger()
	key := addBlob(t, src, ledger, []byte("original"), 1)
	src.objects[key] = []byte("tampered") // no longer hashes to its own key

	_, err := CopyBlobs(context.Background(), ledger, domain.MediaBackendS3, domain.MediaBackendLocalFS, src, dst, false, nil)
	if err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("err = %v, want a damaged-source error", err)
	}
	if ledger.rows[key] != domain.MediaBackendS3 {
		t.Fatal("a corrupt object must not be relabelled")
	}
}

// The in-memory store above encodes this package's assumptions about
// GetRange; this runs the same copy through the real LocalFS on both sides.
func TestCopyBlobsBetweenRealLocalFS(t *testing.T) {
	ctx := context.Background()
	srcFS, err := filesapp.NewLocalFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dstFS, err := filesapp.NewLocalFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ledger := newLedger()
	payloads := [][]byte{nil, []byte("small"), bytes.Repeat([]byte("0123456789"), blobChunkSize/10*2+7)}
	var keys []string
	for _, data := range payloads {
		key, size, sum, err := srcFS.PutReader(ctx, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		ledger.rows[key] = domain.MediaBackendS3
		ledger.meta[key] = postgres.BlobMigrationObject{ObjectKey: key, Size: size, SHA256: sum, LocationRows: 1}
		keys = append(keys, key)
	}

	res, err := CopyBlobs(ctx, ledger, domain.MediaBackendS3, domain.MediaBackendLocalFS, srcFS, dstFS, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != len(payloads) {
		t.Fatalf("moved %d, want %d", res.Moved, len(payloads))
	}
	for i, key := range keys {
		got, err := dstFS.Get(ctx, key)
		if err != nil || !bytes.Equal(got, payloads[i]) {
			t.Fatalf("object %d at destination: len=%d err=%v, want %d bytes", i, len(got), err, len(payloads[i]))
		}
	}
}
