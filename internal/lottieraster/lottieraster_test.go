package lottieraster_test

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"telesrv/internal/lottieraster"
	"telesrv/internal/seed/giftpacks"
)

// Every animation the bundled gift packs ship must come out as a visible
// thumb. LOTTIERASTER_DUMP=<dir> also writes them out for eyeballing against
// rlottie.
func TestRenderGiftPackAnimations(t *testing.T) {
	dump := os.Getenv("LOTTIERASTER_DUMP")
	for _, packID := range giftpacks.IDs() {
		_, assets, ok := giftpacks.Manifest(packID)
		if !ok {
			t.Fatalf("pack %q has no manifest", packID)
		}
		paths := make([]string, 0, len(assets))
		for path := range assets {
			if strings.HasSuffix(path, ".json") {
				paths = append(paths, path)
			}
		}
		sort.Strings(paths)
		for _, path := range paths {
			data, err := lottieraster.RenderPNG(assets[path], 128)
			if err != nil {
				t.Fatalf("%s/%s: %v", packID, path, err)
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("%s/%s: decode: %v", packID, path, err)
			}
			opaque := 0
			b := img.Bounds()
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
						opaque++
					}
				}
			}
			if opaque < 128*128/50 {
				t.Errorf("%s/%s: thumb is (nearly) blank: %d opaque pixels", packID, path, opaque)
			}
			if dump != "" {
				out := filepath.Join(dump, packID, strings.TrimSuffix(path, ".json")+".png")
				if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(out, data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestRenderRejectsGarbage(t *testing.T) {
	if _, err := lottieraster.RenderPNG([]byte("not json"), 64); err == nil {
		t.Fatal("expected an error for non-JSON input")
	}
	if _, err := lottieraster.RenderPNG([]byte(`{"w":0,"h":0,"layers":[]}`), 64); err == nil {
		t.Fatal("expected an error for an animation without a size")
	}
}
