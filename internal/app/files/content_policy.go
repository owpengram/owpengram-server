package files

import (
	"bytes"
	"context"
	"io"
	"math"

	"go.uber.org/zap"

	"telesrv/internal/domain"
)

// Opaque upload policy modes (TELESRV_STORAGE_OPAQUE_UPLOAD_POLICY).
//
// Regular message documents (not secret-chat files, which are E2E-encrypted by
// design and go through CreateEncryptedFileFromUpload) are inspected before
// they are committed to the blob store:
//   - known abuse container formats (e.g. the "ALICEU01" client-side
//     encryption wrapper used by a CSAM network in 2026-11) are always
//     rejected unless the policy is "off";
//   - "opaque" files -- no recognizable file signature and content that is
//     statistically indistinguishable from random bytes -- are rejected in
//     "block" mode and only logged in "log" mode. Ordinary users essentially
//     never send raw ciphertext as a document: real media, archives
//     (including password-protected ones) and office files all carry headers.
const (
	OpaqueUploadPolicyBlock = "block"
	OpaqueUploadPolicyLog   = "log"
	OpaqueUploadPolicyOff   = "off"
)

// contentSniffBytes is how much of the first upload part is inspected.
const contentSniffBytes = 64 << 10

// minOpaqueSampleBytes: below this the entropy estimate is too noisy to call
// anything "random", so tiny files are never treated as opaque.
const minOpaqueSampleBytes = 4 << 10

// WithOpaqueUploadPolicy sets the opaque/abuse-format upload policy. Unknown or
// empty values fall back to "block".
func WithOpaqueUploadPolicy(policy string) Option {
	return func(s *Service) {
		switch policy {
		case OpaqueUploadPolicyLog, OpaqueUploadPolicyOff:
			s.opaqueUploadPolicy = policy
		default:
			s.opaqueUploadPolicy = OpaqueUploadPolicyBlock
		}
	}
}

// blockedContainerMagics are file signatures of client-side encryption
// wrappers seen in abuse cases. Matched as a prefix of the file.
var blockedContainerMagics = [][]byte{
	[]byte("ALICEU"), // "Alice" modded client: ALICEU01 + version + chunk index + ciphertext
}

type uploadContentVerdict int

const (
	uploadContentOK uploadContentVerdict = iota
	uploadContentBlockedFormat
	uploadContentOpaque
)

func (v uploadContentVerdict) String() string {
	switch v {
	case uploadContentBlockedFormat:
		return "blocked_format"
	case uploadContentOpaque:
		return "opaque_high_entropy"
	default:
		return "ok"
	}
}

// classifyUploadHead inspects the first bytes of an upload.
func classifyUploadHead(head []byte) (uploadContentVerdict, float64) {
	for _, magic := range blockedContainerMagics {
		if bytes.HasPrefix(head, magic) {
			return uploadContentBlockedFormat, 0
		}
	}
	if len(head) < minOpaqueSampleBytes || hasKnownFileSignature(head) {
		return uploadContentOK, 0
	}
	h := shannonEntropy(head)
	if h >= randomEntropyThreshold(len(head)) {
		return uploadContentOpaque, h
	}
	return uploadContentOK, h
}

// randomEntropyThreshold returns the bits/byte value above which a sample of n
// bytes is consistent with uniformly random data. The plug-in entropy
// estimator is biased low by about (k-1)/(2n ln 2) bits for k=256 symbols, so
// the threshold is 8 minus twice that bias minus a small margin: random data
// stays above it, while compressed-but-structured formats without a header
// and all text/binary formats fall well below.
func randomEntropyThreshold(n int) float64 {
	bias := 255.0 / (2 * float64(n) * math.Ln2)
	return 8 - 2*bias - 0.01
}

