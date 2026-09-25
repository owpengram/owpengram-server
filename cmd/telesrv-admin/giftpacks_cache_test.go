package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGiftPackReadsAreNotCached pins that the pack shelf and its previews are
// served uncached.
//
// This is a real bug that shipped: the shelf was proxied with the default
// "private, max-age=30", so an operator who uploaded a second pack within
// half a minute of the first watched the panel re-read the list and get the
// browser's copy from *before* the upload -- the pack was in the database
// and simply never appeared. Re-uploading it did not help, because every
// retry landed inside the same cache window. The same applies to a pack's
// animations: re-uploading a pack under the same name replaces it in place,
// so those URLs are mutable too and a cached copy shows the old art exactly
// when someone is checking whether their fix took.
func TestGiftPackReadsAreNotCached(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"packs":[]}`))
	}))
	t.Cleanup(upstream.Close)

	s := &server{cfg: uiConfig{AdminAPIURL: upstream.URL}}
	for _, tc := range []struct {
		name string
		call func(http.ResponseWriter, *http.Request)
		path string
	}{
		{"shelf listing", s.handleGiftPacksAPI, "/api/gift-packs"},
		{"pack animation", s.handleGiftPackAnimationAPI, "/api/gift-packs/p/animations/g"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.SetPathValue("pack_id", "p")
			req.SetPathValue("slug", "g")
			rec := httptest.NewRecorder()
			tc.call(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			cache := rec.Header().Get("Cache-Control")
			if !strings.Contains(cache, "no-store") {
				t.Fatalf("Cache-Control = %q, want no-store: a cached %s hides an upload that already happened", cache, tc.path)
			}
		})
	}
}
