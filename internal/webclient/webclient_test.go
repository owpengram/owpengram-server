package webclient

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func testHandler() http.Handler {
	big := strings.Repeat("console.log('owpengram');\n", 100)
	return newHandler(fstest.MapFS{
		"index.html":                    {Data: []byte("<!doctype html><title>web</title>")},
		"index-O3QQwxZT.js":             {Data: []byte(big)},
		"sw-DqajiDi8.js":                {Data: []byte(big)},
		"site.webmanifest":              {Data: []byte(`{"name":"x"}`)},
		"assets/fonts/tgico.woff":       {Data: []byte("woff-bytes")},
		"tlottie-VsBq3Bx4.wasm":         {Data: []byte("\x00asm")},
		"assets/img/logo.png":           {Data: []byte("png-bytes")},
		"changelogs/en_2.2.md":          {Data: []byte("# changes")},
		"dir/inner.txt":                 {Data: []byte("x")},
		"nested/deep/file-ABCDEFGH.css": {Data: []byte("body{}")},
	})
}

func get(t *testing.T, h http.Handler, method, target string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRootServesIndex(t *testing.T) {
	rec := get(t, testHandler(), http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>web</title>") {
		t.Fatalf("GET / = %d %q, want index.html", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("index.html Cache-Control = %q, want no-cache", got)
	}
}

// A module or worker script with the wrong type is refused by the browser, and
// the host's own type table (Windows: the registry) cannot be trusted for it.
func TestContentTypes(t *testing.T) {
	h := testHandler()
	for target, want := range map[string]string{
		"/index-O3QQwxZT.js":       "text/javascript; charset=utf-8",
		"/site.webmanifest":        "application/manifest+json; charset=utf-8",
		"/assets/fonts/tgico.woff": "font/woff",
		"/tlottie-VsBq3Bx4.wasm":   "application/wasm",
		"/assets/img/logo.png":     "image/png",
		"/changelogs/en_2.2.md":    "text/markdown; charset=utf-8",
	} {
		rec := get(t, h, http.MethodGet, target, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", target, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != want {
			t.Errorf("GET %s Content-Type = %q, want %q", target, got, want)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s X-Content-Type-Options = %q", target, got)
		}
	}
}

func TestCachePolicyFollowsContentHash(t *testing.T) {
	h := testHandler()
	for target, want := range map[string]string{
		"/index-O3QQwxZT.js":             "public, max-age=31536000, immutable",
		"/sw-DqajiDi8.js":                "public, max-age=31536000, immutable",
		"/nested/deep/file-ABCDEFGH.css": "public, max-age=31536000, immutable",
		"/site.webmanifest":              "no-cache",
		"/assets/img/logo.png":           "no-cache",
	} {
		if got := get(t, h, http.MethodGet, target, nil).Header().Get("Cache-Control"); got != want {
			t.Errorf("GET %s Cache-Control = %q, want %q", target, got, want)
		}
	}
}

func TestETagRevalidation(t *testing.T) {
	h := testHandler()
	first := get(t, h, http.MethodGet, "/site.webmanifest", nil)
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	again := get(t, h, http.MethodGet, "/site.webmanifest", http.Header{"If-None-Match": {etag}})
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Fatalf("revalidation = %d with %d body bytes, want 304 and none", again.Code, again.Body.Len())
	}
	stale := get(t, h, http.MethodGet, "/site.webmanifest", http.Header{"If-None-Match": {`"other"`}})
	if stale.Code != http.StatusOK {
		t.Fatalf("stale ETag = %d, want 200", stale.Code)
	}
}

func TestGzipOnlyWhenAcceptedAndSmaller(t *testing.T) {
	h := testHandler()
	plain := get(t, h, http.MethodGet, "/index-O3QQwxZT.js", nil)
	if plain.Header().Get("Content-Encoding") != "" {
		t.Fatal("compressed for a client that did not ask")
	}

	rec := get(t, h, http.MethodGet, "/index-O3QQwxZT.js", http.Header{"Accept-Encoding": {"br, gzip;q=0.8"}})
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("not compressed for a gzip client")
	}
	zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip body: %v", err)
	}
	got, _ := io.ReadAll(zr)
	if !bytes.Equal(got, plain.Body.Bytes()) {
		t.Fatal("gzip body differs from the plain one")
	}
	if rec.Body.Len() >= plain.Body.Len() {
		t.Fatalf("gzip %d bytes is not smaller than %d", rec.Body.Len(), plain.Body.Len())
	}

	if refused := get(t, h, http.MethodGet, "/index-O3QQwxZT.js", http.Header{"Accept-Encoding": {"gzip;q=0"}}); refused.Header().Get("Content-Encoding") != "" {
		t.Fatal("compressed despite gzip;q=0")
	}
	// Already-compressed formats are never touched.
	if img := get(t, h, http.MethodGet, "/assets/img/logo.png", http.Header{"Accept-Encoding": {"gzip"}}); img.Header().Get("Content-Encoding") != "" {
		t.Fatal("compressed a png")
	}
}

