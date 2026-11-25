package files

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"strings"

	"golang.org/x/image/webp"
)

// This file is the format-aware half of the upload content policy (see
// content_policy.go). inspectContent decides whether a file is what it claims
// to be. It is deliberately conservative: a file is only reported as abusive
// when the evidence is unambiguous (a known abuse container, random bytes
// with no file signature, a container whose own checksums/structure are
// broken, or random-looking data appended after the real end of an image or
// archive). Formats it cannot validate fall back to a statistical check that
// only fires on data indistinguishable from ciphertext.
//
// Content is never logged or stored by the inspector; it only reads bounded
// regions (head, a few samples, container directories).

// contentSource is random access to the bytes being inspected.
type contentSource interface {
	io.ReaderAt
	Size() int64
}

type inspectKind string

const (
	inspectOK                 inspectKind = ""
	inspectBlockedFormat      inspectKind = "blocked_format"
	inspectOpaque             inspectKind = "opaque"
	inspectInvalidStructure   inspectKind = "invalid_structure"
	inspectTrailingData       inspectKind = "trailing_data"
	inspectEncryptedContainer inspectKind = "encrypted_container"
)

type inspection struct {
	Kind   inspectKind
	Format string
	Detail string
}

const (
	// sniffBytes bounds every sampled region.
	sniffBytes = 64 << 10
	// minOpaqueBytes: below this nothing is called "random"; the statistics
	// are too noisy and tiny blobs cannot carry meaningful payloads.
	minOpaqueBytes = 1 << 10
	// maxFullScanBytes bounds formats whose end has to be found by walking the
	// whole file (JPEG entropy-coded data, Ogg pages); larger files keep only
	// the cheaper header checks.
	maxFullScanBytes = 64 << 20
	// minTrailerBytes: appended data smaller than this is tolerated (padding,
	// tool footers) without analysis.
	minTrailerBytes = 1 << 10
	// randomChiSquareMax: for uniformly random bytes the chi-square statistic
	// over 256 byte values follows chi2(255) (mean 255, sd ~22.6). Real
	// codec/structured data is far less uniform. A sample at or below this
	// value (≈ +4.2 sd) is "indistinguishable from ciphertext".
	randomChiSquareMax = 350.0
)

var errShortRead = errors.New("short read")

// readRegion returns up to n bytes at off (clamped to the source size).
func readRegion(src contentSource, off int64, n int) ([]byte, error) {
	size := src.Size()
	if off < 0 || off >= size || n <= 0 {
		return nil, nil
	}
	if rem := size - off; int64(n) > rem {
		n = int(rem)
	}
	buf := make([]byte, n)
	got, err := src.ReadAt(buf, off)
	if got == n {
		return buf, nil
	}
	if err == nil || errors.Is(err, io.EOF) {
		return buf[:got], errShortRead
	}
	return buf[:got], err
}

// byteStats returns Shannon entropy (bits/byte) and the chi-square statistic
// against a uniform byte distribution.
func byteStats(data []byte) (entropy, chi2 float64) {
	if len(data) == 0 {
		return 0, math.Inf(1)
	}
	var counts [256]int
	for _, b := range data {
		counts[b]++
	}
	n := float64(len(data))
	expected := n / 256
	for _, c := range counts {
		if c > 0 {
			p := float64(c) / n
			entropy -= p * math.Log2(p)
		}
		d := float64(c) - expected
		chi2 += d * d / expected
	}
	return entropy, chi2
}

// randomEntropyThreshold returns the bits/byte value above which a sample of n
// bytes is consistent with uniformly random data (plug-in estimator bias is
// about 255/(2n ln 2) bits).
func randomEntropyThreshold(n int) float64 {
	bias := 255.0 / (2 * float64(n) * math.Ln2)
	return 8 - 2*bias - 0.01
}

// looksRandom reports whether a sample is statistically indistinguishable
// from ciphertext: both entropy and byte-distribution uniformity must match.
func looksRandom(sample []byte) bool {
	if len(sample) < minOpaqueBytes {
		return false
	}
	h, chi2 := byteStats(sample)
	return h >= randomEntropyThreshold(len(sample)) && chi2 <= randomChiSquareMax
}

// samplesLookRandom checks several regions spread over the file (skipping
// the first headerSkip bytes); all of them must look random.
func samplesLookRandom(src contentSource, headerSkip int64) bool {
	size := src.Size()
	if size-headerSkip < minOpaqueBytes {
		return false
	}
	span := size - headerSkip
	offsets := []int64{headerSkip}
	if span > 4*sniffBytes {
		offsets = append(offsets, headerSkip+span/3, headerSkip+2*span/3, size-sniffBytes)
	}
	for _, off := range offsets {
		sample, err := readRegion(src, off, sniffBytes)
		if err != nil && !errors.Is(err, errShortRead) {
			return false
		}
		if !looksRandom(sample) {
			return false
		}
	}
	return true
}

// blockedContainerMagics are signatures of client-side encryption wrappers
// seen in abuse cases.
var blockedContainerMagics = [][]byte{
	[]byte("ALICEU"), // "Alice" modded client: ALICEU01 + version + chunk index + ciphertext
}

// format identifiers
const (
	fmtZIP      = "zip"
	fmt7z       = "7z"
	fmtRAR5     = "rar5"
	fmtRAR4     = "rar4"
	fmtGzip     = "gzip"
	fmtBzip2    = "bzip2"
	fmtXZ       = "xz"
	fmtZstd     = "zstd"
	fmtLZ4      = "lz4"
	fmtJPEG     = "jpeg"
	fmtPNG      = "png"
	fmtGIF      = "gif"
	fmtRIFF     = "riff"
	fmtISOBMFF  = "isobmff"
	fmtEBML     = "ebml"
	fmtOgg      = "ogg"
	fmtID3      = "id3"
	fmtMPEGA    = "mpeg_audio"
	fmtPDF      = "pdf"
	fmtOpenSSL  = "openssl_enc"
	fmtAge      = "age"
	fmtPGP      = "pgp"
	fmtPGPArmor = "pgp_armored_message"
	fmtText     = "text_marker"
	fmtWeak     = "weak_signature"
	fmtKeePass  = "keepass"
	fmtTDF      = "tdesktop_tdf"
	fmtTar      = "tar"
	fmtISO9660  = "iso9660"
)

