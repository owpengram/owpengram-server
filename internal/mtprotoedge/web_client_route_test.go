package mtprotoedge

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameOrigin(t *testing.T) {
	for _, tc := range []struct {
		origin, host string
		want         bool
	}{
		{"http://127.0.0.1:2398", "127.0.0.1:2398", true},
		{"https://me.owpengram.org", "me.owpengram.org", true},
		{"HTTP://Me.Owpengram.Org", "me.owpengram.org", true},
		{"http://127.0.0.1:8080", "127.0.0.1:2398", false}, // another port is another origin
		{"http://evil.example", "127.0.0.1:2398", false},
		{"http://127.0.0.1:2398.evil.example", "127.0.0.1:2398", false},
		{"http://evil.example/127.0.0.1:2398", "127.0.0.1:2398", false},
		{"null", "127.0.0.1:2398", false},
		{"file://127.0.0.1:2398", "127.0.0.1:2398", false},
		{"", "127.0.0.1:2398", false},
		{"http://127.0.0.1:2398", "", false},
	} {
		if got := sameOrigin(tc.origin, tc.host); got != tc.want {
			t.Errorf("sameOrigin(%q, %q) = %v, want %v", tc.origin, tc.host, got, tc.want)
		}
	}
}

// The embedded web client opens its WebSocket from a page this server served,
// so its Origin is the server's own address and no allowlist entry exists for it.
func TestWebsocketRouteAllowsOwnOriginOnly(t *testing.T) {
	reached := false
	ws := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusSwitchingProtocols)
	})
	h := websocketRouteHandler(ws, []string{"http://localhost:8080"})

	try := func(origin string) int {
		reached = false
		req := httptest.NewRequest(http.MethodGet, "/apiws", nil)
		req.Host = "127.0.0.1:2398"
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	for origin, want := range map[string]int{
		"http://127.0.0.1:2398": http.StatusSwitchingProtocols, // own page
		"http://localhost:8080": http.StatusSwitchingProtocols, // allowlisted dev server
		"":                      http.StatusSwitchingProtocols, // non-browser client
		"http://evil.example":   http.StatusForbidden,
		"http://127.0.0.1:9999": http.StatusForbidden,
	} {
		if got := try(origin); got != want {
			t.Errorf("Origin %q = %d, want %d", origin, got, want)
		}
		if reached != (want == http.StatusSwitchingProtocols) {
			t.Errorf("Origin %q: handler reached = %v", origin, reached)
		}
	}
}

func TestWebClientRouteHandler(t *testing.T) {
	ws := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ws")) })
	web := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("web")) })

	serve := func(h http.Handler, path string) string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Body.String()
	}

	withWeb := webClientRouteHandler(ws, web)
	for path, want := range map[string]string{
		"/":                   "web",
		"/index-O3QQwxZT.js":  "web",
		"/apiws":              "ws",
		"/apiws_test":         "ws",
		"/apiws_premium":      "ws",
		"/apiws_test_premium": "ws",
		"/apiws/":             "web", // not a WebSocket route, so not the ws handler's business
	} {
		if got := serve(withWeb, path); got != want {
			t.Errorf("with web client, GET %s = %q, want %q", path, got, want)
		}
	}

	// Without a web client nothing changes: every path still reaches ws.
	for _, path := range []string{"/", "/apiws", "/anything"} {
		if got := serve(webClientRouteHandler(ws, nil), path); got != "ws" {
			t.Errorf("without web client, GET %s = %q, want ws", path, got)
		}
	}
}
