package giftpack

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"

	stargiftsapp "telesrv/internal/app/stargifts"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

// fakeBlobs is a minimal in-memory stargifts.BlobBackend for tests -- no
// production BlobBackend implementation is convenient to reuse here (the
// real ones talk to a filesystem or S3).
type fakeBlobs struct {
	mu    sync.Mutex
	store map[string][]byte
}

func newFakeBlobs() *fakeBlobs { return &fakeBlobs{store: map[string][]byte{}} }

func (b *fakeBlobs) Name() string { return "test" }

func (b *fakeBlobs) Put(_ context.Context, data []byte) (string, error) {
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])
	b.mu.Lock()
	b.store[key] = append([]byte(nil), data...)
	b.mu.Unlock()
	return key, nil
}

func (b *fakeBlobs) Get(_ context.Context, objectKey string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.store[objectKey]
	if !ok {
		return nil, fmt.Errorf("blob %q not found", objectKey)
	}
	return data, nil
}

func newTestService(t *testing.T) *stargiftsapp.Service {
	t.Helper()
	return stargiftsapp.NewService(memory.NewStarGiftStore(), newFakeBlobs(), 2)
}

// testLottie returns a minimal, valid 512x512 Lottie JSON animation (a
// single red ellipse layer, 1s @ 30fps) -- just enough to pass
// stargifts.Service.PrepareAnimation's structural validation.
func testLottie() []byte {
	return []byte(`{
		"v": "5.7.4", "fr": 30, "ip": 0, "op": 30, "w": 512, "h": 512, "nm": "t", "ddd": 0,
		"assets": [],
		"layers": [{
			"ddd": 0, "ind": 1, "ty": 4, "nm": "L", "sr": 1,
			"ks": {
				"o": {"a": 0, "k": 100}, "r": {"a": 0, "k": 0},
				"p": {"a": 0, "k": [256, 256, 0]}, "a": {"a": 0, "k": [0, 0, 0]},
				"s": {"a": 0, "k": [100, 100, 100]}
			},
			"ao": 0,
			"shapes": [
				{"ty": "el", "nm": "E", "d": 1, "p": {"a": 0, "k": [0, 0]}, "s": {"a": 0, "k": [100, 100]}},
				{"ty": "fl", "nm": "F", "r": 1, "o": {"a": 0, "k": 100}, "c": {"a": 0, "k": [1, 0, 0]}}
			],
			"ip": 0, "op": 30, "st": 0, "bm": 0
		}]
	}`)
}

func fixtureAssets() MapAssetResolver {
	assets := MapAssetResolver{}
	for _, name := range []string{
		"basic.json", "upgradeable.json", "m1.json", "m2.json", "p1.json", "p2.json",
		"auction.json",
	} {
		assets[name] = testLottie()
	}
	return assets
}

func fixtureManifest() Manifest {
	return Manifest{
		PackName: "Test Pack",
		Gifts: []GiftSpec{
			{Title: "Basic", Stars: 15, BaseAnimation: "basic.json"},
			{
				Title: "Upgradeable", Stars: 25, ConvertStars: 25, BaseAnimation: "upgradeable.json",
				Upgrade: &UpgradeSpec{
					UpgradeStars: 50, SupplyTotal: 100, SlugPrefix: "up",
					Models: []AttrSpec{
						{Name: "M1", Animation: "m1.json", Permille: 500},
						{Name: "M2", Animation: "m2.json", Permille: 500},
					},
					Patterns: []AttrSpec{
						{Name: "P1", Animation: "p1.json", Permille: 500},
						{Name: "P2", Animation: "p2.json", Permille: 500},
					},
					Backdrops: []BackdropSpec{
						{Name: "B1", Center: "#ff0000", Edge: "#00ff00", Pattern: "#0000ff", Text: "#ffffff", Permille: 500},
						{Name: "B2", Center: "#111111", Edge: "#222222", Pattern: "#333333", Text: "#444444", Permille: 500},
					},
				},
			},
			{
				Title: "Auction", Stars: 10, BaseAnimation: "auction.json",
				Limited: true, Auction: true, AuctionSlug: "auction-test",
				GiftsPerRound: 5, AvailabilityTotal: 20,
			},
		},
	}
}

func TestImportCreatesEveryGift(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	result, err := Import(ctx, svc, fixtureManifest(), fixtureAssets(), ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Gifts) != 3 {
		t.Fatalf("Gifts = %d, want 3", len(result.Gifts))
	}
	for _, g := range result.Gifts {
		if g.Status != "created" {
			t.Errorf("gift %q status = %q, error=%q, want created", g.Title, g.Status, g.Error)
		}
		if g.GiftID == 0 {
			t.Errorf("gift %q GiftID = 0, want non-zero", g.Title)
		}
	}
	catalog, err := svc.Catalog(ctx)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(catalog) != 3 {
		t.Fatalf("catalog has %d gifts, want 3", len(catalog))
	}
	var auction, upgradeable domain.StarGift
	var sawAuction, sawUpgradeable bool
	for _, g := range catalog {
		switch g.Title {
		case "Auction":
			auction, sawAuction = g, true
		case "Upgradeable":
			upgradeable, sawUpgradeable = g, true
		}
	}
	if !sawAuction || !auction.Limited || !auction.Auction || auction.AuctionSlug != "auction-test" {
		t.Fatalf("auction gift = %+v (found=%v), want limited+auction with slug", auction, sawAuction)
	}
	if !sawUpgradeable || upgradeable.ConvertStars != 25 {
		t.Fatalf("upgradeable gift = %+v (found=%v), want convert_stars=25", upgradeable, sawUpgradeable)
	}
}

