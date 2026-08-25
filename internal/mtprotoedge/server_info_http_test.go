package mtprotoedge

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"telesrv/internal/identity"
)

func TestServeServerInfoIncludesIdentity(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pem := rsaPublicKeyPEM(key)

	store := identity.NewStore(t.TempDir())
	if err := store.SetText("OwpenGram", "A self-hosted server."); err != nil {
		t.Fatal(err)
	}

	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	h := serverInfoHTTPHandler(fallback, 2, pem, store)

	req := httptest.NewRequest(http.MethodGet, ServerInfoPath, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp ServerInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Name != "OwpenGram" || resp.Description != "A self-hosted server." {
		t.Fatalf("got %+v", resp)
	}
	if resp.HasIcon {
		t.Fatal("has_icon should be false before any upload")
	}
}

func TestServeServerInfoWithoutIdentityStore(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pem := rsaPublicKeyPEM(key)

	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	h := serverInfoHTTPHandler(fallback, 2, pem, nil)

	req := httptest.NewRequest(http.MethodGet, ServerInfoPath, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp ServerInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Name != "" || resp.Description != "" || resp.HasIcon {
		t.Fatalf("expected empty identity fields when store is nil, got %+v", resp)
	}
	if resp.RSAPublicKeyPEM == "" {
		t.Fatal("RSA key should still serve when identity store is nil")
	}
}

func TestServeServerIcon(t *testing.T) {
	store := identity.NewStore(t.TempDir())
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	h := serverInfoHTTPHandler(fallback, 2, []byte("pem"), store)

	// No icon configured yet -> 404.
	req := httptest.NewRequest(http.MethodGet, ServerIconPath, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 before upload", rec.Code)
	}

	png := []byte{0x89, 'P', 'N', 'G', 1, 2, 3, 4}
	if err := store.SetIcon(png, ".png"); err != nil {
		t.Fatal(err)
	}

	req = httptest.NewRequest(http.MethodGet, ServerIconPath, nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != string(png) {
		t.Fatal("icon body mismatch")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q", ct)
	}
}
