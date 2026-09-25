package giftpack

import (
	"context"
	"fmt"
	"time"

	"telesrv/internal/domain"
)

// LibraryStore persists uploaded pack archives. Packs are keyed by their
// slug, so re-uploading a corrected build of the same pack replaces it
// instead of accumulating near-identical copies.
type LibraryStore interface {
	SaveGiftPack(ctx context.Context, pack domain.GiftPack) (domain.GiftPack, error)
	// GiftPacks lists every stored pack, metadata and manifest only --
	// never the archives, which are megabytes each.
	GiftPacks(ctx context.Context) ([]domain.GiftPack, error)
	// GiftPack loads one pack including its archive.
	GiftPack(ctx context.Context, packID string) (domain.GiftPack, bool, error)
	DeleteGiftPack(ctx context.Context, packID string) (bool, error)
}

// Library is the operator's uploaded-pack shelf: the packs the admin panel
// can preview and import from. It deliberately holds no built-in packs --
// a fresh server starts with an empty shelf, and packs arrive as archives
// an operator uploads.
type Library struct {
	store LibraryStore
	prep  Preparer
	now   func() time.Time
}

// NewLibrary wires the shelf. prep is the same animation validator the
// single-gift admin forms use, so a preview renders exactly the normalized
// Lottie an import would publish -- not the raw uploaded bytes.
func NewLibrary(store LibraryStore, prep Preparer) *Library {
	return &Library{store: store, prep: prep, now: time.Now}
}

// Save validates an uploaded archive and stores it. The manifest is parsed
// and every declared animation is resolved and validated up front, so a
// broken pack is rejected at upload time rather than sitting on the shelf
// looking importable.
func (l *Library) Save(ctx context.Context, fileName string, archive []byte, actor string) (domain.GiftPack, error) {
	if l == nil || l.store == nil {
		return domain.GiftPack{}, fmt.Errorf("gift pack library is not configured")
	}
	if len(archive) == 0 {
		return domain.GiftPack{}, fmt.Errorf("pack archive is empty")
	}
	if len(archive) > domain.MaxGiftPackArchiveBytes {
		return domain.GiftPack{}, fmt.Errorf("pack archive exceeds %d bytes", domain.MaxGiftPackArchiveBytes)
	}
	assets, err := NewZipAssetResolver(archive)
	if err != nil {
		return domain.GiftPack{}, err
	}
	manifestData, err := assets.Manifest()
	if err != nil {
		return domain.GiftPack{}, fmt.Errorf("pack.json: %w", err)
	}
	manifest, err := ParseManifest(manifestData)
	if err != nil {
		return domain.GiftPack{}, err
	}
	if err := l.validateAssets(manifest, assets); err != nil {
		return domain.GiftPack{}, err
	}
	packID := Slugify(manifest.PackName)
	if packID == "" {
		return domain.GiftPack{}, fmt.Errorf("pack manifest: pack_name has no usable characters")
	}
	return l.store.SaveGiftPack(ctx, domain.GiftPack{
		PackID:      packID,
		Name:        manifest.PackName,
		Author:      manifest.Author,
		Description: manifest.Description,
		FileName:    fileName,
		GiftCount:   len(manifest.Gifts),
		SizeBytes:   len(archive),
		Manifest:    manifestData,
		Archive:     archive,
		UploadedBy:  actor,
		UploadedAt:  l.now().UTC(),
	})
}

// validateAssets resolves and validates every animation the manifest
// references. Without this an archive missing one file imports most of its
// gifts and fails the rest halfway through, leaving a half-imported pack.
func (l *Library) validateAssets(manifest Manifest, assets AssetResolver) error {
	if l.prep == nil {
		return nil
	}
	for _, path := range AssetPaths(manifest) {
		if _, err := l.animation(manifest, assets, path); err != nil {
			return err
		}
	}
	return nil
}

// List is every stored pack, rendered for the panel.
func (l *Library) List(ctx context.Context) ([]PackSummary, error) {
	if l == nil || l.store == nil {
		return nil, fmt.Errorf("gift pack library is not configured")
	}
	packs, err := l.store.GiftPacks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PackSummary, 0, len(packs))
	for _, pack := range packs {
		manifest, err := ParseManifest(pack.Manifest)
		if err != nil {
			// A stored pack was validated on the way in; a manifest that no
			// longer parses means the row is corrupt, and hiding it would
			// make the shelf silently lose a pack.
			out = append(out, PackSummary{ID: pack.PackID, Name: pack.Name, Author: pack.Author,
				Description: "This pack's manifest is unreadable: " + err.Error(), Gifts: []GiftSummary{}})
			continue
		}
		out = append(out, Summarize(pack.PackID, manifest))
	}
	return out, nil
}

// Animation returns one gift's (or collectible attribute's) normalized
// Lottie JSON for the preview, resolved through the pack's own slug map --
// never through a caller-supplied archive path.
func (l *Library) Animation(ctx context.Context, packID, slug string) ([]byte, bool, error) {
	manifest, assets, found, err := l.load(ctx, packID)
	if err != nil || !found {
		return nil, found, err
	}
	path, ok := AssetPaths(manifest)[slug]
	if !ok {
		return nil, false, nil
	}
	data, err := l.animation(manifest, assets, path)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (l *Library) animation(manifest Manifest, assets AssetResolver, path string) ([]byte, error) {
	raw, err := assets.Open(path)
	if err != nil {
		return nil, err
	}
	prepared, err := l.prep.PrepareAnimation(path, raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return prepared.JSON, nil
}

// Delete removes a pack from the shelf. Gifts already imported from it are
// untouched -- they are catalog entries of their own by then.
func (l *Library) Delete(ctx context.Context, packID string) (bool, error) {
	if l == nil || l.store == nil {
		return false, fmt.Errorf("gift pack library is not configured")
	}
	return l.store.DeleteGiftPack(ctx, packID)
}

// Load returns a stored pack ready to import from.
func (l *Library) Load(ctx context.Context, packID string) (Manifest, AssetResolver, bool, error) {
	return l.load(ctx, packID)
}

func (l *Library) load(ctx context.Context, packID string) (Manifest, AssetResolver, bool, error) {
	if l == nil || l.store == nil {
		return Manifest{}, nil, false, fmt.Errorf("gift pack library is not configured")
	}
	pack, found, err := l.store.GiftPack(ctx, packID)
	if err != nil || !found {
		return Manifest{}, nil, found, err
	}
	assets, err := NewZipAssetResolver(pack.Archive)
	if err != nil {
		return Manifest{}, nil, true, err
	}
	manifest, err := ParseManifest(pack.Manifest)
	if err != nil {
		return Manifest{}, nil, true, err
	}
	return manifest, assets, true, nil
}