// weakFormats are recognized by a short signature plus a cheap header check.
// Executables, installers and fonts legitimately contain compressed
// (random-looking) payloads, so they are never judged by statistics; only a
// failed header check makes the file "unidentified" (and then a random body
// is reported as opaque).
type weakFormat struct {
	name  string
	sig   []byte
	check func(head []byte, size int64) bool
}

func u16le(b []byte, off int) int   { return int(binary.LittleEndian.Uint16(b[off:])) }
func u32le(b []byte, off int) int64 { return int64(binary.LittleEndian.Uint32(b[off:])) }
func u16be(b []byte, off int) int   { return int(binary.BigEndian.Uint16(b[off:])) }
func u32be(b []byte, off int) int64 { return int64(binary.BigEndian.Uint32(b[off:])) }

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

var weakFormats = []weakFormat{
	{"pe", []byte("MZ"), func(h []byte, _ int64) bool {
		if len(h) < 0x40 {
			return false
		}
		e := u32le(h, 0x3C)
		return e >= 0x40 && e+4 <= int64(len(h)) && bytes.Equal(h[e:e+4], []byte("PE\x00\x00"))
	}},
	{"elf", []byte{0x7F, 'E', 'L', 'F'}, func(h []byte, _ int64) bool {
		return len(h) > 6 && (h[4] == 1 || h[4] == 2) && (h[5] == 1 || h[5] == 2) && h[6] == 1
	}},
	{"java_class_or_fat_macho", []byte{0xCA, 0xFE, 0xBA, 0xBE}, func(h []byte, _ int64) bool {
		if len(h) < 8 {
			return false
		}
		major := u16be(h, 6)
		return u32be(h, 4) < 32 || (major >= 45 && major <= 90)
	}},
	{"macho64", []byte{0xCF, 0xFA, 0xED, 0xFE}, func(h []byte, _ int64) bool { return len(h) >= 32 }},
	{"macho32", []byte{0xCE, 0xFA, 0xED, 0xFE}, func(h []byte, _ int64) bool { return len(h) >= 28 }},
	{"dex", []byte("dex\n"), func(h []byte, size int64) bool {
		return len(h) >= 0x24 && h[7] == 0 && u32le(h, 0x20) == size
	}},
	{"cab", []byte("MSCF"), func(h []byte, size int64) bool {
		return len(h) >= 12 && u32le(h, 4) == 0 && u32le(h, 8) <= size && u32le(h, 8) > 36
	}},
	{"installshield", []byte("ISc("), func([]byte, int64) bool { return true }},
	{"ole", []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, func(h []byte, _ int64) bool {
		return len(h) >= 0x20 && h[0x1C] == 0xFE && h[0x1D] == 0xFF && (u16le(h, 0x1E) == 9 || u16le(h, 0x1E) == 12)
	}},
	{"woff", []byte("wOFF"), func(h []byte, size int64) bool { return len(h) >= 12 && u32be(h, 8) == size }},
	{"woff2", []byte("wOF2"), func(h []byte, size int64) bool { return len(h) >= 12 && u32be(h, 8) == size }},
	{"truetype", []byte{0x00, 0x01, 0x00, 0x00}, func(h []byte, _ int64) bool {
		return len(h) >= 12 && u16be(h, 4) >= 1 && u16be(h, 4) <= 100
	}},
	{"opentype", []byte("OTTO"), func(h []byte, _ int64) bool {
		return len(h) >= 12 && u16be(h, 4) >= 1 && u16be(h, 4) <= 100
	}},
	{"tiff_le", []byte("II*\x00"), func(h []byte, size int64) bool { return len(h) >= 8 && u32le(h, 4) >= 8 && u32le(h, 4) < size }},
	{"tiff_be", []byte("MM\x00*"), func(h []byte, size int64) bool { return len(h) >= 8 && u32be(h, 4) >= 8 && u32be(h, 4) < size }},
	{"psd", []byte("8BPS"), func(h []byte, _ int64) bool {
		return len(h) >= 12 && (u16be(h, 4) == 1 || u16be(h, 4) == 2) && allZero(h[6:12])
	}},
	{"bmp", []byte("BM"), func(h []byte, size int64) bool {
		return len(h) >= 14 && (u32le(h, 2) == size || u32le(h, 2) == 0) && allZero(h[6:10])
	}},
	{"flac", []byte("fLaC"), func(h []byte, _ int64) bool {
		return len(h) >= 8 && h[4]&0x7F == 0 && int(h[5])<<16|int(h[6])<<8|int(h[7]) == 34
	}},
	{"asf", []byte{0x30, 0x26, 0xB2, 0x75, 0x8E, 0x66, 0xCF, 0x11, 0xA6, 0xD9, 0x00, 0xAA, 0x00, 0x62, 0xCE, 0x6C}, func([]byte, int64) bool { return true }},
	{"flv", []byte("FLV"), func(h []byte, _ int64) bool {
		return len(h) >= 13 && h[3] == 1 && h[4]&0xFA == 0 && u32be(h, 5) == 9 && u32be(h, 9) == 0
	}},
	{"amr", []byte("#!AMR"), func([]byte, int64) bool { return true }},
	{"caf", []byte("caff"), func(h []byte, _ int64) bool { return len(h) >= 8 && u16be(h, 4) == 1 }},
	{"iff", []byte("FORM"), func(h []byte, size int64) bool {
		return len(h) >= 12 && printableFourCC(h[8:12]) && u32be(h, 4)+8 <= size+1
	}},
	{"jpeg2000", []byte{0x00, 0x00, 0x00, 0x0C, 'j', 'P', ' ', ' '}, func([]byte, int64) bool { return true }},
	{"jpegxl", []byte{0x00, 0x00, 0x00, 0x0C, 'J', 'X', 'L', ' '}, func([]byte, int64) bool { return true }},
	{"jpegxl_codestream", []byte{0xFF, 0x0A}, func(h []byte, _ int64) bool { return len(h) >= 16 }},
	{"mpeg_ps", []byte{0x00, 0x00, 0x01, 0xBA}, func([]byte, int64) bool { return true }},
	{"mpeg_video", []byte{0x00, 0x00, 0x01, 0xB3}, func([]byte, int64) bool { return true }},
	{"ar", []byte("!<arch>\n"), func(h []byte, _ int64) bool {
		// First member header: 60 bytes ending in "`\n", decimal size field.
		if len(h) < 68 || h[66] != '`' || h[67] != '\n' {
			return false
		}
		for _, c := range bytes.TrimRight(h[56:66], " ") {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}},
}

func matchWeakFormat(head []byte) *weakFormat {
	for i := range weakFormats {
		if bytes.HasPrefix(head, weakFormats[i].sig) {
			return &weakFormats[i]
		}
	}
	return nil
}

// tarHeaderOK verifies a ustar header checksum.
func tarHeaderOK(h []byte) bool {
	if len(h) < 512 || !bytes.Equal(h[257:262], []byte("ustar")) {
		return false
	}
	var sum int64
	for i := 0; i < 512; i++ {
		if i >= 148 && i < 156 {
			sum += ' '
		} else {
			sum += int64(h[i])
		}
	}
	field := strings.TrimSpace(strings.TrimRight(string(h[148:156]), "\x00 "))
	var want int64
	for _, c := range field {
		if c < '0' || c > '7' {
			return false
		}
		want = want*8 + int64(c-'0')
	}
	return want == sum
}

// textSignatures are plain-text formats (never random; nothing to check).
var textSignatures = [][]byte{
	[]byte("SQLite format 3"), []byte("-----BEGIN"), []byte("{\\rtf"),
}

func detectFormat(head []byte) string {
	switch {
	case bytes.HasPrefix(head, []byte{'P', 'K', 0x03, 0x04}), bytes.HasPrefix(head, []byte{'P', 'K', 0x05, 0x06}):
		return fmtZIP
	case bytes.HasPrefix(head, []byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C}):
		return fmt7z
	case bytes.HasPrefix(head, []byte("Rar!\x1A\x07\x01\x00")):
		return fmtRAR5
	case bytes.HasPrefix(head, []byte("Rar!\x1A\x07\x00")):
		return fmtRAR4
	case bytes.HasPrefix(head, []byte{0x1F, 0x8B}):
		return fmtGzip
	case len(head) >= 4 && bytes.HasPrefix(head, []byte("BZh")) && head[3] >= '1' && head[3] <= '9':
		return fmtBzip2
	case bytes.HasPrefix(head, []byte{0xFD, '7', 'z', 'X', 'Z', 0x00}):
		return fmtXZ
	case bytes.HasPrefix(head, []byte{0x28, 0xB5, 0x2F, 0xFD}):
		return fmtZstd
	case bytes.HasPrefix(head, []byte{0x04, 0x22, 0x4D, 0x18}):
		return fmtLZ4
	case bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF}):
		return fmtJPEG
	case bytes.HasPrefix(head, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return fmtPNG
	case bytes.HasPrefix(head, []byte("GIF87a")), bytes.HasPrefix(head, []byte("GIF89a")):
		return fmtGIF
	case bytes.HasPrefix(head, []byte("RIFF")):
		return fmtRIFF
	case len(head) >= 12 && isISOBMFFStart(head):
		return fmtISOBMFF
	case bytes.HasPrefix(head, []byte{0x1A, 0x45, 0xDF, 0xA3}):
		return fmtEBML
	case bytes.HasPrefix(head, []byte("OggS")):
		return fmtOgg
	case bytes.HasPrefix(head, []byte("ID3")):
		return fmtID3
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return fmtPDF
	case bytes.HasPrefix(head, []byte("Salted__")):
		return fmtOpenSSL
	case bytes.HasPrefix(head, []byte("age-encryption.org/")):
		return fmtAge
	case bytes.HasPrefix(head, []byte("-----BEGIN PGP MESSAGE-----")):
		return fmtPGPArmor
	case bytes.HasPrefix(head, []byte{0x03, 0xD9, 0xA2, 0x9A}):
		return fmtKeePass
	case bytes.HasPrefix(head, []byte("TDF$")):
		return fmtTDF
	case tarHeaderOK(head):
		return fmtTar
	case len(head) >= 32774 && bytes.Equal(head[32769:32774], []byte("CD001")):
		return fmtISO9660
	}
	for _, sig := range textSignatures {
		if bytes.HasPrefix(head, sig) {
			return fmtText
		}
	}
	if isPGPBinaryMessage(head) {
		return fmtPGP
	}
	if len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0 {
		return fmtMPEGA
	}
	if matchWeakFormat(head) != nil {
		return fmtWeak
	}
	return ""
}

