package files

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"testing"
)

// Web K downloads a peer photo as one 512 KiB part because the location
// carries no size, so every advertised size of the server's own avatars must
// be a real rendition of that size, small enough for one part.
func TestSeedAvatarSizesAreRenderedAndFitOneDownloadPart(t *testing.T) {
	const webKAvatarPart = 512 * 1024
	assets := map[string][]byte{
		"botfather": botFatherAvatarJPG,
		"chatbot":   chatBotAvatarPNG,
		"gifbot":    gifBotAvatarPNG,
		"premium":   premiumBotAvatarPNG,
		"stickers":  stickersBotAvatarPNG,
		"verify":    verifyBotAvatarPNG,
		"system":    officialSystemAvatarPNG,
	}
	ctx := context.Background()
	for name, data := range assets {
		media := newFakeMediaStore()
		blobs, err := NewLocalFS(t.TempDir())
		if err != nil {
			t.Fatalf("NewLocalFS: %v", err)
		}
		svc := NewService(media, blobs, 2)
		const photoID = 77
		sizes, err := svc.putSeedAvatarSizes(ctx, photoID, data)
		if err != nil {
			t.Fatalf("%s: putSeedAvatarSizes: %v", name, err)
		}
		for _, size := range sizes {
			if size.W > seedAvatarMaxSide || size.H > seedAvatarMaxSide {
				t.Fatalf("%s %s: %dx%d, want at most %d", name, size.Type, size.W, size.H, seedAvatarMaxSide)
			}
			blob, ok, err := media.GetFileBlob(ctx, fmt.Sprintf("photo:%d:%s", photoID, size.Type))
			if err != nil || !ok {
				t.Fatalf("%s %s: blob ok=%v err=%v", name, size.Type, ok, err)
			}
			body, _, err := blobs.GetRange(ctx, blob.ObjectKey, 0, blob.Size)
			if err != nil {
				t.Fatalf("%s %s: read blob: %v", name, size.Type, err)
			}
			if len(body) != size.Size || len(body) > webKAvatarPart {
				t.Fatalf("%s %s: %d bytes (advertised %d), want the advertised size and at most %d", name, size.Type, len(body), size.Size, webKAvatarPart)
			}
			cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
			if err != nil || cfg.Width != size.W || cfg.Height != size.H {
				t.Fatalf("%s %s: stored image %dx%d (err %v), advertised %dx%d", name, size.Type, cfg.Width, cfg.Height, err, size.W, size.H)
			}
		}
	}
}

// An operator identity icon Go cannot decode is still stored, as-is.
func TestSeedAvatarSizesKeepUndecodableIconAsIs(t *testing.T) {
	media := newFakeMediaStore()
	blobs, err := NewLocalFS(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalFS: %v", err)
	}
	svc := NewService(media, blobs, 2)
	icon := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"/>`)
	sizes, err := svc.putSeedAvatarSizes(context.Background(), 78, icon)
	if err != nil {
		t.Fatalf("putSeedAvatarSizes: %v", err)
	}
	if len(sizes) == 0 {
		t.Fatalf("no sizes stored")
	}
	for _, size := range sizes {
		if size.Size != len(icon) {
			t.Fatalf("%s: size %d, want the icon as-is (%d)", size.Type, size.Size, len(icon))
		}
	}
}
