package files

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"go.uber.org/zap"

	"telesrv/internal/domain"
)

// Upload content policy (TELESRV_STORAGE_OPAQUE_UPLOAD_POLICY and
// TELESRV_STORAGE_ENCRYPTED_CONTAINER_POLICY).
//
// Every path that turns client/bot/fetched bytes into a stored document runs
// inspectContent (content_inspect.go) before the bytes are committed:
//   - known abuse containers (ALICEU01) are always rejected unless the policy
//     is "off";
//   - opaque random bytes, containers with broken structure and random data
//     appended after a file's real end are rejected in "block" mode and only
//     logged in "log" mode;
//   - recognizable encryption (password-protected zip/7z/rar, openssl enc,
//     age, OpenPGP messages) is governed by the encrypted-container policy:
//     "flag" (default: allowed, logged and reported to moderation), "block",
//     or "off".
//
// Secret-chat files are E2E-encrypted by design and never inspected.
// Rejections map to MEDIA_INVALID. Hits are logged as "upload content policy
// hit" (metadata only) and forwarded to the ContentPolicyReporter.
const (
	OpaqueUploadPolicyBlock = "block"
	OpaqueUploadPolicyLog   = "log"
	OpaqueUploadPolicyOff   = "off"

	EncryptedContainerPolicyFlag  = "flag"
	EncryptedContainerPolicyBlock = "block"
	EncryptedContainerPolicyOff   = "off"
)

// WithOpaqueUploadPolicy sets the opaque/abuse-format upload policy. Unknown
// or empty values fall back to "block".
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

// WithEncryptedContainerPolicy sets the policy for recognizable encrypted
// containers. Unknown or empty values fall back to "flag".
func WithEncryptedContainerPolicy(policy string) Option {
	return func(s *Service) {
		switch policy {
		case EncryptedContainerPolicyBlock, EncryptedContainerPolicyOff:
			s.encryptedContainerPolicy = policy
		default:
			s.encryptedContainerPolicy = EncryptedContainerPolicyFlag
		}
	}
}

// contentPolicyReporter is set after construction (the moderation service is
// built later in cmd/telesrv); guarded for concurrent readers.
type contentPolicyReporterHolder struct {
	mu sync.RWMutex
	r  domain.ContentPolicyReporter
}

// SetContentPolicyReporter installs the receiver of content-policy hits.
func (s *Service) SetContentPolicyReporter(r domain.ContentPolicyReporter) {
	if s == nil {
		return
	}
	s.contentReporter.mu.Lock()
	s.contentReporter.r = r
	s.contentReporter.mu.Unlock()
}

func (s *Service) reportContentHit(ctx context.Context, hit domain.ContentPolicyHit) {
	s.contentReporter.mu.RLock()
	r := s.contentReporter.r
	s.contentReporter.mu.RUnlock()
	if r != nil {
		r.ReportContentPolicyHit(ctx, hit)
	}
}

// decide maps an inspection to (block, report) under the configured policies.
func (s *Service) decide(in inspection) (block, report bool) {
	opaque := s.opaqueUploadPolicy
	switch in.Kind {
	case inspectOK:
		return false, false
	case inspectBlockedFormat:
		return opaque != OpaqueUploadPolicyOff, opaque != OpaqueUploadPolicyOff
	case inspectEncryptedContainer:
		switch s.encryptedContainerPolicy {
		case EncryptedContainerPolicyOff:
			return false, false
		case EncryptedContainerPolicyBlock:
			return true, true
		default:
			return false, true
		}
	default: // opaque, invalid_structure, trailing_data
		switch opaque {
		case OpaqueUploadPolicyOff:
			return false, false
		case OpaqueUploadPolicyLog:
			return false, true
		default:
			return true, true
		}
	}
}

// applyContentPolicy inspects src and returns ErrMediaContentBlocked when the
// policy refuses it. source names the path for logs/reports.
func (s *Service) applyContentPolicy(ctx context.Context, src contentSource, ownerUserID int64, source, mimeType string) error {
	if s.opaqueUploadPolicy == OpaqueUploadPolicyOff && s.encryptedContainerPolicy == EncryptedContainerPolicyOff {
		return nil
	}
	in := inspectContent(src)
	block, report := s.decide(in)
	if !report {
		return nil
	}
	s.log.Warn("upload content policy hit",
		zap.String("verdict", string(in.Kind)),
		zap.String("format", in.Format),
		zap.String("detail", in.Detail),
		zap.Bool("blocked", block),
		zap.String("source", source),
		zap.Int64("owner_user_id", ownerUserID),
		zap.String("declared_mime", mimeType),
		zap.Int64("size", src.Size()))
	s.reportContentHit(ctx, domain.ContentPolicyHit{
		UserID:  ownerUserID,
		Source:  source,
		Kind:    string(in.Kind),
		Format:  in.Format,
		Blocked: block,
		Size:    src.Size(),
		At:      time.Now(),
	})
	if block {
		return domain.ErrMediaContentBlocked
	}
	return nil
}

