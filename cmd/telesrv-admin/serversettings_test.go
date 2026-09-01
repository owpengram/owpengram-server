package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"telesrv/internal/identity"
)

func TestServerIdentityAPIReportsOverridesAndDefaultsSeparately(t *testing.T) {
	store := identity.NewStore(t.TempDir())
	srv := &server{
		cfg: uiConfig{
			WelcomeMessagePhoneDefault: "env phone default",
			WelcomeMessageEmailDefault: "env email default",
		},
		identity: store,
	}

	// Before any override: GET must report empty raw fields (so the UI can
	// tell "unset" apart from "explicitly set to the same text as the
	// default"), alongside the effective default text.
	req := httptest.NewRequest(http.MethodGet, "/api/server/identity", nil)
	rec := httptest.NewRecorder()
	srv.handleServerIdentityAPI(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp serverIdentityAPIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.WelcomeMessagePhoneTemplate != "" || resp.WelcomeMessageEmailTemplate != "" {
		t.Fatalf("expected empty overrides before any Set, got %+v", resp)
	}
	if resp.DefaultWelcomeMessagePhoneTemplate != "env phone default" || resp.DefaultWelcomeMessageEmailTemplate != "env email default" {
		t.Fatalf("expected effective defaults from cfg, got %+v", resp)
	}

	// Set an override for phone only.
	setReq := httptest.NewRequest(http.MethodPost, "/api/actions/set-welcome-message-templates", strings.NewReader(`{
		"reason": "test",
		"confirm": true,
		"phone_template": "custom phone template"
	}`))
	setRec := httptest.NewRecorder()
	srv.handleSetWelcomeMessageTemplatesAPI(setRec, setReq)
	if setRec.Code != http.StatusOK {
		t.Fatalf("SET status = %d, body = %s", setRec.Code, setRec.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/server/identity", nil)
	rec2 := httptest.NewRecorder()
	srv.handleServerIdentityAPI(rec2, req2)
	var resp2 serverIdentityAPIResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp2.WelcomeMessagePhoneTemplate != "custom phone template" {
		t.Fatalf("expected phone override to be set, got %+v", resp2)
	}
	if resp2.WelcomeMessageEmailTemplate != "" {
		t.Fatalf("expected email override to stay unset, got %+v", resp2)
	}

	// Reset (empty string) clears the override back to "unset".
	resetReq := httptest.NewRequest(http.MethodPost, "/api/actions/set-welcome-message-templates", strings.NewReader(`{
		"reason": "test",
		"confirm": true,
		"phone_template": ""
	}`))
	resetRec := httptest.NewRecorder()
	srv.handleSetWelcomeMessageTemplatesAPI(resetRec, resetReq)
	if resetRec.Code != http.StatusOK {
		t.Fatalf("reset status = %d, body = %s", resetRec.Code, resetRec.Body.String())
	}
	info, err := store.Get()
	if err != nil {
		t.Fatal(err)
	}
	if info.WelcomeMessagePhoneTemplate != "" {
		t.Fatalf("expected phone override cleared after reset, got %+v", info)
	}
}