func shannonEntropy(data []byte) float64 {
	if len(data) == 0 {
		return 0
	}
	var counts [256]int
	for _, b := range data {
		counts[b]++
	}
	n := float64(len(data))
	var h float64
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

// knownSignatures are prefixes of common file formats whose payload is
// compressed (high entropy) but which are legitimate to send as documents.
var knownSignatures = [][]byte{
	{0xFF, 0xD8, 0xFF},                      // JPEG
	{0x89, 'P', 'N', 'G'},                   // PNG
	[]byte("GIF8"),                          // GIF
	[]byte("RIFF"),                          // WEBP / WAV / AVI
	{0x1A, 0x45, 0xDF, 0xA3},                // Matroska / WebM
	[]byte("OggS"),                          // Ogg / Opus voice
	[]byte("ID3"),                           // MP3 with tag
	[]byte("fLaC"),                          // FLAC
	{0x30, 0x26, 0xB2, 0x75},                // ASF / WMV
	[]byte("FLV"),                           // FLV
	{'P', 'K', 0x03, 0x04},                  // ZIP / APK / DOCX / XLSX / ...
	{'P', 'K', 0x05, 0x06},                  // empty ZIP
	{'P', 'K', 0x07, 0x08},                  // spanned ZIP
	[]byte("Rar!"),                          // RAR
	{'7', 'z', 0xBC, 0xAF},                  // 7z (also when password-protected)
	{0x1F, 0x8B},                            // gzip / .tgs stickers
	{0xFD, '7', 'z', 'X', 'Z'},              // xz
	[]byte("BZh"),                           // bzip2
	{0x28, 0xB5, 0x2F, 0xFD},                // zstd
	{0x04, 0x22, 0x4D, 0x18},                // lz4
	[]byte("%PDF"),                          // PDF
	{0xD0, 0xCF, 0x11, 0xE0},                // legacy MS Office
	[]byte("MSCF"),                          // CAB
	[]byte("ISc("),                          // InstallShield
	[]byte("MZ"),                            // Windows executables
	{0x7F, 'E', 'L', 'F'},                   // ELF
	{0xCA, 0xFE, 0xBA, 0xBE},                // Mach-O fat / Java class
	{0xCF, 0xFA, 0xED, 0xFE},                // Mach-O 64
	[]byte("dex\n"),                         // Android DEX
	[]byte("SQLite format 3"),               // SQLite
	[]byte("age-encryption.org/"),           // age
	[]byte("-----BEGIN"),                    // PEM / armored PGP
	[]byte("Salted__"),                      // openssl enc
	[]byte("II*\x00"),                       // TIFF LE
	[]byte("MM\x00*"),                       // TIFF BE
	[]byte("8BPS"),                          // PSD
	{0x00, 0x00, 0x01, 0xBA},                // MPEG-PS
	{0x00, 0x00, 0x01, 0xB3},                // MPEG video
	{0x00, 0x00, 0x00, 0x0C, 'j', 'P'},      // JPEG 2000
	{0x00, 0x00, 0x00, 0x0C, 'J', 'X', 'L'}, // JPEG XL container
	{0xFF, 0x0A},                            // JPEG XL codestream
	[]byte("wOFF"),                          // WOFF
	[]byte("wOF2"),                          // WOFF2
	[]byte("\x00\x01\x00\x00"),              // TTF
	[]byte("OTTO"),                          // OTF
	[]byte("#!AMR"),                         // AMR
	[]byte("caff"),                          // CAF
	[]byte("FORM"),                          // AIFF
	[]byte("BM"),                            // BMP
}

func hasKnownFileSignature(head []byte) bool {
	for _, sig := range knownSignatures {
		if bytes.HasPrefix(head, sig) {
			return true
		}
	}
	// ISO-BMFF (MP4 / MOV / M4A / HEIC / AVIF / 3GP): "ftyp" box at offset 4.
	if len(head) >= 12 && (bytes.Equal(head[4:8], []byte("ftyp")) ||
		bytes.Equal(head[4:8], []byte("moov")) || bytes.Equal(head[4:8], []byte("mdat")) ||
		bytes.Equal(head[4:8], []byte("free")) || bytes.Equal(head[4:8], []byte("wide")) ||
		bytes.Equal(head[4:8], []byte("skip"))) {
		return true
	}
	// MPEG-TS: 0x47 sync byte every 188 bytes.
	if len(head) >= 188*3 && head[0] == 0x47 && head[188] == 0x47 && head[376] == 0x47 {
		return true
	}
	// MP3 / AAC (ADTS) frame sync without an ID3 tag.
	if len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0 {
		return true
	}
	// tar: "ustar" at offset 257.
	if len(head) >= 262 && bytes.Equal(head[257:262], []byte("ustar")) {
		return true
	}
	return false
}

// checkUploadContent enforces the opaque upload policy on a regular document
// upload before it is assembled into a permanent blob. It only reads the head
// of the first part; it never stores or logs content.
func (s *Service) checkUploadContent(ctx context.Context, file domain.UploadedFileRef, mimeType string) error {
	if s.opaqueUploadPolicy == OpaqueUploadPolicyOff || s.uploadParts == nil {
		return nil
	}
	parts, err := s.media.LoadFileParts(ctx, file.OwnerUserID, file.FileID)
	if err != nil || len(parts) == 0 || parts[0].Part != 0 || parts[0].ObjectKey == "" {
		// Let the normal assembly path report part errors.
		return nil
	}
	rc, err := s.uploadParts.OpenUploadPart(ctx, parts[0].ObjectKey)
	if err != nil {
		return nil
	}
	head, err := io.ReadAll(io.LimitReader(rc, contentSniffBytes))
	_ = rc.Close()
	if err != nil {
		return nil
	}
	verdict, entropy := classifyUploadHead(head)
	if verdict == uploadContentOK {
		return nil
	}
	var total int64
	for _, p := range parts {
		total += p.Size
	}
	blocked := verdict == uploadContentBlockedFormat || s.opaqueUploadPolicy != OpaqueUploadPolicyLog
	s.log.Warn("upload content policy hit",
		zap.String("verdict", verdict.String()),
		zap.Bool("blocked", blocked),
		zap.Int64("owner_user_id", file.OwnerUserID),
		zap.Int64("file_id", file.FileID),
		zap.String("declared_mime", mimeType),
		zap.Int64("size", total),
		zap.Float64("entropy", entropy))
	if blocked {
		return domain.ErrMediaContentBlocked
	}
	return nil
}
