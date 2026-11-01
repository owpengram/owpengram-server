package lottieraster_test

import (
	"archive/zip"
	"bytes"
	"io"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"telesrv/internal/lottieraster"
	"telesrv/internal/app/giftpack/giftpacktest"
)

// Every animation of the test gift pack must come out as a visible
// thumb. LOTTIERASTER_DUMP=<dir> also writes them out for eyeballing against
// rlottie.
func TestRenderGiftPackAnimations(t *testing.T) {
	dump := os.Getenv("LOTTIERASTER_DUMP")
	for _, packID := range []string{"phone-pack"} {
		assets := phonePackAnimations(t)
		paths := make([]string, 0, len(assets))
		for path := range assets {
			if strings.HasSuffix(path, ".json") && path != "pack.json" {
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

// phonePackAnimations reads every file of the test pack's archive, the way an
// operator's upload would be opened.
func phonePackAnimations(t *testing.T) map[string][]byte {
	t.Helper()
	data := giftpacktest.PhonePackZip()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open phone pack: %v", err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = body
	}
	return out
}

func TestRenderRejectsGarbage(t *testing.T) {
	if _, err := lottieraster.RenderPNG([]byte("not json"), 64); err == nil {
		t.Fatal("expected an error for non-JSON input")
	}
	if _, err := lottieraster.RenderPNG([]byte(`{"w":0,"h":0,"layers":[]}`), 64); err == nil {
		t.Fatal("expected an error for an animation without a size")
	}
}
