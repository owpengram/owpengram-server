package files

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"strings"
	"testing"
)

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestClassifyUploadHead_AliceContainerBlocked(t *testing.T) {
	head := append([]byte("ALICEU01\x01\x01\x00\x04\x00\x00\x00\x00\x00\x00\x00\x00"), randomBytes(t, 30000)...)
	if v, _ := classifyUploadHead(head); v != uploadContentBlockedFormat {
		t.Fatalf("verdict = %v, want blocked_format", v)
	}
	// Even tiny Alice files (media.bin ~2 KB) are blocked.
	if v, _ := classifyUploadHead([]byte("ALICEU01\x01\x01")); v != uploadContentBlockedFormat {
		t.Fatalf("small alice verdict = %v", v)
	}
}

func TestClassifyUploadHead_RandomIsOpaque(t *testing.T) {
	for _, n := range []int{4096, 16 << 10, 64 << 10} {
		head := randomBytes(t, n)
		// Avoid an accidental known-signature prefix (e.g. 0xFF 0xEx frame sync).
		head[0], head[1] = 0x13, 0x37
		if v, h := classifyUploadHead(head); v != uploadContentOpaque {
			t.Fatalf("n=%d verdict = %v (entropy %.4f, threshold %.4f), want opaque", n, v, h, randomEntropyThreshold(n))
		}
	}
}

func TestClassifyUploadHead_LegitimateFilesPass(t *testing.T) {
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write(randomBytes(t, 40000))
	_ = w.Close()

	mp4 := append([]byte{0, 0, 0, 0x20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, randomBytes(t, 40000)...)
	zip := append([]byte{'P', 'K', 0x03, 0x04}, randomBytes(t, 40000)...)
	sevenZip := append([]byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C}, randomBytes(t, 40000)...)
	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, randomBytes(t, 40000)...)
	text := []byte(strings.Repeat("Hello, this is an ordinary text document. ", 1000))
	small := randomBytes(t, 2000)

	cases := map[string][]byte{
		"gzip": gz.Bytes(), "mp4": mp4, "zip": zip, "7z": sevenZip, "jpeg": jpeg, "text": text, "small": small,
	}
	for name, head := range cases {
		if v, h := classifyUploadHead(head); v != uploadContentOK {
			t.Errorf("%s: verdict = %v (entropy %.4f), want ok", name, v, h)
		}
	}
}

func TestRandomEntropyThresholdBelowEight(t *testing.T) {
	for _, n := range []int{4096, 65536} {
		th := randomEntropyThreshold(n)
		if th <= 7.8 || th >= 8 {
			t.Fatalf("threshold(%d) = %.4f", n, th)
		}
	}
}
