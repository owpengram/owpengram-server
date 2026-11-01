// Package giftpacktest carries a small real gift pack for tests that need an
// upgradeable gift with a full collectible pool. The packs themselves are
// authored in the owpengram-giftpacks repository; the server only imports
// them, so its tests keep one trimmed archive of their own instead of
// depending on the generator.
package giftpacktest

import (
	_ "embed"
	"fmt"

	"telesrv/internal/app/giftpack"
)

//go:embed phone-pack.zip
var phonePackZip []byte

// PhonePackZip is the archive exactly as an operator would upload it.
func PhonePackZip() []byte {
	return append([]byte(nil), phonePackZip...)
}

// PhonePack parses the archive through the same path an upload takes: one
// upgradeable gift, "phone", with its models, patterns and backdrops.
func PhonePack() (giftpack.Manifest, *giftpack.ZipAssetResolver, error) {
	assets, err := giftpack.NewZipAssetResolver(phonePackZip)
	if err != nil {
		return giftpack.Manifest{}, nil, fmt.Errorf("open phone pack: %w", err)
	}
	raw, err := assets.Manifest()
	if err != nil {
		return giftpack.Manifest{}, nil, fmt.Errorf("read phone pack manifest: %w", err)
	}
	manifest, err := giftpack.ParseManifest(raw)
	if err != nil {
		return giftpack.Manifest{}, nil, fmt.Errorf("parse phone pack manifest: %w", err)
	}
	return manifest, assets, nil
}