// checkUploadContent enforces the policy on a chunked upload before it is
// assembled into a permanent blob. It reads the parts through OpenUploadPart
// (never GetUploadPart) and keeps at most two parts in memory.
func (s *Service) checkUploadContent(ctx context.Context, file domain.UploadedFileRef, mimeType, source string) error {
	if s.uploadParts == nil {
		return nil
	}
	parts, err := s.media.LoadFileParts(ctx, file.OwnerUserID, file.FileID)
	if err != nil || len(parts) == 0 {
		// Let the normal assembly path report part errors.
		return nil
	}
	var total int64
	for i, p := range parts {
		if p.Part != i || p.Size <= 0 || p.ObjectKey == "" {
			return nil
		}
		total += p.Size
	}
	src := &uploadPartsSource{ctx: ctx, backend: s.uploadParts, parts: parts, size: total}
	return s.applyContentPolicy(ctx, src, file.OwnerUserID, source, mimeType)
}

// checkBytesContent enforces the policy on bytes already in memory (Bot API,
// inline bots, external media).
func (s *Service) checkBytesContent(ctx context.Context, data []byte, mimeType, source string) error {
	return s.applyContentPolicy(ctx, bytes.NewReader(data), domain.ContentUploaderFrom(ctx), source, mimeType)
}

// uploadPartsSource gives random access over the ordered upload parts of one
// file. Parts are read whole (each is at most MaxUploadPartBytes) via
// OpenUploadPart and cached two at a time, so sequential walks read every
// byte at most once.
type uploadPartsSource struct {
	ctx     context.Context
	backend UploadPartBackend
	parts   []domain.UploadPart
	size    int64

	cache [2]struct {
		idx  int
		data []byte
	}
	next int
}

func (u *uploadPartsSource) Size() int64 { return u.size }

func (u *uploadPartsSource) part(idx int) ([]byte, error) {
	for i := range u.cache {
		if u.cache[i].data != nil && u.cache[i].idx == idx {
			return u.cache[i].data, nil
		}
	}
	rc, err := u.backend.OpenUploadPart(u.ctx, u.parts[idx].ObjectKey)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(rc, u.parts[idx].Size+1))
	_ = rc.Close()
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != u.parts[idx].Size {
		return nil, domain.ErrFilePartsInvalid
	}
	slot := u.next
	u.next = (u.next + 1) % len(u.cache)
	u.cache[slot].idx = idx
	u.cache[slot].data = data
	return data, nil
}

func (u *uploadPartsSource) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if off >= u.size {
		return 0, io.EOF
	}
	n := 0
	var start int64
	for idx, part := range u.parts {
		end := start + part.Size
		if off+int64(n) >= end {
			start = end
			continue
		}
		data, err := u.part(idx)
		if err != nil {
			return n, err
		}
		inner := off + int64(n) - start
		n += copy(p[n:], data[inner:])
		if n == len(p) {
			return n, nil
		}
		start = end
	}
	return n, io.EOF
}

// newBufioReader returns a sequential byte reader over the whole source.
func newBufioReader(src contentSource) *bufio.Reader {
	return bufio.NewReaderSize(io.NewSectionReader(src, 0, src.Size()), 64<<10)
}

// imageEnd returns the offset just after the real end of a JPEG or PNG image
// in data, or -1 when the format is different or the end cannot be found.
func imageEnd(data []byte) int64 {
	switch detectFormat(data[:min(len(data), 16)]) {
	case fmtJPEG:
		if end, ok := jpegEnd(bufio.NewReader(bytes.NewReader(data))); ok {
			return end
		}
	case fmtPNG:
		if end, ok := pngEnd(bytes.NewReader(data)); ok {
			return end
		}
	}
	return -1
}

// sanitizeImageBytes drops anything appended after the real end of a JPEG or
// PNG before it is stored (a photo is re-served as the original bytes, so an
// appended archive would otherwise ride along). A random-looking or invalid
// trailer is logged and reported; the image itself is kept.
func (s *Service) sanitizeImageBytes(ctx context.Context, data []byte, ownerUserID int64, source string) []byte {
	end := imageEnd(data)
	if end <= 0 || end >= int64(len(data)) {
		return data
	}
	if s.opaqueUploadPolicy != OpaqueUploadPolicyOff {
		if trailer := analyzeTrailer(bytes.NewReader(data), end, 0); trailer.Kind != inspectOK {
			s.log.Warn("upload content policy hit",
				zap.String("verdict", string(inspectTrailingData)),
				zap.String("format", trailer.Format),
				zap.String("detail", "stripped from image: "+trailer.Detail),
				zap.Bool("blocked", false),
				zap.String("source", source),
				zap.Int64("owner_user_id", ownerUserID),
				zap.Int64("size", int64(len(data))),
				zap.Int64("trailer_size", int64(len(data))-end))
			s.reportContentHit(ctx, domain.ContentPolicyHit{
				UserID: ownerUserID, Source: source, Kind: string(inspectTrailingData),
				Format: trailer.Format, Blocked: false, Size: int64(len(data)), At: time.Now(),
			})
		}
	}
	return data[:end]
}
