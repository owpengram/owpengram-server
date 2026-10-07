// Package webclient serves the OwpenGram web client (the tweb fork in
// owpengram-web-client) out of the server binary, on the same port as MTProto.
//
// What is embedded is dist/, produced by `owpengram-web-client`'s
// `node scripts/owpengram-web.mjs embed`: the embed build of the client plus
// its static files. It is too big to commit (tens of MB), so a checkout
// without a build embeds only the placeholder and Available reports false --
// the server then simply does not serve a web client.
package webclient

import (
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

//go:embed all:dist
var embedded embed.FS

// indexFile is the entry point and also what tells a real build from the
// placeholder-only checkout.
const indexFile = "index.html"

// Available reports whether a web client build is embedded in this binary.
func Available() bool {
	return available(distRoot())
}

// Handler serves the embedded web client, or nil when none is embedded.
func Handler() http.Handler {
	root := distRoot()
	if !available(root) {
		return nil
	}
	return newHandler(root)
}

func distRoot() fs.FS {
	root, err := fs.Sub(embedded, "dist")
	if err != nil {
		// Unreachable: "dist" is a literal embed pattern, so it always exists.
		panic(err)
	}
	return root
}

func available(root fs.FS) bool {
	info, err := fs.Stat(root, indexFile)
	return err == nil && !info.IsDir()
}

// hashedName matches the content-hashed names Vite gives its output
// (index-O3QQwxZT.css, sw-DqajiDi8.js): a name that changes with the content
// can be cached forever, anything else has to be revalidated.
var hashedName = regexp.MustCompile(`-[A-Za-z0-9_-]{8}\.[A-Za-z0-9.]+$`)

// contentTypes is consulted before the mime package on purpose: on Windows
// that reads the registry, where .js is often text/plain -- which a browser
// refuses for a module or worker script.
var contentTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json; charset=utf-8",
	".webmanifest": "application/manifest+json; charset=utf-8",
	".xml":         "application/xml; charset=utf-8",
	".md":          "text/markdown; charset=utf-8",
	".txt":         "text/plain; charset=utf-8",
	".svg":         "image/svg+xml",
	".wasm":        "application/wasm",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".ttf":         "font/ttf",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".webp":        "image/webp",
	".gif":         "image/gif",
	".ico":         "image/x-icon",
	".mp3":         "audio/mpeg",
	".ogg":         "audio/ogg",
	".mp4":         "video/mp4",
	".webm":        "video/webm",
}

// compressible lists what is worth gzipping; the rest (images, fonts, wasm
// already carry their own compression or do not shrink).
var compressible = map[string]bool{
	".html": true, ".js": true, ".mjs": true, ".css": true, ".json": true,
	".webmanifest": true, ".xml": true, ".md": true, ".txt": true, ".svg": true,
	".wasm": true, ".ttf": true,
}

type entry struct {
	body []byte
	gz   []byte // nil when not compressible or not smaller
	etag string
	typ  string
	ext  string
	name string
}

// slot is one file's place in the cache. Each file has its own, so reading and
// compressing one never makes a request for another wait.
type slot struct {
	once  sync.Once
	entry *entry // nil: does not exist
}

type handler struct {
	root fs.FS

	mu    sync.Mutex
	slots map[string]*slot
}

func newHandler(root fs.FS) http.Handler {
	return &handler{root: root, slots: map[string]*slot{}}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = indexFile
	}
	e := h.load(name)
	if e == nil {
		http.NotFound(w, r)
		return
	}

	hdr := w.Header()
	hdr.Set("Content-Type", e.typ)
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("ETag", e.etag)
	if hashedName.MatchString(e.name) {
		hdr.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// Same-named files change between builds (index.html, manifests,
		// the icons): always ask, and let the ETag answer.
		hdr.Set("Cache-Control", "no-cache")
	}
	if e.gz != nil {
		hdr.Add("Vary", "Accept-Encoding")
	}
	if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, e.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	body := e.body
	if e.gz != nil && acceptsGzip(r) {
		body = e.gz
		hdr.Set("Content-Encoding", "gzip")
	}
	// Explicit length: keeps net/http from chunk-encoding, and the HEAD
	// response reports the same size the GET would.
	hdr.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// load reads a file once and keeps it (and its gzip form) for the life of the
// process: the content is immutable, being part of the binary.
func (h *handler) load(name string) *entry {
	h.mu.Lock()
	s, ok := h.slots[name]
	if !ok {
		s = &slot{}
		h.slots[name] = s
	}
	h.mu.Unlock()

	// A page asks for hundreds of files at once on a cold start. The map lock
	// above is held only to find the slot; the work happens here, per file.
	s.once.Do(func() { s.entry = h.read(name) })
	return s.entry
}

func (h *handler) read(name string) *entry {
	if !fs.ValidPath(name) {
		return nil
	}
	info, err := fs.Stat(h.root, name)
	if err != nil || info.IsDir() {
		return nil
	}
	body, err := fs.ReadFile(h.root, name)
	if err != nil {
		return nil
	}
	ext := strings.ToLower(path.Ext(name))
	typ := contentTypes[ext]
	if typ == "" {
		typ = mime.TypeByExtension(ext)
	}
	if typ == "" {
		typ = "application/octet-stream"
	}
	sum := sha1.Sum(body)
	e := &entry{
		body: body,
		etag: `"` + hex.EncodeToString(sum[:8]) + `"`,
		typ:  typ,
		ext:  ext,
		name: name,
	}
	if compressible[ext] && len(body) > 512 {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
		_, _ = zw.Write(body)
		if zw.Close() == nil && buf.Len() < len(body) {
			e.gz = buf.Bytes()
		}
	}
	return e
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		token, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(token), "gzip") {
			continue
		}
		params = strings.ReplaceAll(params, " ", "")
		if value, ok := strings.CutPrefix(strings.ToLower(params), "q="); ok {
			q, err := strconv.ParseFloat(value, 64)
			return err != nil || q > 0
		}
		return true
	}
	return false
}

func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}