func isISOBMFFStart(head []byte) bool {
	switch string(head[4:8]) {
	case "ftyp", "moov", "mdat", "free", "wide", "skip", "styp", "pnot":
		return true
	}
	return false
}

// isPGPBinaryMessage recognizes an OpenPGP binary message that starts with a
// public-key (tag 1) or symmetric-key (tag 3) encrypted session key packet.
func isPGPBinaryMessage(head []byte) bool {
	if len(head) < 8 || head[0]&0x80 == 0 {
		return false
	}
	var tag byte
	var lenBytes int
	if head[0]&0x40 != 0 { // new format
		tag = head[0] & 0x3F
		switch l := head[1]; {
		case l < 192:
			lenBytes = 1
		case l < 224:
			lenBytes = 2
		case l == 255:
			lenBytes = 5
		default:
			return false
		}
	} else { // old format
		tag = (head[0] >> 2) & 0x0F
		switch head[0] & 0x03 {
		case 0:
			lenBytes = 1
		case 1:
			lenBytes = 2
		case 2:
			lenBytes = 4
		default:
			return false
		}
	}
	vpos := 1 + lenBytes
	if vpos >= len(head) {
		return false
	}
	v := head[vpos]
	switch tag {
	case 1:
		return v == 3 || v == 6
	case 3:
		return v == 4 || v == 5 || v == 6
	}
	return false
}

// inspectContent runs the full format-aware inspection.
func inspectContent(src contentSource) inspection {
	return inspectContentDepth(src, 0)
}

