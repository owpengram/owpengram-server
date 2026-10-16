package stargifts

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"testing"

	"telesrv/internal/domain"
)

type thumbTestBlobs map[string][]byte

func (b thumbTestBlobs) Name() string { return string(domain.MediaBackendLocalFS) }
func (b thumbTestBlobs) Put(_ context.Context, data []byte) (string, error) {
	key := fmt.Sprintf("obj-%d", len(b))
	b[key] = append([]byte(nil), data...)
	return key, nil
}
func (b thumbTestBlobs) Get(_ context.Context, key string) ([]byte, error) {
	data, ok := b[key]
	if !ok {
		return nil, fmt.Errorf("no object %q", key)
	}
	return data, nil
}

type thumbTestStore struct {
	docs  map[int64]domain.Document
	blobs map[string]domain.FileBlob
}

func (s *thumbTestStore) GetDocuments(_ context.Context, ids []int64) ([]domain.Document, error) {
	var out []domain.Document
	for _, id := range ids {
		if doc, ok := s.docs[id]; ok {
			out = append(out, doc)
		}
	}
	return out, nil
}
func (s *thumbTestStore) GetFileBlob(_ context.Context, key string) (domain.FileBlob, bool, error) {
	blob, ok := s.blobs[key]
	return blob, ok, nil
}
func (s *thumbTestStore) PutFileBlob(_ context.Context, blob domain.FileBlob) error {
	s.blobs[blob.LocationKey] = blob
	return nil
}
func (s *thumbTestStore) PutDocument(_ context.Context, doc domain.Document) error {
	s.docs[doc.ID] = doc
	return nil
}

const thumbTestLottie = `{"v":"5.5.2","fr":60,"ip":0,"op":60,"w":512,"h":512,"layers":[{"ty":4,"ind":1,"ip":0,"op":60,"st":0,"sr":1,
"ks":{"o":{"a":0,"k":100},"r":{"a":0,"k":0},"p":{"a":0,"k":[256,256]},"a":{"a":0,"k":[0,0]},"s":{"a":0,"k":[100,100]}},
"shapes":[{"ty":"gr","it":[{"ty":"el","p":{"a":0,"k":[0,0]},"s":{"a":0,"k":[300,300]}},{"ty":"fl","c":{"a":0,"k":[1,0,0,1]},"o":{"a":0,"k":100},"r":1},
{"ty":"tr","p":{"a":0,"k":[0,0]},"a":{"a":0,"k":[0,0]},"s":{"a":0,"k":[100,100]},"r":{"a":0,"k":0},"o":{"a":0,"k":100}}]}]}]}`

func TestRepairDocumentThumbsAddsRasterThumbOnce(t *testing.T) {
	ctx := context.Background()
	var tgs bytes.Buffer
	zw := gzip.NewWriter(&tgs)
	_, _ = zw.Write([]byte(thumbTestLottie))
	_ = zw.Close()

	blobs := thumbTestBlobs{"anim": tgs.Bytes()}
	st := &thumbTestStore{
		docs: map[int64]domain.Document{
			7: {ID: 7, MimeType: "application/x-tgsticker"},
			8: {ID: 8, MimeType: "image/webp"},
		},
		blobs: map[string]domain.FileBlob{"doc:7": {LocationKey: "doc:7", ObjectKey: "anim"}},
	}
	svc := NewService(nil, blobs, 1)

	repaired, err := svc.RepairDocumentThumbs(ctx, []int64{7, 8}, st)
	if err != nil || repaired != 1 {
		t.Fatalf("repair = %d, %v; want 1 document", repaired, err)
	}
	doc := st.docs[7]
	if len(doc.Thumbs) != 1 || doc.Thumbs[0].Type != "m" || doc.Thumbs[0].W != documentThumbSize || doc.Thumbs[0].Size <= 0 {
		t.Fatalf("thumbs = %+v, want one raster m thumb", doc.Thumbs)
	}
	blob, ok := st.blobs["doc:7:m"]
	if !ok || blob.MimeType != "image/png" || int(blob.Size) != doc.Thumbs[0].Size {
		t.Fatalf("thumb blob = %+v (found %v)", blob, ok)
	}
	if !bytes.HasPrefix(blobs[blob.ObjectKey], []byte("\x89PNG")) {
		t.Fatal("thumb object is not a PNG")
	}

	again, err := svc.RepairDocumentThumbs(ctx, []int64{7, 8}, st)
	if err != nil || again != 0 {
		t.Fatalf("second repair = %d, %v; want nothing to do", again, err)
	}
}
