package giftpack

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strings"
)

// maxAssetBytes bounds a single asset read from a pack; PrepareAnimation
// enforces the real (tighter) per-format ceilings afterwards, this is just a
// defensive cap against a hostile zip entry before that point.
const maxAssetBytes = 8 << 20

// AssetResolver resolves a path referenced by a Manifest (e.g.
// GiftSpec.BaseAnimation, AttrSpec.Animation) to its file bytes.
type AssetResolver interface {
	Open(path string) ([]byte, error)
}

// ZipAssetResolver resolves paths against an uploaded pack .zip. pack.json
// is expected at the archive root.
type ZipAssetResolver struct {
	zr *zip.Reader
}

// NewZipAssetResolver opens a pack archive from raw zip bytes.
func NewZipAssetResolver(data []byte) (*ZipAssetResolver, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open pack zip: %w", err)
	}
	return &ZipAssetResolver{zr: zr}, nil
}

// Manifest returns the raw bytes of pack.json.
func (z *ZipAssetResolver) Manifest() ([]byte, error) {
	return z.Open("pack.json")
}

func (z *ZipAssetResolver) Open(path string) ([]byte, error) {
	path = strings.TrimPrefix(strings.TrimSpace(path), "/")
	f, err := z.zr.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q in pack: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxAssetBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %q in pack: %w", path, err)
	}
	if len(data) > maxAssetBytes {
		return nil, fmt.Errorf("%q in pack exceeds %d bytes", path, maxAssetBytes)
	}
	return data, nil
}

// MapAssetResolver resolves paths against an in-memory map, used by the
// built-in packs (internal/seed/giftpacks), which generate their assets
// procedurally rather than shipping real files.
type MapAssetResolver map[string][]byte

func (m MapAssetResolver) Open(path string) ([]byte, error) {
	data, ok := m[path]
	if !ok {
		return nil, fmt.Errorf("asset %q not found in pack", path)
	}
	return data, nil
}