func inspectContentDepth(src contentSource, depth int) inspection {
	size := src.Size()
	if size <= 0 {
		return inspection{}
	}
	head, err := readRegion(src, 0, sniffBytes)
	if err != nil && !errors.Is(err, errShortRead) {
		return inspection{} // I/O problems are reported by the normal path
	}
	for _, magic := range blockedContainerMagics {
		if bytes.HasPrefix(head, magic) {
			return inspection{Kind: inspectBlockedFormat, Format: string(magic)}
		}
	}
	format := detectFormat(head)
	switch format {
	case "":
		if size >= minOpaqueBytes && looksRandom(head) {
			return inspection{Kind: inspectOpaque}
		}
		return inspection{}
	case fmtText:
		return inspection{Format: format}
	case fmtOpenSSL, fmtAge, fmtPGP, fmtPGPArmor, fmtKeePass, fmtTDF:
		return inspection{Kind: inspectEncryptedContainer, Format: format}
	case fmtTar, fmtISO9660:
		return inspection{Format: format}
	case fmtZIP:
		return withFormat(inspectZIP(src), format)
	case fmt7z:
		return withFormat(inspect7z(src, head), format)
	case fmtRAR5:
		return withFormat(inspectRAR5(head), format)
	case fmtRAR4:
		return withFormat(inspectRAR4(head), format)
	case fmtGzip:
		return withFormat(inspectGzip(head), format)
	case fmtBzip2:
		return withFormat(inspectBzip2(head), format)
	case fmtXZ:
		return withFormat(inspectXZ(head), format)
	case fmtZstd, fmtLZ4:
		// Header-only formats without a header checksum we can verify cheaply.
		return inspection{Format: format}
	case fmtJPEG:
		return withFormat(inspectJPEG(src, head, depth), format)
	case fmtPNG:
		return withFormat(inspectPNG(src, head, depth), format)
	case fmtGIF:
		return withFormat(inspectImageConfig(head, gif.DecodeConfig), format)
	case fmtRIFF:
		return withFormat(inspectRIFF(src, head, depth), format)
	case fmtISOBMFF:
		return withFormat(inspectISOBMFF(src, depth), format)
	case fmtEBML:
		return withFormat(inspectEBML(src, depth), format)
	case fmtOgg:
		return withFormat(inspectOgg(src, depth), format)
	case fmtID3:
		return withFormat(inspectID3(src, head), format)
	case fmtMPEGA:
		if mpegAudioChainAt(head, 0, 3) {
			return inspection{Format: format}
		}
		// A two-byte frame sync is a weak signature: without a frame chain
		// treat the file as unidentified.
		if size >= minOpaqueBytes && looksRandom(head) {
			return inspection{Kind: inspectOpaque}
		}
		return inspection{}
	case fmtPDF:
		return withFormat(inspectPDF(src, head, depth), format)
	case fmtWeak:
		wf := matchWeakFormat(head)
		if wf.check(head, size) {
			return inspection{Format: wf.name}
		}
		// Signature without a plausible header: judge it as unidentified.
		if size >= minOpaqueBytes && samplesLookRandom(src, int64(len(wf.sig))) {
			return inspection{Kind: inspectOpaque, Format: wf.name, Detail: "random body behind fake " + wf.name + " signature"}
		}
		return inspection{Format: wf.name, Detail: "header check failed"}
	}
	return inspection{Format: format}
}

func withFormat(in inspection, format string) inspection {
	if in.Format == "" {
		in.Format = format
	}
	return in
}

func invalid(detail string) inspection {
	return inspection{Kind: inspectInvalidStructure, Detail: detail}
}

// mediaInvalid handles a broken audio/video/document container. Real files
// with sloppy containers exist in the wild (players tolerate them), so a
// broken structure only counts as abuse when the body is also
// indistinguishable from ciphertext; codec data never is.
func mediaInvalid(src contentSource, skip int64, detail string) inspection {
	if samplesLookRandom(src, skip) {
		return invalid(detail + " (random body)")
	}
	return inspection{Detail: "tolerated: " + detail}
}

// analyzeTrailer decides about bytes appended after the real end of a file
// (end = offset of the first byte after the format's own end). Small, empty,
// zero-padded or recognizable trailers (motion-photo MP4, a second JPEG, ...)
// are fine; a trailer that is random-looking or an invalid known format is
// reported as trailing data.
func analyzeTrailer(src contentSource, end int64, depth int) inspection {
	size := src.Size()
	if end <= 0 || end >= size {
		return inspection{}
	}
	trailerLen := size - end
	if trailerLen < minTrailerBytes {
		return inspection{}
	}
	sub := io.NewSectionReader(src, end, trailerLen)
	head, err := readRegion(sub, 0, sniffBytes)
	if err != nil && !errors.Is(err, errShortRead) {
		return inspection{}
	}
	// Samsung/Google motion photos put an MP4 (possibly after a short marker
	// such as "MotionPhoto_Data") behind the JPEG.
	if idx := bytes.Index(head[:min(len(head), 256)], []byte("ftyp")); idx >= 4 {
		return inspection{}
	}
	if depth < 2 {
		inner := inspectContentDepth(sub, depth+1)
		switch inner.Kind {
		case inspectOK:
			switch inner.Format {
			case "":
				// unidentified: decided by the randomness check below
			case fmtJPEG, fmtISOBMFF, fmtText, fmtID3:
				// MPO second image, motion-photo video, text footers, an
				// ID3 tag appended to WAV/other audio
				return inspection{}
			default:
				// A complete archive/media file glued behind another file is
				// the classic way to smuggle content inside an innocent one.
				return inspection{Kind: inspectTrailingData, Format: inner.Format, Detail: "appended " + inner.Format}
			}
		case inspectEncryptedContainer:
			return inspection{Kind: inspectEncryptedContainer, Format: inner.Format, Detail: "appended"}
		default:
			return inspection{Kind: inspectTrailingData, Detail: "appended " + string(inner.Kind)}
		}
	}
	if samplesLookRandom(sub, 0) {
		return inspection{Kind: inspectTrailingData, Detail: "random data appended"}
	}
	return inspection{}
}

// --- archives -------------------------------------------------------------

func inspectZIP(src contentSource) inspection {
	r, err := zip.NewReader(src, src.Size())
	if err != nil {
		if errors.Is(err, zip.ErrInsecurePath) && r != nil {
			// Names are irrelevant here; the directory itself parsed.
		} else {
			return invalid("zip directory: " + err.Error())
		}
	}
	for _, f := range r.File {
		if f.Flags&0x1 != 0 {
			return inspection{Kind: inspectEncryptedContainer, Detail: "password-protected entries"}
		}
	}
	return inspection{}
}

