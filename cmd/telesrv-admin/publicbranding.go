package main

import (
	"net/http"
)

// The login screen shows which server it belongs to, so the two fields it needs
// are served without a session.
//
// This discloses nothing new. owpengram-server already publishes the same name
// and icon to every client that asks, over its own open endpoints
// (/owpengram/server-info and /owpengram/server-icon) -- that is how a client
// fills in the "Add Server" form and how the panel's own RefreshServersInfo
// works. Branding is public by design; the point of a server name is to be
// read before you are anybody.
//
// It stays deliberately narrow all the same: name and whether an icon exists,
// and nothing else off identity.Info, which also carries the description and
// the welcome/login-code message templates. Those are operator-facing settings
// and stay behind server.manage.

type publicBrandingResponse struct {
	Name    string `json:"name"`
	HasIcon bool   `json:"has_icon"`
}

func (s *server) handlePublicBrandingAPI(w http.ResponseWriter, _ *http.Request) {
	out := publicBrandingResponse{}
	if s.identity != nil {
		if info, err := s.identity.Get(); err == nil {
			out.Name = info.Name
		}
		_, _, ok := s.identity.Icon()
		out.HasIcon = ok
	}
	// A server that has not been named yet answers with an empty name rather
	// than an error: the login page falls back to its own branding, and a
	// failed request there would only produce a console error for nothing.
	writeJSON(w, http.StatusOK, out)
}

// handlePublicIconAPI serves the same bytes as handleServerIconAPI, without the
// session. Same file, same rationale as above.
func (s *server) handlePublicIconAPI(w http.ResponseWriter, _ *http.Request) {
	if s.identity == nil {
		http.NotFound(w, nil)
		return
	}
	data, ext, ok := s.identity.Icon()
	if !ok {
		http.Error(w, "no icon configured", http.StatusNotFound)
		return
	}
	contentType := map[string]string{
		".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
		".webp": "image/webp", ".gif": "image/gif",
	}[ext]
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}