func TestUnknownPathsAndDirectories(t *testing.T) {
	h := testHandler()
	for _, target := range []string{"/nope.js", "/dir", "/dir/", "/assets", "/../../etc/passwd", "/%2e%2e/index.html"} {
		rec := get(t, h, http.MethodGet, target, nil)
		// path.Clean folds the traversal ones back inside the root, so they
		// may resolve to a real file -- what must never happen is a 5xx or a
		// directory listing.
		if rec.Code >= 500 || strings.Contains(rec.Body.String(), "<pre>") {
			t.Fatalf("GET %s = %d %q", target, rec.Code, rec.Body.String())
		}
	}
	for _, target := range []string{"/nope.js", "/dir", "/dir/", "/assets"} {
		if rec := get(t, h, http.MethodGet, target, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", target, rec.Code)
		}
	}
}

func TestMethods(t *testing.T) {
	h := testHandler()
	head := get(t, h, http.MethodHead, "/index-O3QQwxZT.js", nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") == "" {
		t.Fatalf("HEAD = %d with %d body bytes and Content-Length %q", head.Code, head.Body.Len(), head.Header().Get("Content-Length"))
	}
	post := get(t, h, http.MethodPost, "/", nil)
	if post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST = %d Allow %q, want 405 GET, HEAD", post.Code, post.Header().Get("Allow"))
	}
}

func TestPlaceholderOnlyIsNotAvailable(t *testing.T) {
	if available(fstest.MapFS{".gitkeep": {}}) {
		t.Fatal("a tree without index.html counts as a web client")
	}
	if !available(fstest.MapFS{"index.html": {Data: []byte("x")}}) {
		t.Fatal("a tree with index.html is not seen")
	}
}

// A page asks for hundreds of files at once on a cold start; every one has to be
// answered correctly and read only once, however many ask for it together.
func TestConcurrentColdRequests(t *testing.T) {
	files := fstest.MapFS{}
	for i := 0; i < 40; i++ {
		files[fmt.Sprintf("chunk-%08d.js", i)] = &fstest.MapFile{Data: []byte(strings.Repeat(fmt.Sprintf("var a%d=1;", i), 400))}
	}
	h := newHandler(files)

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		for n := 0; n < 5; n++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				rec := get(t, h, http.MethodGet, fmt.Sprintf("/chunk-%08d.js", i), http.Header{"Accept-Encoding": {"gzip"}})
				if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "gzip" {
					t.Errorf("chunk %d = %d encoding %q", i, rec.Code, rec.Header().Get("Content-Encoding"))
				}
			}(i)
		}
	}
	wg.Wait()

	if got := len(h.(*handler).slots); got != 40 {
		t.Fatalf("slots = %d, want one per file (40)", got)
	}
}
