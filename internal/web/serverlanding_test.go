package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"telesrv/internal/identity"
)

func TestServerLandingOffersAddServerLink(t *testing.T) {
	store := identity.NewStore(t.TempDir())
	if err := store.SetText("HomeGram", "Our family server"); err != nil {
		t.Fatalf("SetText: %v", err)
	}
	h, err := NewHandler(Config{
		StickerSets:   fakeResolver{},
		PublicBaseURL: "https://me.example.org",
		AppScheme:     "owpg",
		AppName:       "OwpenGram",
		Identity:      store,
		AdvertiseHost: "203.0.113.7",
		ServerPort:    2398,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"HomeGram",
		"Our family server",
		"203.0.113.7:2398",
		"Add to OwpenGram",
		// html/template escapes & in attributes.
		"owpg://addserver?host=203.0.113.7&amp;port=2398",
		`<link rel="canonical" href="https://me.example.org/">`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func TestServerLandingWithoutAddressOffersDownload(t *testing.T) {
	h, err := NewHandler(Config{
		StickerSets:   fakeResolver{},
		PublicBaseURL: "https://me.example.org",
		AppScheme:     "owpg",
		AppName:       "OwpenGram",
		DownloadURL:   "https://owpengram.org/download",
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rr.Body.String()
	if strings.Contains(body, "addserver") {
		t.Fatalf("no advertised address, yet the page links addserver:\n%s", body)
	}
	if !strings.Contains(body, "Get OwpenGram") {
		t.Fatalf("want the download fallback button:\n%s", body)
	}
}

func TestServerIconRefusesNonRasterTypes(t *testing.T) {
	store := identity.NewStore(t.TempDir())
	if err := store.SetIcon([]byte("<svg onload=alert(1)>"), ".svg"); err != nil {
		t.Fatalf("SetIcon: %v", err)
	}
	h, err := NewHandler(Config{StickerSets: fakeResolver{}, PublicBaseURL: "https://me.example.org", Identity: store})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/_public/server-icon", nil))
	if got := rr.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q, want the brand logo's image/png", got)
	}
	if strings.Contains(rr.Body.String(), "<svg") {
		t.Fatal("an SVG icon was served as-is")
	}
}
