package domain

import "strings"

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
