package stargifts

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"

	"telesrv/internal/domain"
	"telesrv/internal/lottieraster"
)

// documentThumbSize is the raster "m" thumb's edge, as on Telegram's own
// animated-sticker documents.
const documentThumbSize = 128

// documentRasterThumb renders a gift (or collectible model/pattern)
// animation's first frame into the document's raster "m" thumb and stores
// its bytes.
//
// Every real Telegram .tgs document carries one, and clients draw the
// sticker from it whenever they show it frozen -- tweb's pinned gifts on a
// profile and its emoji patterns among others. Without it they download the
// .tgs itself into an <img>, which renders as a broken image.
//
// ok is false when the animation could not be rendered; the document then
// simply has no raster thumb, as before.
func (s *Service) documentRasterThumb(ctx context.Context, documentID int64, lottieJSON []byte) (domain.PhotoSize, *domain.FileBlob, bool, error) {
	data, err := lottieraster.RenderPNG(lottieJSON, documentThumbSize)
	if err != nil {
		return domain.PhotoSize{}, nil, false, nil
	}
	objectKey, err := s.blobs.Put(ctx, data)
	if err != nil {
		return domain.PhotoSize{}, nil, false, fmt.Errorf("store star gift document thumb: %w", err)
	}
	thumb := domain.PhotoSize{
		Kind: domain.PhotoSizeKindDefault, Type: "m",
		W: documentThumbSize, H: documentThumbSize, Size: len(data),
	}
	blob := &domain.FileBlob{
		LocationKey: fmt.Sprintf("doc:%d:%s", documentID, thumb.Type),
		Backend:     domain.MediaBackend(s.blobs.Name()),
		ObjectKey:   objectKey,
		Size:        int64(len(data)),
		MimeType:    "image/png",
	}
	return thumb, blob, true, nil
}

// animationLottieJSON returns an animation's Lottie JSON, from its .tgs
// when the JSON itself was not kept.
func animationLottieJSON(animation domain.StarGiftAnimation) []byte {
	if len(animation.JSON) > 0 {
		return animation.JSON
	}
	data, err := gunzipTGS(animation.TGS)
	if err != nil {
		return nil
	}
	return data
}

func gunzipTGS(tgs []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(tgs))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(io.LimitReader(zr, 16<<20))
}

func hasRasterThumb(thumbs []domain.PhotoSize) bool {
	for _, t := range thumbs {
		if t.W > 0 && t.H > 0 && ((t.Kind == domain.PhotoSizeKindDefault && t.Size > 0) ||
			(t.Kind == domain.PhotoSizeKindCached && len(t.Bytes) > 0)) {
			return true
		}
	}
	return false
}

// DocumentThumbStore is the media store RepairDocumentThumbs reads and writes.
type DocumentThumbStore interface {
	GetDocuments(ctx context.Context, ids []int64) ([]domain.Document, error)
	GetFileBlob(ctx context.Context, locationKey string) (domain.FileBlob, bool, error)
	PutFileBlob(ctx context.Context, blob domain.FileBlob) error
	PutDocument(ctx context.Context, doc domain.Document) error
}

// RepairDocumentThumbs gives a raster "m" thumb to the gift, collectible
// model and pattern documents among ids that were minted before
// documentRasterThumb existed. Documents that already have one are left
// alone, so it is cheap to run on every start. It returns how many documents
// it repaired.
func (s *Service) RepairDocumentThumbs(ctx context.Context, ids []int64, st DocumentThumbStore) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	docs, err := st.GetDocuments(ctx, ids)
	if err != nil {
		return 0, err
	}
	repaired := 0
	for _, doc := range docs {
		if doc.MimeType != "application/x-tgsticker" || hasRasterThumb(doc.Thumbs) {
			continue
		}
		blob, ok, err := st.GetFileBlob(ctx, fmt.Sprintf("doc:%d", doc.ID))
		if err != nil {
			return repaired, err
		}
		if !ok {
			continue
		}
		tgs, err := s.blobs.Get(ctx, blob.ObjectKey)
		if err != nil {
			continue // a missing object is reported where the document is served
		}
		lottieJSON, err := gunzipTGS(tgs)
		if err != nil {
			continue
		}
		thumb, thumbBlob, ok, err := s.documentRasterThumb(ctx, doc.ID, lottieJSON)
		if err != nil {
			return repaired, err
		}
		if !ok {
			continue
		}
		if err := st.PutFileBlob(ctx, *thumbBlob); err != nil {
			return repaired, err
		}
		doc.Thumbs = append(doc.Thumbs, thumb)
		if err := st.PutDocument(ctx, doc); err != nil {
			return repaired, err
		}
		repaired++
	}
	if repaired > 0 {
		s.InvalidateStarGiftCatalog()
	}
	return repaired, nil
}
