package files

import (
	"embed"
	"fmt"
)

const seedBundledDocumentThumbType = "m"

// StatusPack is exported without document thumbnails, while Android uses a
// non-empty thumbs vector to recognize application/x-tgsticker documents as
// animated custom emoji. Keep real, visible first-frame previews with the
// server's default media assets instead of inventing transparent metadata.
//
//go:embed statuspack_previews/*.png
var statusPackPreviewFS embed.FS

var seedBundledDocumentPreviews = map[int64][]byte{
	5244508282231465075: mustReadStatusPackPreview(5244508282231465075),
	5246743378917334735: mustReadStatusPackPreview(5246743378917334735),
	5246772116543512028: mustReadStatusPackPreview(5246772116543512028),
	5246828303305678732: mustReadStatusPackPreview(5246828303305678732),
	5246842176050046092: mustReadStatusPackPreview(5246842176050046092),
	5246960163096632543: mustReadStatusPackPreview(5246960163096632543),
	5247100325059370738: mustReadStatusPackPreview(5247100325059370738),
	5247133031235329609: mustReadStatusPackPreview(5247133031235329609),
	5247176827016847212: mustReadStatusPackPreview(5247176827016847212),
	5247209275494769660: mustReadStatusPackPreview(5247209275494769660),
	5249273776079640466: mustReadStatusPackPreview(5249273776079640466),
}

func mustReadStatusPackPreview(documentID int64) []byte {
	data, err := statusPackPreviewFS.ReadFile(fmt.Sprintf("statuspack_previews/%d.png", documentID))
	if err != nil {
		panic(fmt.Sprintf("read bundled StatusPack preview %d: %v", documentID, err))
	}
	return data
}

func seedBundledDocumentPreview(documentID int64) ([]byte, bool) {
	data, ok := seedBundledDocumentPreviews[documentID]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), data...), true
}