func inspect7z(src contentSource, head []byte) inspection {
	if len(head) < 32 {
		return invalid("7z start header truncated")
	}
	if crc32.ChecksumIEEE(head[12:32]) != binary.LittleEndian.Uint32(head[8:12]) {
		return invalid("7z start header crc")
	}
	nextOff := binary.LittleEndian.Uint64(head[12:20])
	nextSize := binary.LittleEndian.Uint64(head[20:28])
	nextCRC := binary.LittleEndian.Uint32(head[28:32])
	size := uint64(src.Size())
	if nextSize == 0 && nextOff == 0 {
		return inspection{} // empty archive
	}
	if nextOff > size || nextSize > size || 32+nextOff+nextSize > size {
		return invalid("7z next header out of range")
	}
	if nextSize > 16<<20 {
		return inspection{}
	}
	hdr, err := readRegion(src, int64(32+nextOff), int(nextSize))
	if err != nil {
		return inspection{}
	}
	if crc32.ChecksumIEEE(hdr) != nextCRC {
		return invalid("7z next header crc")
	}
	if bytes.Contains(hdr, []byte{0x06, 0xF1, 0x07, 0x01}) {
		return inspection{Kind: inspectEncryptedContainer, Detail: "AES coder"}
	}
	return inspection{}
}

// readVint reads a RAR5 variable-length integer.
func readVint(b []byte) (uint64, int, bool) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7F) << (7 * uint(i))
		if b[i]&0x80 == 0 {
			return v, i + 1, true
		}
	}
	return 0, 0, false
}

func inspectRAR5(head []byte) inspection {
	p := head[8:]
	if len(p) < 6 {
		return invalid("rar5 header truncated")
	}
	crc := binary.LittleEndian.Uint32(p[:4])
	hsize, n, ok := readVint(p[4:])
	if !ok || hsize == 0 || hsize > 2<<20 {
		return invalid("rar5 header size")
	}
	end := 4 + n + int(hsize)
	if end > len(p) {
		return inspection{} // header beyond sample: cannot verify, do not guess
	}
	if crc32.ChecksumIEEE(p[4:end]) != crc {
		return invalid("rar5 header crc")
	}
	htype, _, ok := readVint(p[4+n : end])
	if !ok {
		return invalid("rar5 header type")
	}
	if htype == 4 {
		return inspection{Kind: inspectEncryptedContainer, Detail: "encrypted headers"}
	}
	if htype != 1 {
		return invalid("rar5 first header is not main")
	}
	return inspection{}
}

func inspectRAR4(head []byte) inspection {
	const off = 7
	if len(head) < off+7 {
		return invalid("rar4 header truncated")
	}
	crc := binary.LittleEndian.Uint16(head[off:])
	htype := head[off+2]
	flags := binary.LittleEndian.Uint16(head[off+3:])
	hsize := int(binary.LittleEndian.Uint16(head[off+5:]))
	if htype != 0x73 || hsize < 7 {
		return invalid("rar4 main header")
	}
	if off+hsize > len(head) {
		return inspection{}
	}
	if uint16(crc32.ChecksumIEEE(head[off+2:off+hsize])) != crc {
		return invalid("rar4 main header crc")
	}
	if flags&0x0080 != 0 {
		return inspection{Kind: inspectEncryptedContainer, Detail: "encrypted headers"}
	}
	fo := off + hsize
	if fo+7 <= len(head) && head[fo+2] == 0x74 {
		if binary.LittleEndian.Uint16(head[fo+3:])&0x0004 != 0 {
			return inspection{Kind: inspectEncryptedContainer, Detail: "encrypted file"}
		}
	}
	return inspection{}
}

func inspectGzip(head []byte) inspection {
	zr, err := gzip.NewReader(bytes.NewReader(head))
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return inspection{}
		}
		return invalid("gzip header")
	}
	// Only the first member matters; trailing padding after it is normal.
	zr.Multistream(false)
	_, err = io.CopyN(io.Discard, zr, 256<<10)
	var corrupt flate.CorruptInputError
	switch {
	case errors.As(err, &corrupt):
		return invalid("gzip deflate stream")
	case errors.Is(err, gzip.ErrChecksum):
		return invalid("gzip checksum")
	}
	return inspection{}
}

func inspectBzip2(head []byte) inspection {
	if len(head) < 10 {
		return invalid("bzip2 truncated")
	}
	magic := head[4:10]
	if bytes.Equal(magic, []byte{0x31, 0x41, 0x59, 0x26, 0x53, 0x59}) ||
		bytes.Equal(magic, []byte{0x17, 0x72, 0x45, 0x38, 0x50, 0x90}) {
		return inspection{}
	}
	return invalid("bzip2 block magic")
}

func inspectXZ(head []byte) inspection {
	if len(head) < 12 {
		return invalid("xz truncated")
	}
	if crc32.ChecksumIEEE(head[6:8]) != binary.LittleEndian.Uint32(head[8:12]) {
		return invalid("xz stream header crc")
	}
	return inspection{}
}

// --- images ---------------------------------------------------------------

func inspectImageConfig(head []byte, decode func(io.Reader) (image.Config, error)) inspection {
	if _, err := decode(bytes.NewReader(head)); err != nil {
		if isFormatError(err) {
			return invalid("image header: " + err.Error())
		}
	}
	return inspection{}
}

func isFormatError(err error) bool {
	var jf jpeg.FormatError
	var pf png.FormatError
	if errors.As(err, &jf) || errors.As(err, &pf) {
		return true
	}
	msg := err.Error()
	return strings.HasPrefix(msg, "gif:") && !strings.Contains(msg, "EOF") ||
		strings.HasPrefix(msg, "webp: invalid format")
}

