package files

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func cpRandom(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func inspectBytes(data []byte) inspection { return inspectContent(bytes.NewReader(data)) }

func cpImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 96, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 96; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 2), uint8(y * 3), uint8(x ^ y), 255})
		}
	}
	return img
}

func cpJPEG(t *testing.T, progressive bool) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, cpImage(), &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func cpPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, cpImage()); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func cpGIF(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := gif.Encode(&b, cpImage(), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func cpZIP(t *testing.T, encrypted bool) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	if encrypted {
		// archive/zip cannot encrypt; a raw entry with the encryption flag is
		// exactly what a password-protected archive's directory looks like.
		payload := cpRandom(t, 4000)
		fw, err := w.CreateRaw(&zip.FileHeader{Name: "secret.bin", Flags: 0x1, Method: zip.Store,
			CRC32: crc32.ChecksumIEEE(payload), CompressedSize64: uint64(len(payload)), UncompressedSize64: uint64(len(payload))})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(payload)
	} else {
		fw, _ := w.Create("readme.txt")
		_, _ = fw.Write([]byte(strings.Repeat("hello zip ", 500)))
		fw2, _ := w.Create("photo.jpg")
		_, _ = fw2.Write(cpJPEG(t, false))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func cpGzip(t *testing.T, payload []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write(payload)
	_ = w.Close()
	return b.Bytes()
}

func cp7z(nextHeader []byte) []byte {
	start := make([]byte, 20)
	binary.LittleEndian.PutUint64(start[0:], 0)
	binary.LittleEndian.PutUint64(start[8:], uint64(len(nextHeader)))
	binary.LittleEndian.PutUint32(start[16:], crc32.ChecksumIEEE(nextHeader))
	out := []byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C, 0, 4}
	var crc [4]byte
	binary.LittleEndian.PutUint32(crc[:], crc32.ChecksumIEEE(start))
	return cat(out, crc[:], start, nextHeader)
}

func cpRAR5(headerType byte) []byte {
	data := []byte{headerType, 0x00, 0x00}
	h := append([]byte{byte(len(data))}, data...)
	var crc [4]byte
	binary.LittleEndian.PutUint32(crc[:], crc32.ChecksumIEEE(h))
	return cat([]byte("Rar!\x1A\x07\x01\x00"), crc[:], h, bytes.Repeat([]byte{0x11}, 2000))
}

func cpRAR4(flags uint16) []byte {
	hdr := []byte{0x73, byte(flags), byte(flags >> 8), 13, 0, 0, 0, 0, 0, 0, 0}
	crc := uint16(crc32.ChecksumIEEE(hdr))
	return cat([]byte("Rar!\x1A\x07\x00"), []byte{byte(crc), byte(crc >> 8)}, hdr, bytes.Repeat([]byte{0x22}, 2000))
}

func cpFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "content", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Real files of every common kind must pass untouched.
func TestInspectContentAcceptsLegitimateFiles(t *testing.T) {
	cases := map[string][]byte{
		"jpeg":            cpJPEG(t, false),
		"png":             cpPNG(t),
		"gif":             cpGIF(t),
		"zip":             cpZIP(t, false),
		"gzip":            cpGzip(t, []byte(strings.Repeat("tgs lottie json ", 4000))),
		"7z empty":        cp7z(nil),
		"7z plain header": cp7z([]byte{0x01, 0x04, 0x06, 0x00, 0x01, 0x09, 0x10, 0x00}),
		"rar5":            cpRAR5(1),
		"rar4":            cpRAR4(0),
		"text":            []byte(strings.Repeat("Hello, this is an ordinary text document. ", 1000)),
		"small random":    cpRandom(t, 900),
		"pdf":             []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\n" + strings.Repeat("x", 3000) + "\ntrailer\n%%EOF\n"),
		"jpeg+mp4 motion": cat(cpJPEG(t, false), []byte("MotionPhoto_Data"), cpFixture(t, "video.mp4")),
		"jpeg+jpeg (MPO)": cat(cpJPEG(t, false), cpJPEG(t, false)),
		"jpeg+zero pad":   cat(cpJPEG(t, false), make([]byte, 4096)),
		"wav+id3":         cat(cpFixture(t, "audio.wav"), []byte("ID3\x04\x00\x00\x00\x00\x00\x0a"), make([]byte, 1100)),
		"pgp public key":  []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\n" + strings.Repeat("mQINBF", 500)),
	}
	for _, name := range []string{"video.mp4", "video_moov_end.mp4", "video_frag.mp4", "video.webm", "video.mkv",
		"video.mov", "voice.ogg", "audio.mp3", "audio_noid3.mp3", "audio.m4a", "audio.aac", "audio.wav",
		"audio.flac", "image.webp"} {
		cases[name] = cpFixture(t, name)
	}
	for _, extra := range []string{"../../../seed/freeze/freeze.tgs", "../../giftpack/giftpacktest/phone-pack.zip"} {
		if data, err := os.ReadFile(extra); err == nil {
			cases[filepath.Base(extra)] = data
		}
	}
	for name, data := range cases {
		if in := inspectBytes(data); in.Kind != inspectOK {
			t.Errorf("%s: %+v, want ok", name, in)
		}
	}
}

// Every evasion we know of must be caught.
func TestInspectContentCatchesEvasions(t *testing.T) {
	cipher := cpRandom(t, 200<<10)
	cases := []struct {
		name string
		data []byte
		want inspectKind
	}{
		{"alice container", cat([]byte("ALICEU01\x01\x01\x00\x04"), cipher), inspectBlockedFormat},
		{"tiny alice", []byte("ALICEU01\x01\x01"), inspectBlockedFormat},
		{"raw ciphertext", cipher, inspectOpaque},
		{"small raw ciphertext", cpRandom(t, 2048), inspectOpaque},
		{"fake zip header", cat([]byte{'P', 'K', 0x03, 0x04}, cipher), inspectInvalidStructure},
		{"fake mp4 header", cat([]byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 2, 0, 'i', 's', 'o', 'm', 'm', 'p', '4', '1'}, cipher), inspectInvalidStructure},
		{"fake mp4 ftyp+mdat no moov", cat([]byte{0, 0, 0, 0x10, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 2, 0}, []byte{0, 0, 0, 0}, []byte("mdat"), cipher), inspectInvalidStructure},
		{"fake jpeg header", cat([]byte{0xFF, 0xD8, 0xFF, 0xE0}, cipher), inspectInvalidStructure},
		{"fake png header", cat([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, cipher), inspectInvalidStructure},
		{"fake gzip header", cat([]byte{0x1F, 0x8B, 0x08, 0, 0, 0, 0, 0, 0, 0xFF}, cipher), inspectInvalidStructure},
		{"fake 7z header", cat([]byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C, 0, 4}, cipher), inspectInvalidStructure},
		{"fake rar5 header", cat([]byte("Rar!\x1A\x07\x01\x00"), cipher), inspectInvalidStructure},
		{"fake ogg header", cat([]byte("OggS\x00\x02"), cipher), inspectInvalidStructure},
		{"fake webm header", cat([]byte{0x1A, 0x45, 0xDF, 0xA3}, cipher), inspectInvalidStructure},
		{"fake flac header", cat([]byte("fLaC"), cipher), inspectOpaque},
		{"fake mp3 sync", cat([]byte{0xFF, 0xFB, 0x90, 0x00}, cipher), inspectOpaque},
		{"fake id3 tag", cat([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), cipher), inspectOpaque},
		{"fake pdf header", cat([]byte("%PDF-1.7\n"), cipher), inspectOpaque},
		{"jpeg + ciphertext", cat(cpJPEG(t, false), cipher), inspectTrailingData},
		{"png + ciphertext", cat(cpPNG(t), cipher), inspectTrailingData},
		{"jpeg + zip", cat(cpJPEG(t, false), cpZIP(t, false)), inspectTrailingData},
		{"mp4 + ciphertext", cat(cpFixture(t, "video.mp4"), cipher), inspectTrailingData},
		{"webm + ciphertext", cat(cpFixture(t, "video.webm"), cipher), inspectTrailingData},
		{"ogg + ciphertext", cat(cpFixture(t, "voice.ogg"), cipher), inspectTrailingData},
		{"pdf + ciphertext", cat([]byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n%%EOF\n"), cipher), inspectTrailingData},
		{"encrypted zip", cpZIP(t, true), inspectEncryptedContainer},
		{"encrypted 7z", cp7z([]byte{0x17, 0x06, 0xF1, 0x07, 0x01, 0x00}), inspectEncryptedContainer},
		{"encrypted rar5", cpRAR5(4), inspectEncryptedContainer},
		{"encrypted rar4", cpRAR4(0x0080), inspectEncryptedContainer},
		{"openssl enc", cat([]byte("Salted__"), cipher), inspectEncryptedContainer},
		{"age", cat([]byte("age-encryption.org/v1\n"), cipher), inspectEncryptedContainer},
		{"pgp armored message", []byte("-----BEGIN PGP MESSAGE-----\nhQEMA..."), inspectEncryptedContainer},
		{"pgp binary message", cat([]byte{0x85, 0x01, 0x0C, 0x03}, cipher), inspectEncryptedContainer},
	}
	for _, c := range cases {
		if in := inspectBytes(c.data); in.Kind != c.want {
			t.Errorf("%s: %+v, want %s", c.name, in, c.want)
		}
	}
}

func TestSanitizeImageBytesStripsTrailers(t *testing.T) {
	svc := &Service{log: zap.NewNop()}
	img := cpJPEG(t, false)
	for _, trailer := range [][]byte{cpRandom(t, 5000), cpZIP(t, false), make([]byte, 10)} {
		got := svc.sanitizeImageBytes(t.Context(), cat(img, trailer), 1, "photo")
		if !bytes.Equal(got, img) {
			t.Fatalf("trailer of %d bytes not stripped (got %d, want %d)", len(trailer), len(got), len(img))
		}
	}
	p := cpPNG(t)
	if got := svc.sanitizeImageBytes(t.Context(), cat(p, cpRandom(t, 3000)), 1, "photo"); !bytes.Equal(got, p) {
		t.Fatal("png trailer not stripped")
	}
	if got := svc.sanitizeImageBytes(t.Context(), img, 1, "photo"); !bytes.Equal(got, img) {
		t.Fatal("clean jpeg modified")
	}
}

func TestPolicyDecisions(t *testing.T) {
	block := &Service{}
	if b, r := block.decide(inspection{Kind: inspectOpaque}); !b || !r {
		t.Fatal("default policy must block opaque")
	}
	if b, r := block.decide(inspection{Kind: inspectEncryptedContainer}); b || !r {
		t.Fatal("default encrypted-container policy must flag, not block")
	}
	logOnly := &Service{opaqueUploadPolicy: OpaqueUploadPolicyLog}
	if b, r := logOnly.decide(inspection{Kind: inspectTrailingData}); b || !r {
		t.Fatal("log policy must not block")
	}
	if b, _ := logOnly.decide(inspection{Kind: inspectBlockedFormat}); !b {
		t.Fatal("known abuse container must be blocked in log mode")
	}
	encBlock := &Service{encryptedContainerPolicy: EncryptedContainerPolicyBlock}
	if b, _ := encBlock.decide(inspection{Kind: inspectEncryptedContainer}); !b {
		t.Fatal("encrypted block policy must block")
	}
	off := &Service{opaqueUploadPolicy: OpaqueUploadPolicyOff}
	if b, r := off.decide(inspection{Kind: inspectBlockedFormat}); b || r {
		t.Fatal("off must disable")
	}
}

func TestRandomnessStatistics(t *testing.T) {
	for _, n := range []int{1024, 4096, 65536} {
		if !looksRandom(cpRandom(t, n)) {
			t.Fatalf("random %d bytes not detected", n)
		}
	}
	// Compressed media is high-entropy but not uniform.
	if looksRandom(cpFixture(t, "audio.flac")) {
		t.Fatal("flac looks random")
	}
	if looksRandom([]byte(strings.Repeat("abcdefghijklmnopqrstuvwxyz0123456789", 200))) {
		t.Fatal("text looks random")
	}
}

// Cases found by the dry-run scan of real prod blobs plus formats that must
// never be judged by statistics.
func TestInspectContentRealWorldEdgeCases(t *testing.T) {
	cipher := cpRandom(t, 300<<10)

	// MP4 whose mdat size field is wrong (seen on 12 real user videos): the
	// box chain breaks but the body is codec data -> tolerated.
	sloppy := append([]byte(nil), cpFixture(t, "video_moov_end.mp4")...)
	if idx := bytes.Index(sloppy, []byte("mdat")); idx >= 4 {
		binary.BigEndian.PutUint32(sloppy[idx-4:], 16)
	}
	// .deb (ar archive) with a compressed member: random-looking body.
	deb := cat([]byte("!<arch>\n"), []byte("debian-binary   1700000000  0     0     100644  4         `\n"), []byte("2.0\n"),
		[]byte("data.tar.xz     1700000000  0     0     100644  307200    `\n"), cipher)
	// tar with valid checksum
	tarHdr := make([]byte, 512)
	copy(tarHdr, "file.txt")
	copy(tarHdr[257:], "ustar\x0000")
	for i := 148; i < 156; i++ {
		tarHdr[i] = ' '
	}
	var sum int
	for _, c := range tarHdr {
		sum += int(c)
	}
	copy(tarHdr[148:], []byte(fmt.Sprintf("%06o\x00 ", sum)))
	// PE installer with a compressed (random) payload.
	pe := make([]byte, 0x200)
	copy(pe, "MZ")
	binary.LittleEndian.PutUint32(pe[0x3C:], 0x80)
	copy(pe[0x80:], "PE\x00\x00")
	installer := cat(pe, cipher)

	ok := map[string][]byte{"sloppy mp4": sloppy, "deb": deb, "tar": cat(tarHdr, cipher), "pe installer": installer}
	for name, data := range ok {
		if in := inspectBytes(data); in.Kind != inspectOK {
			t.Errorf("%s: %+v, want ok", name, in)
		}
	}
	bad := []struct {
		name string
		data []byte
		want inspectKind
	}{
		{"fake MZ", cat([]byte("MZ"), cipher), inspectOpaque},
		{"fake ar", cat([]byte("!<arch>\n"), cipher), inspectOpaque},
		{"keepass db", cat([]byte{0x03, 0xD9, 0xA2, 0x9A, 0x67, 0xFB, 0x4B, 0xB5}, cipher), inspectEncryptedContainer},
		{"tdesktop tdf", cat([]byte("TDF$"), cipher), inspectEncryptedContainer},
	}
	for _, c := range bad {
		if in := inspectBytes(c.data); in.Kind != c.want {
			t.Errorf("%s: %+v, want %s", c.name, in, c.want)
		}
	}
}
