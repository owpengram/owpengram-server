package domain

import (
	"context"
	"math"
	"strings"
	"time"
)

// blockedClientMarkers are protocol strings of third-party client-side
// encryption layers built on top of regular messages to hide abuse content
// from the server (2026-11 CSAM case: the "Alice" modded client exchanged
// ECDH keys and group join proofs as plain text messages). A message
// containing one of them is refused, which breaks the key exchange and so the
// whole encrypted channel.
var blockedClientMarkers = []string{
	"AliceAvatarHello",
	"AliceAvatarRevoke",
	"AliceJoinProof",
	"ALICE8W.",
	"ALICE_AUTO_WELCOME",
}

// ContainsBlockedClientMarker reports whether text carries a blocked
// third-party encryption protocol marker.
func ContainsBlockedClientMarker(text string) bool {
	if len(text) < 8 {
		return false
	}
	for _, m := range blockedClientMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// spkiBase64Signatures are the fixed base64 (std and url-safe agree on them:
// none contains '+' or '/') encodings of the DER SubjectPublicKeyInfo headers
// of common public keys. Any app that exchanges keys over chat text by
// encoding the standard SPKI form -- whatever it calls its messages -- emits
// one of these, so matching them survives a renamed protocol.
var spkiBase64Signatures = []string{
	"MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE", // EC P-256
	"MHYwEAYHKoZIzj0CAQYFK4EEACIDYgAE",     // EC P-384
	"MIGbMBAGByqGSM49AgEGBSuBBAAjA4GGAA",   // EC P-521
	"MFYwEAYHKoZIzj0CAQYFK4EEAAoDQgAE",     // EC secp256k1
	"MCowBQYDK2VuAyEA",                     // X25519
	"MCowBQYDK2VwAyEA",                     // Ed25519
	"MEIwBQYDK2VvAzkA",                     // X448
	"MEMwBQYDK2VxAzoA",                     // Ed448
	"jANBgkqhkiG9w0BAQEFAAOC",              // RSA (any size), after "MIIBIj"/"MIICIj"...
}

// armoredKeyMarkers are PEM/OpenPGP armored key headers. People do share
// PGP public keys legitimately, so these are only reported, never blocked.
var armoredKeyMarkers = []string{
	"-----BEGIN PUBLIC KEY-----",
	"-----BEGIN EC PUBLIC KEY-----",
	"-----BEGIN RSA PUBLIC KEY-----",
	"-----BEGIN PGP PUBLIC KEY BLOCK-----",
}

// MessageTextVerdict classifies chat text for the anti-abuse content policy.
type MessageTextVerdict int

const (
	MessageTextOK MessageTextVerdict = iota
	// MessageTextClientMarker: a known abuse-client protocol marker (block).
	MessageTextClientMarker
	// MessageTextRawPublicKey: a bare base64 SPKI public key (block).
	MessageTextRawPublicKey
	// MessageTextArmoredKey: a PEM/PGP armored public key (report only).
	MessageTextArmoredKey
	// MessageTextOpaqueToken: the text is dominated by a long random-looking
	// token, the shape of ciphertext, keys or proofs (report only).
	MessageTextOpaqueToken
)

func (v MessageTextVerdict) String() string {
	switch v {
	case MessageTextClientMarker:
		return "client_marker"
	case MessageTextRawPublicKey:
		return "raw_public_key"
	case MessageTextArmoredKey:
		return "armored_key"
	case MessageTextOpaqueToken:
		return "opaque_token"
	default:
		return "ok"
	}
}

// Blocks reports whether the verdict refuses the message (as opposed to only
// reporting it).
func (v MessageTextVerdict) Blocks() bool {
	return v == MessageTextClientMarker || v == MessageTextRawPublicKey
}

// ClassifyMessageText runs the anti-abuse text checks, most severe first.
func ClassifyMessageText(text string) MessageTextVerdict {
	if len(text) < 16 {
		return MessageTextOK
	}
	if ContainsBlockedClientMarker(text) {
		return MessageTextClientMarker
	}
	for _, sig := range spkiBase64Signatures {
		if strings.Contains(text, sig) {
			return MessageTextRawPublicKey
		}
	}
	for _, m := range armoredKeyMarkers {
		if strings.Contains(text, m) {
			return MessageTextArmoredKey
		}
	}
	if hasDominantOpaqueToken(text) {
		return MessageTextOpaqueToken
	}
	return MessageTextOK
}

// Opaque-token heuristics: a token of at least minOpaqueTokenLen characters
// from the base64/base64url/hex/separator alphabet, not a URL, mixing at
// least two character classes, with per-character entropy at the level of
// random encodings, making up most of the message.
const (
	minOpaqueTokenLen       = 40
	minOpaqueTokenEntropy   = 4.2
	minOpaqueTokenShareOfMs = 0.6
)

func isTokenRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
		r == '+' || r == '/' || r == '=' || r == '_' || r == '-' || r == '.' || r == ':'
}

func hasDominantOpaqueToken(text string) bool {
	nonSpace := 0
	for _, r := range text {
		if r != ' ' && r != '\n' && r != '\t' && r != '\r' {
			nonSpace++
		}
	}
	if nonSpace < minOpaqueTokenLen {
		return false
	}
	for _, field := range strings.Fields(text) {
		if len(field) < minOpaqueTokenLen || float64(len(field)) < minOpaqueTokenShareOfMs*float64(nonSpace) {
			continue
		}
		if strings.Contains(field, "://") || strings.HasPrefix(strings.ToLower(field), "www.") {
			continue
		}
		if looksOpaque(field) {
			return true
		}
	}
	return false
}

func looksOpaque(token string) bool {
	var lower, upper, digit int
	counts := map[rune]int{}
	for _, r := range token {
		if !isTokenRune(r) {
			return false
		}
		switch {
		case r >= 'a' && r <= 'z':
			lower++
		case r >= 'A' && r <= 'Z':
			upper++
		case r >= '0' && r <= '9':
			digit++
		}
		counts[r]++
	}
	classes := 0
	for _, c := range []int{lower, upper, digit} {
		if c > 0 {
			classes++
		}
	}
	if classes < 2 {
		return false
	}
	n := float64(len(token))
	var h float64
	for _, c := range counts {
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h >= minOpaqueTokenEntropy
}

// ContentPolicyHit is one anti-abuse content-policy signal (an upload or a
// message text that tripped a check). It carries metadata only, never
// content.
type ContentPolicyHit struct {
	UserID  int64
	Source  string // "upload", "bytes", "photo", "avatar_video", "message"
	Kind    string // e.g. "blocked_format", "opaque", "trailing_data", "raw_public_key"
	Format  string
	Blocked bool
	Size    int64
	At      time.Time
}

// ContentPolicyReporter receives content-policy hits (e.g. to file automatic
// moderation reports). Implementations must not block the caller.
type ContentPolicyReporter interface {
	ReportContentPolicyHit(ctx context.Context, hit ContentPolicyHit)
}

type contentUploaderKey struct{}

// WithContentUploader attributes uploads made through byte-based paths (Bot
// API, inline bots, external media) to a user for content-policy logging.
func WithContentUploader(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, contentUploaderKey{}, userID)
}

// ContentUploaderFrom returns the user set by WithContentUploader.
func ContentUploaderFrom(ctx context.Context) int64 {
	if v, ok := ctx.Value(contentUploaderKey{}).(int64); ok {
		return v
	}
	return 0
}