// jpegEnd returns the offset just after the EOI marker by walking JPEG
// segments and entropy-coded data. ok=false means the stream is malformed or
// has no EOI.
func jpegEnd(r io.ByteReader) (int64, bool) {
	var pos int64
	next := func() (byte, bool) {
		b, err := r.ReadByte()
		if err != nil {
			return 0, false
		}
		pos++
		return b, true
	}
	skip := func(n int) bool {
		for i := 0; i < n; i++ {
			if _, ok := next(); !ok {
				return false
			}
		}
		return true
	}
	if a, ok := next(); !ok || a != 0xFF {
		return 0, false
	}
	if b, ok := next(); !ok || b != 0xD8 {
		return 0, false
	}
	for {
		b, ok := next()
		if !ok {
			return 0, false
		}
		if b != 0xFF {
			return 0, false
		}
		m, ok := next()
		for ok && m == 0xFF { // fill bytes
			m, ok = next()
		}
		if !ok {
			return 0, false
		}
		switch {
		case m == 0xD9:
			return pos, true
		case m >= 0xD0 && m <= 0xD7, m == 0x01:
			continue
		}
		hi, ok1 := next()
		lo, ok2 := next()
		if !ok1 || !ok2 {
			return 0, false
		}
		segLen := int(hi)<<8 | int(lo)
		if segLen < 2 || !skip(segLen-2) {
			return 0, false
		}
		if m != 0xDA {
			continue
		}
		// Entropy-coded data until a real marker.
		for {
			c, ok := next()
			if !ok {
				return 0, false
			}
			if c != 0xFF {
				continue
			}
			d, ok := next()
			for ok && d == 0xFF {
				d, ok = next()
			}
			if !ok {
				return 0, false
			}
			if d == 0x00 || (d >= 0xD0 && d <= 0xD7) {
				continue
			}
			if d == 0xD9 {
				return pos, true
			}
			// Another marker (e.g. next SOS/DHT in progressive JPEG).
			hi, ok1 := next()
			lo, ok2 := next()
			if !ok1 || !ok2 {
				return 0, false
			}
			segLen := int(hi)<<8 | int(lo)
			if segLen < 2 || !skip(segLen-2) {
				return 0, false
			}
			if d != 0xDA {
				break // back to segment loop
			}
		}
	}
}

func inspectJPEG(src contentSource, head []byte, depth int) inspection {
	if in := inspectImageConfig(head, jpeg.DecodeConfig); in.Kind != inspectOK {
		return in
	}
	if src.Size() > maxFullScanBytes {
		return inspection{}
	}
	end, ok := jpegEnd(newBufioReader(src))
	if !ok {
		// Truncated or exotic JPEG: no trailer to analyze, decoder accepted
		// the header. Do not guess.
		return inspection{}
	}
	return analyzeTrailer(src, end, depth)
}

func inspectPNG(src contentSource, head []byte, depth int) inspection {
	if in := inspectImageConfig(head, png.DecodeConfig); in.Kind != inspectOK {
		return in
	}
	end, ok, bad := pngWalk(src)
	if bad != "" {
		return invalid(bad)
	}
	if !ok {
		return inspection{} // truncated: nothing appended
	}
	return analyzeTrailer(src, end, depth)
}

// pngEnd returns the offset just after the IEND chunk.
func pngEnd(src contentSource) (int64, bool) {
	end, ok, bad := pngWalk(src)
	return end, ok && bad == ""
}

func pngWalk(src contentSource) (end int64, ok bool, bad string) {
	size := src.Size()
	off := int64(8)
	var hdr [8]byte
	for chunks := 0; off+12 <= size && chunks < 1<<20; chunks++ {
		if n, _ := src.ReadAt(hdr[:], off); n != 8 {
			return 0, false, ""
		}
		length := int64(binary.BigEndian.Uint32(hdr[:4]))
		for _, c := range hdr[4:8] {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
				return 0, false, "png chunk type"
			}
		}
		if length > 1<<31-1 {
			return 0, false, "png chunk length"
		}
		off += 12 + length
		if string(hdr[4:8]) == "IEND" {
			return off, true, ""
		}
	}
	return 0, false, ""
}

func inspectRIFF(src contentSource, head []byte, depth int) inspection {
	if len(head) < 12 {
		return inspection{}
	}
	form := string(head[8:12])
	if form == "WEBP" {
		if in := inspectImageConfig(head, webp.DecodeConfig); in.Kind != inspectOK {
			return in
		}
	}
	riffSize := int64(binary.LittleEndian.Uint32(head[4:8]))
	size := src.Size()
	if riffSize == 0 || riffSize == 0xFFFFFFFF {
		return inspection{} // streaming writers leave the size unset
	}
	end := 8 + riffSize
	// Walk the top-level chunks; every id must be printable FourCC.
	off := int64(12)
	var hdr [8]byte
	for chunks := 0; off+8 <= end && off+8 <= size && chunks < 1<<16; chunks++ {
		if n, _ := src.ReadAt(hdr[:], off); n != 8 {
			break
		}
		for _, c := range hdr[:4] {
			if c < 0x20 || c > 0x7E {
				return mediaInvalid(src, 12, "riff chunk id")
			}
		}
		clen := int64(binary.LittleEndian.Uint32(hdr[4:8]))
		off += 8 + clen + clen&1
	}
	if end < size {
		if end&1 == 1 && size-end == 1 {
			return inspection{}
		}
		return analyzeTrailer(src, end, depth)
	}
	return inspection{}
}

// --- audio/video containers ----------------------------------------------

func printableFourCC(b []byte) bool {
	for _, c := range b {
		if !(c >= 0x20 && c <= 0x7E || c == 0xA9) {
			return false
		}
	}
	return true
}

func inspectISOBMFF(src contentSource, depth int) inspection {
	size := src.Size()
	var off int64
	var hdr [16]byte
	hasIndex := false
	for boxes := 0; off < size && boxes < 1<<20; boxes++ {
		if size-off < 8 {
			if size-off <= 4 {
				break // tiny tail, tolerated
			}
			return analyzeTrailer(src, off, depth)
		}
		n, _ := src.ReadAt(hdr[:], off)
		if n < 8 {
			break
		}
		boxSize := int64(binary.BigEndian.Uint32(hdr[:4]))
		typ := string(hdr[4:8])
		if !printableFourCC(hdr[4:8]) {
			if hasIndex {
				return analyzeTrailer(src, off, depth)
			}
			return mediaInvalid(src, 64, "box type")
		}
		hdrLen := int64(8)
		switch boxSize {
		case 1:
			if n < 16 {
				return mediaInvalid(src, 64, "largesize truncated")
			}
			boxSize = int64(binary.BigEndian.Uint64(hdr[8:16]))
			hdrLen = 16
		case 0:
			boxSize = size - off
		}
		if boxSize < hdrLen {
			if hasIndex {
				return analyzeTrailer(src, off, depth)
			}
			return mediaInvalid(src, 64, "box size")
		}
		switch typ {
		case "moov", "moof", "meta":
			hasIndex = true
		}
		if off+boxSize > size {
			// Last box runs past EOF (truncated file).
			break
		}
		off += boxSize
	}
	if !hasIndex {
		return mediaInvalid(src, 64, "no moov/moof/meta box")
	}
	return inspection{}
}

