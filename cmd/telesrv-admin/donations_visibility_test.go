package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDonationsHiddenWhenDisabled mirrors the third-party-verification
// visibility test this codebase doesn't have a named test for yet, but
// should: TELESRV_DONATIONS_ENABLED=false must make every donations route
// 404 regardless of session permissions -- not just make the panel stop
// offering the nav entry. A session with "*" is deliberately used here,
// because the whole point of a feature switch (as opposed to a permission)
// is that no grant gets around it.
func TestDonationsHiddenWhenDisabled(t *testing.T) {
	srv, err := newServer(uiConfig{
		SessionKey:       []byte(testSessionKey),
		Password:         "letmein",
		Permissions:      []string{permissionAll},
		DonationsEnabled: false,
	}, nil, nil)
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	cookies, _ := signIn(t, srv)

	for _, route := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/donations/wallet"},
		{http.MethodGet, "/api/donations/chains"},
		{http.MethodGet, "/api/donations/deposits"},
		{http.MethodGet, "/api/donations/settings"},
		{http.MethodGet, "/api/donations/price-preview"},
		{http.MethodGet, "/api/donations/chains/polygon/balance"},
	} {
		rec := httptest.NewRecorder()
		req := withCookies(httptest.NewRequest(route.method, route.path, nil), cookies)
		srv.routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404 while donations are disabled", route.method, route.path, rec.Code)
		}
	}
}

// TestDonationsReachableWhenEnabled is the control: the same routes must NOT
// 404 once the feature is on (they still fail for their own reasons here,
// since this server has no read store wired up -- the point is that the
// failure is no longer specifically "hidden").
func TestDonationsReachableWhenEnabled(t *testing.T) {
	srv, err := newServer(uiConfig{
		SessionKey:       []byte(testSessionKey),
		Password:         "letmein",
		Permissions:      []string{permissionAll},
		DonationsEnabled: true,
	}, nil, nil)
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	cookies, _ := signIn(t, srv)

	rec := httptest.NewRecorder()
	req := withCookies(httptest.NewRequest(http.MethodGet, "/api/donations/wallet", nil), cookies)
	srv.routes().ServeHTTP(rec, req)
	if rec.Code == http.StatusNotFound {
		t.Fatalf("GET /api/donations/wallet = 404 while donations are enabled, want it to reach the handler")
	}
}

// TestSessionReportsDonationsEnabled pins the field the frontend reads to
// hide the nav entry (see cmd/telesrv-admin/web/src/permissions.tsx). Both
// /api/login and /api/session must agree with cfg, since the panel reads
// either depending on whether it just signed in or reloaded an existing
// session.
func TestSessionReportsDonationsEnabled(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		srv, err := newServer(uiConfig{
			SessionKey:       []byte(testSessionKey),
			Password:         "letmein",
			Permissions:      []string{permissionAll},
			DonationsEnabled: enabled,
		}, nil, nil)
		if err != nil {
			t.Fatalf("newServer: %v", err)
		}

		loginRec := httptest.NewRecorder()
		loginReq := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"owpengram","secret":"letmein"}`))
		srv.routes().ServeHTTP(loginRec, loginReq)
		if loginRec.Code != http.StatusOK {
			t.Fatalf("login status=%d body=%s", loginRec.Code, loginRec.Body.String())
		}
		var loginBody struct {
			DonationsEnabled bool `json:"donations_enabled"`
		}
		if err := json.Unmarshal(loginRec.Body.Bytes(), &loginBody); err != nil {
			t.Fatalf("decode login: %v", err)
		}
		if loginBody.DonationsEnabled != enabled {
			t.Errorf("login donations_enabled = %v, want %v", loginBody.DonationsEnabled, enabled)
		}

		cookies := loginRec.Result().Cookies()
		sessionRec := httptest.NewRecorder()
		sessionReq := withCookies(httptest.NewRequest(http.MethodGet, "/api/session", nil), cookies)
		srv.routes().ServeHTTP(sessionRec, sessionReq)
		if sessionRec.Code != http.StatusOK {
			t.Fatalf("session status=%d body=%s", sessionRec.Code, sessionRec.Body.String())
		}
		var sessionBody struct {
			DonationsEnabled bool `json:"donations_enabled"`
		}
		if err := json.Unmarshal(sessionRec.Body.Bytes(), &sessionBody); err != nil {
			t.Fatalf("decode session: %v", err)
		}
		if sessionBody.DonationsEnabled != enabled {
			t.Errorf("GET /api/session donations_enabled = %v, want %v", sessionBody.DonationsEnabled, enabled)
		}
	}
}
