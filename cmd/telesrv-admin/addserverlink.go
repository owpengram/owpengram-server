package main

import (
	"net/http"
	"net/url"
	"strconv"
)

// handleAddServerLinkAPI builds an owpg://addserver link (see the desktop
// and Android clients' handling of that scheme) carrying only this server's
// host and port, so an operator can hand it out as a ready-made "add my
// server" button/QR code.
//
// Deliberately carries nothing else -- no name, description, key, or DC.
// Anyone who can get a link in front of a user (a forum post, a chat
// message, an intercepted share) controls whatever it contains; if it also
// carried the RSA key, a link with a forged key pointed at an attacker's own
// host would be indistinguishable from a real one, and the client would
// trust it outright as "this server's identity" -- a real MITM vector, not
// a hypothetical one. host+port alone can't misrepresent anything: the
// client always fetches name/description/key/DC itself, straight from
// whatever actually answers at that address (ServerInfoPath), the same way
// it already does for a hand-typed address in the "Add Server" form.
func (s *server) handleAddServerLinkAPI(w http.ResponseWriter, r *http.Request) {
	q := url.Values{}
	q.Set("host", s.cfg.AdvertiseHost)
	q.Set("port", strconv.Itoa(s.cfg.ServerPort))

	writeJSON(w, http.StatusOK, map[string]any{
		"link": "owpg://addserver?" + q.Encode(),
	})
}