// readEBMLID reads an EBML element id (1-4 bytes, marker bits kept).
func readEBMLID(b []byte) (uint32, int, bool) {
	if len(b) == 0 || b[0] == 0 {
		return 0, 0, false
	}
	n := 1
	for mask := byte(0x80); b[0]&mask == 0; mask >>= 1 {
		n++
	}
	if n > 4 || len(b) < n {
		return 0, 0, false
	}
	var id uint32
	for i := 0; i < n; i++ {
		id = id<<8 | uint32(b[i])
	}
	return id, n, true
}

// readEBMLSize reads an EBML data size (1-8 bytes); unknown=true for the
// all-ones reserved value.
func readEBMLSize(b []byte) (size uint64, n int, unknown, ok bool) {
	if len(b) == 0 || b[0] == 0 {
		return 0, 0, false, false
	}
	n = 1
	for mask := byte(0x80); b[0]&mask == 0; mask >>= 1 {
		n++
	}
	if n > 8 || len(b) < n {
		return 0, 0, false, false
	}
	size = uint64(b[0] & (0xFF >> uint(n)))
	allOnes := size == uint64(0xFF>>uint(n))
	for i := 1; i < n; i++ {
		size = size<<8 | uint64(b[i])
		if b[i] != 0xFF {
			allOnes = false
		}
	}
	return size, n, allOnes, true
}

var matroskaLevel1 = map[uint32]bool{
	0x114D9B74: true, // SeekHead
	0x1549A966: true, // Info
	0x1654AE6B: true, // Tracks
	0x1F43B675: true, // Cluster
	0x1C53BB6B: true, // Cues
	0x1254C367: true, // Tags
	0x1941A469: true, // Attachments
	0x1043A770: true, // Chapters
	0xEC:       true, // Void
	0xBF:       true, // CRC-32
}

func inspectEBML(src contentSource, depth int) inspection {
	size := src.Size()
	buf, err := readRegion(src, 0, 64)
	if err != nil && !errors.Is(err, errShortRead) {
		return inspection{}
	}
	id, n, ok := readEBMLID(buf)
	if !ok || id != 0x1A45DFA3 {
		return mediaInvalid(src, 16, "ebml header id")
	}
	hsize, m, unknown, ok := readEBMLSize(buf[n:])
	if !ok || unknown || hsize > 4096 {
		return mediaInvalid(src, 16, "ebml header size")
	}
	off := int64(n+m) + int64(hsize)
	seg, err := readRegion(src, off, 16)
	if err != nil && !errors.Is(err, errShortRead) {
		return inspection{}
	}
	id, n, ok = readEBMLID(seg)
	if !ok || id != 0x18538067 {
		return mediaInvalid(src, 16, "ebml segment id")
	}
	segSize, m, segUnknown, ok := readEBMLSize(seg[n:])
	if !ok {
		return mediaInvalid(src, 16, "ebml segment size")
	}
	child := off + int64(n+m)
	segEnd := size
	if !segUnknown {
		segEnd = child + int64(segSize)
	}
	// The first few level-1 children must be known Matroska elements.
	for i := 0; i < 4 && child < segEnd && child < size; i++ {
		el, err := readRegion(src, child, 16)
		if err != nil && !errors.Is(err, errShortRead) {
			return inspection{}
		}
		id, n, ok := readEBMLID(el)
		if !ok || !matroskaLevel1[id] {
			return mediaInvalid(src, 16, "matroska level-1 element")
		}
		esize, m, eunknown, ok := readEBMLSize(el[n:])
		if !ok {
			return mediaInvalid(src, 16, "matroska element size")
		}
		if eunknown {
			break
		}
		child += int64(n+m) + int64(esize)
	}
	if !segUnknown && segEnd < size {
		return analyzeTrailer(src, segEnd, depth)
	}
	return inspection{}
}

var oggCRCTable = func() [256]uint32 {
	var t [256]uint32
	for i := range t {
		r := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if r&0x80000000 != 0 {
				r = r<<1 ^ 0x04C11DB7
			} else {
				r <<= 1
			}
		}
		t[i] = r
	}
	return t
}()

func oggCRC(b []byte) uint32 {
	var crc uint32
	for _, c := range b {
		crc = crc<<8 ^ oggCRCTable[byte(crc>>24)^c]
	}
	return crc
}

func inspectOgg(src contentSource, depth int) inspection {
	size := src.Size()
	var off int64
	pages := 0
	for off < size && pages < 1<<22 {
		hdr, err := readRegion(src, off, 27+255)
		if err != nil && !errors.Is(err, errShortRead) {
			return inspection{}
		}
		if len(hdr) < 27 || !bytes.HasPrefix(hdr, []byte("OggS")) || hdr[4] != 0 {
			if pages == 0 {
				return mediaInvalid(src, 4, "ogg first page")
			}
			return analyzeTrailer(src, off, depth)
		}
		nseg := int(hdr[26])
		if len(hdr) < 27+nseg {
			break // truncated
		}
		body := 0
		for _, s := range hdr[27 : 27+nseg] {
			body += int(s)
		}
		pageLen := int64(27 + nseg + body)
		if pages < 3 {
			page, err := readRegion(src, off, int(pageLen))
			if err != nil {
				break // truncated page
			}
			want := binary.LittleEndian.Uint32(page[22:26])
			page[22], page[23], page[24], page[25] = 0, 0, 0, 0
			if oggCRC(page) != want {
				return mediaInvalid(src, 4, "ogg page crc")
			}
		} else if size > maxFullScanBytes {
			break // validated the start; skip walking huge files
		}
		off += pageLen
		pages++
	}
	return inspection{}
}