// TestImportMovesToAFreeSlugPrefix covers re-importing a pack after its old
// gifts were disabled: the new gift identity must not reuse the prefix the
// old one minted slugs under, or its first upgrade collides with "up-1".
func TestImportMovesToAFreeSlugPrefix(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	first, err := Import(ctx, svc, fixtureManifest(), fixtureAssets(), ImportOptions{})
	if err != nil {
		t.Fatalf("first Import: %v", err)
	}
	second := fixtureManifest()
	second.Gifts = second.Gifts[1:2]
	second.Gifts[0].Title = "Upgradeable Again"
	third := fixtureManifest()
	third.Gifts = third.Gifts[1:2]
	third.Gifts[0].Title = "Upgradeable Third"

	for i, tc := range []struct {
		manifest Manifest
		want     string
	}{{second, "up-2"}, {third, "up-3"}} {
		result, err := Import(ctx, svc, tc.manifest, fixtureAssets(), ImportOptions{})
		if err != nil || len(result.Gifts) != 1 || result.Gifts[0].Status != "created" {
			t.Fatalf("import #%d = %+v err %v, want created", i+2, result, err)
		}
		preview, found, err := svc.CollectiblePreview(ctx, result.Gifts[0].GiftID)
		if err != nil || !found || preview.SlugPrefix != tc.want {
			t.Fatalf("import #%d slug prefix = %q found %v err %v, want %q", i+2, preview.SlugPrefix, found, err, tc.want)
		}
	}
	original, _, _ := svc.CollectiblePreview(ctx, first.Gifts[1].GiftID)
	if original.SlugPrefix != "up" {
		t.Fatalf("original gift prefix = %q, want it untouched as \"up\"", original.SlugPrefix)
	}
	catalog, _ := svc.Catalog(ctx)
	if len(catalog) != 5 {
		t.Fatalf("catalog has %d gifts, want 5 (no orphan left by a rejected prefix)", len(catalog))
	}
}

func TestImportIsIdempotentByTitle(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	manifest := fixtureManifest()
	if _, err := Import(ctx, svc, manifest, fixtureAssets(), ImportOptions{}); err != nil {
		t.Fatalf("first Import: %v", err)
	}
	result, err := Import(ctx, svc, manifest, fixtureAssets(), ImportOptions{})
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	for _, g := range result.Gifts {
		if g.Status != "skipped" {
			t.Errorf("gift %q status = %q on re-import, want skipped", g.Title, g.Status)
		}
	}
	catalog, err := svc.Catalog(ctx)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(catalog) != 3 {
		t.Fatalf("catalog has %d gifts after re-import, want 3 (no duplicates)", len(catalog))
	}
}

func TestImportDryRunWritesNothing(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	result, err := Import(ctx, svc, fixtureManifest(), fixtureAssets(), ImportOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	for _, g := range result.Gifts {
		if g.Status != "would_create" {
			t.Errorf("gift %q status = %q, want would_create", g.Title, g.Status)
		}
	}
	catalog, err := svc.Catalog(ctx)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(catalog) != 0 {
		t.Fatalf("catalog has %d gifts after dry-run, want 0", len(catalog))
	}
}

func TestImportReportsInvalidAuctionAsFailedWithoutAbortingPack(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	manifest := Manifest{
		PackName: "Bad Pack",
		Gifts: []GiftSpec{
			{Title: "Good", Stars: 15, BaseAnimation: "basic.json"},
			// Auction=true with no slug/gifts_per_round/availability_total:
			// ValidateLifecycleAuthoring must reject this.
			{Title: "BadAuction", Stars: 10, BaseAnimation: "basic.json", Auction: true},
		},
	}
	assets := MapAssetResolver{"basic.json": testLottie()}
	result, err := Import(ctx, svc, manifest, assets, ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.Gifts[0].Status != "created" {
		t.Errorf("Good gift status = %q, want created", result.Gifts[0].Status)
	}
	if result.Gifts[1].Status != "failed" || result.Gifts[1].Error == "" {
		t.Errorf("BadAuction gift = %+v, want failed with an error message", result.Gifts[1])
	}
	catalog, err := svc.Catalog(ctx)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(catalog) != 1 {
		t.Fatalf("catalog has %d gifts, want 1 (only Good)", len(catalog))
	}
}

func TestImportOptionsNowOverridesClock(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	manifest := Manifest{PackName: "Clock", Gifts: []GiftSpec{{Title: "Basic", Stars: 5, BaseAnimation: "basic.json"}}}
	assets := MapAssetResolver{"basic.json": testLottie()}
	if _, err := Import(ctx, svc, manifest, assets, ImportOptions{Now: func() time.Time { return fixed }}); err != nil {
		t.Fatalf("Import: %v", err)
	}
}
