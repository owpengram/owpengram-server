package web

import (
	_ "embed"
	"net/http"
	"strings"
)

//go:embed assets/logo.png
var brandLogoPNG []byte

//go:embed assets/fonts/plus-jakarta-sans-400.woff2
var fontJakarta400 []byte

//go:embed assets/fonts/plus-jakarta-sans-500.woff2
var fontJakarta500 []byte

//go:embed assets/fonts/plus-jakarta-sans-600.woff2
var fontJakarta600 []byte

//go:embed assets/fonts/plus-jakarta-sans-700.woff2
var fontJakarta700 []byte

//go:embed assets/fonts/plus-jakarta-sans-800.woff2
var fontJakarta800 []byte

var publicFonts = map[string][]byte{
	"plus-jakarta-sans-400.woff2": fontJakarta400,
	"plus-jakarta-sans-500.woff2": fontJakarta500,
	"plus-jakarta-sans-600.woff2": fontJakarta600,
	"plus-jakarta-sans-700.woff2": fontJakarta700,
	"plus-jakarta-sans-800.woff2": fontJakarta800,
}

func (h *handler) brandLogo(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
	_, _ = w.Write(brandLogoPNG)
}

func (h *handler) brandFont(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("file"))
	data, ok := publicFonts[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "font/woff2")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write(data)
}