// MPEG audio frame lengths.
var mpeg1Bitrates = [4][16]int{
	3: {0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448, 0}, // layer I
	2: {0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, 0},    // layer II
	1: {0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0},     // layer III
}
var mpeg2Bitrates = [4][16]int{
	3: {0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256, 0},
	2: {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
	1: {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
}
var mpegSampleRates = [4][3]int{
	3: {44100, 48000, 32000}, // MPEG1
	2: {22050, 24000, 16000}, // MPEG2
	0: {11025, 12000, 8000},  // MPEG2.5
}

// mpegFrameLen returns the length of the MPEG audio or ADTS frame at b[0:].
func mpegFrameLen(b []byte) int {
	if len(b) < 7 || b[0] != 0xFF || b[1]&0xE0 != 0xE0 {
		return 0
	}
	version := (b[1] >> 3) & 0x03
	layer := (b[1] >> 1) & 0x03
	if b[1]&0xF6 == 0xF0 { // ADTS: sync 0xFFF, layer 00
		l := int(b[3]&0x03)<<11 | int(b[4])<<3 | int(b[5]>>5)
		if l < 7 {
			return 0
		}
		return l
	}
	if version == 1 || layer == 0 {
		return 0
	}
	brIdx := b[2] >> 4
	srIdx := (b[2] >> 2) & 0x03
	if brIdx == 0 || brIdx == 15 || srIdx == 3 {
		return 0
	}
	pad := int((b[2] >> 1) & 0x01)
	var br int
	if version == 3 {
		br = mpeg1Bitrates[layer][brIdx]
	} else {
		br = mpeg2Bitrates[layer][brIdx]
	}
	sr := mpegSampleRates[version][srIdx]
	if br == 0 || sr == 0 {
		return 0
	}
	br *= 1000
	switch {
	case layer == 3: // layer I
		return (12*br/sr + pad) * 4
	case layer == 1 && version != 3: // layer III, MPEG2/2.5
		return 72*br/sr + pad
	default:
		return 144*br/sr + pad
	}
}

// mpegAudioChainAt reports whether `frames` consecutive valid frames start at off.
func mpegAudioChainAt(b []byte, off, frames int) bool {
	for i := 0; i < frames; i++ {
		if off >= len(b) {
			return i > 0 // ran out of sample after valid frames
		}
		l := mpegFrameLen(b[off:])
		if l == 0 {
			return false
		}
		off += l
	}
	return true
}

func inspectID3(src contentSource, head []byte) inspection {
	if len(head) < 10 {
		return invalid("id3 truncated")
	}
	for _, c := range head[6:10] {
		if c >= 0x80 {
			return invalid("id3 size")
		}
	}
	tagSize := int64(head[6])<<21 | int64(head[7])<<14 | int64(head[8])<<7 | int64(head[9])
	start := 10 + tagSize
	if head[5]&0x10 != 0 {
		start += 10
	}
	if start >= src.Size() {
		return inspection{} // tag-only file
	}
	audio, err := readRegion(src, start, sniffBytes)
	if err != nil && !errors.Is(err, errShortRead) {
		return inspection{}
	}
	// Look for a 3-frame chain within the first part of the audio data
	// (encoders may leave padding or junk before the first frame).
	limit := min(len(audio), 16<<10)
	for i := 0; i < limit; i++ {
		if audio[i] == 0xFF && i+1 < len(audio) && audio[i+1]&0xE0 == 0xE0 && mpegAudioChainAt(audio, i, 3) {
			return inspection{}
		}
	}
	// ID3 in front of something else (FLAC, AAC in other wrapper, ...).
	if f := detectFormat(audio); f != "" && f != fmtMPEGA {
		return inspection{}
	}
	if samplesLookRandom(src, start) {
		return inspection{Kind: inspectOpaque, Detail: "random data behind id3 tag"}
	}
	return inspection{}
}

func inspectPDF(src contentSource, head []byte, depth int) inspection {
	size := src.Size()
	tailLen := int64(sniffBytes)
	if tailLen > size {
		tailLen = size
	}
	tail, err := readRegion(src, size-tailLen, int(tailLen))
	if err != nil && !errors.Is(err, errShortRead) {
		return inspection{}
	}
	if idx := bytes.LastIndex(tail, []byte("%%EOF")); idx >= 0 {
		end := size - tailLen + int64(idx) + 5
		return analyzeTrailer(src, end, depth)
	}
	// No end marker near the end: find the last one anywhere (bounded scan);
	// everything after it was appended.
	if size <= maxFullScanBytes {
		if end := lastMarkerEnd(src, []byte("%%EOF")); end > 0 {
			return analyzeTrailer(src, end, depth)
		}
	}
	if bytes.Contains(head, []byte(" obj")) || bytes.Contains(head, []byte("\nobj")) {
		return inspection{}
	}
	if samplesLookRandom(src, 16) {
		return inspection{Kind: inspectOpaque, Detail: "random data behind pdf header"}
	}
	return inspection{}
}

// lastMarkerEnd returns the offset just after the last occurrence of marker in
// src, or -1. It reads the source sequentially in overlapping chunks.
func lastMarkerEnd(src contentSource, marker []byte) int64 {
	const chunk = 256 << 10
	last := int64(-1)
	size := src.Size()
	overlap := int64(len(marker) - 1)
	for off := int64(0); off < size; off += chunk - overlap {
		buf, err := readRegion(src, off, chunk)
		if err != nil && !errors.Is(err, errShortRead) {
			return last
		}
		if idx := bytes.LastIndex(buf, marker); idx >= 0 {
			last = off + int64(idx) + int64(len(marker))
		}
		if int64(len(buf)) < chunk {
			break
		}
	}
	return last
}

// InspectContentForScan exposes the inspector to offline tools (dry-run
// scans of an existing blob store). It returns the verdict kind ("" = ok),
// detected format and detail.
func InspectContentForScan(r io.ReaderAt, size int64) (kind, format, detail string) {
	in := inspectContent(io.NewSectionReader(r, 0, size))
	return string(in.Kind), in.Format, in.Detail
}
