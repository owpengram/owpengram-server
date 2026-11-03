package admin

import (
	"net/http"
	"testing"
)

func TestStickerPreviewTypeAcceptsVideoStickers(t *testing.T) {
	// The first bytes of a WebM file (EBML header + "webm" doc type), as a
	// video sticker or video custom emoji starts.
	webm := []byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x86, 0x81, 0x01, 0x42, 0xF7, 0x81, 0x01, 0x42, 0xF2, 0x81, 0x04, 0x42, 0xF3, 0x81, 0x08, 0x42, 0x82, 0x84, 'w', 'e', 'b', 'm'}
	if got := http.DetectContentType(webm); !isSafeStickerPreviewType(got) {
		t.Fatalf("WebM sniffed as %q is refused; video stickers would show as not found", got)
	}
	for _, kind := range []string{"image/webp", "image/png", "video/webm", "video/mp4"} {
		if !isSafeStickerPreviewType(kind) {
			t.Errorf("%s refused", kind)
		}
	}
	for _, kind := range []string{"image/svg+xml", "text/html; charset=utf-8", "application/octet-stream"} {
		if isSafeStickerPreviewType(kind) {
			t.Errorf("%s allowed; it must never be served as a preview", kind)
		}
	}
}
