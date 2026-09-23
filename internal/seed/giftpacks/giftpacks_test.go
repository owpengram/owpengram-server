package giftpacks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"telesrv/internal/app/giftpack"
	stargiftsapp "telesrv/internal/app/stargifts"
	"telesrv/internal/store/memory"
)

type fakeBlobs struct {
	mu    sync.Mutex
	store map[string][]byte
}

func newFakeBlobs() *fakeBlobs    { return &fakeBlobs{store: map[string][]byte{}} }
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

func TestPacksImportCleanlyAndIdempotently(t *testing.T) {
	if len(List()) == 0 {
		t.Fatal("no built-in packs registered")
	}
	for _, p := range List() {
		t.Run(p.ID, func(t *testing.T) {
			svc := stargiftsapp.NewService(memory.NewStarGiftStore(), newFakeBlobs(), 2)
			manifest, assets, ok := Manifest(p.ID)
			if !ok {
				t.Fatal("Manifest: pack not found")
			}
			ctx := context.Background()
			result, err := giftpack.Import(ctx, svc, manifest, assets, giftpack.ImportOptions{})
			if err != nil {
				t.Fatalf("Import: %v", err)
			}
			if len(result.Gifts) != len(p.Gifts) {
				t.Fatalf("imported %d gifts, want %d", len(result.Gifts), len(p.Gifts))
			}
			for _, g := range result.Gifts {
				if g.Status != "created" {
					t.Errorf("gift %q status = %q error=%q, want created", g.Title, g.Status, g.Error)
				}
			}
			// Every mechanic a gift declares must survive the import into the
			// live catalog -- that's what makes the pack useful for testing.
			catalog, err := svc.Catalog(ctx)
			if err != nil {
				t.Fatalf("Catalog: %v", err)
			}
			byTitle := map[string]bool{}
			for _, got := range catalog {
				byTitle[got.Title] = true
				var want giftpack.GiftSpec
				for _, g := range manifest.Gifts {
					if g.Title == got.Title {
						want = g
					}
				}
				if got.Auction != want.Auction || got.AuctionSlug != want.AuctionSlug || got.GiftsPerRound != want.GiftsPerRound ||
					got.Limited != (want.Limited || want.AvailabilityTotal > 0) || got.AvailabilityTotal != want.AvailabilityTotal ||
					got.RequirePremium != want.RequirePremium || got.Birthday != want.Birthday || got.SupportOnly != want.SupportOnly ||
					got.LimitedPerUser != want.LimitedPerUser || got.PerUserTotal != want.PerUserTotal ||
					got.ResellMinStars != want.ResellMinStars || got.ConvertStars != want.ConvertStars {
					t.Errorf("gift %q imported as %+v, want mechanics of %+v", got.Title, got, want)
				}
				if upgradeable := want.Upgrade != nil; upgradeable != (got.UpgradeStars > 0) {
					t.Errorf("gift %q UpgradeStars = %d, upgradeable = %v", got.Title, got.UpgradeStars, upgradeable)
				}
			}
			for _, g := range manifest.Gifts {
				if !byTitle[g.Title] {
					t.Errorf("gift %q missing from catalog", g.Title)
				}
			}

			again, err := giftpack.Import(ctx, svc, manifest, assets, giftpack.ImportOptions{})
			if err != nil {
				t.Fatalf("re-Import: %v", err)
			}
			for _, g := range again.Gifts {
				if g.Status != "skipped" {
					t.Errorf("gift %q status = %q on re-import, want skipped", g.Title, g.Status)
				}
			}
		})
	}
}

func TestListIsConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range List() {
		if seen[p.ID] {
			t.Errorf("duplicate pack id %q", p.ID)
		}
		seen[p.ID] = true
		if p.Name == "" || p.Description == "" {
			t.Errorf("pack %q is missing name or description", p.ID)
		}
		if _, ok := Animation(p.ID, p.Icon); !ok {
			t.Errorf("pack %q icon %q is not one of its gifts", p.ID, p.Icon)
		}
		for _, g := range p.Gifts {
			if _, ok := Animation(p.ID, g.Slug); !ok {
				t.Errorf("pack %q gift %q has no animation", p.ID, g.Slug)
			}
			if g.Upgrade == nil {
				continue
			}
			for _, a := range append(append([]AttrSummary{}, g.Upgrade.Models...), g.Upgrade.Patterns...) {
				if _, ok := Animation(p.ID, a.ID); !ok {
					t.Errorf("pack %q gift %q attribute %q has no animation", p.ID, g.Slug, a.ID)
				}
			}
			for _, b := range g.Upgrade.Backdrops {
				if b.Center == "" || b.Edge == "" {
					t.Errorf("pack %q gift %q backdrop %q has no colours", p.ID, g.Slug, b.Name)
				}
			}
		}
	}
	if _, ok := Animation("no-such-pack", "x"); ok {
		t.Error("Animation found a gift in an unknown pack")
	}
}

// TestAnimationsFollowRlottieContract pins the structural rules a generic
// Lottie player forgives but rlottie does not (see lottie.go's doc comment).
// Each of these once shipped broken art that only showed up in the client.
func TestAnimationsFollowRlottieContract(t *testing.T) {
	for _, p := range registry {
		want := len(p.gifts)
		seen := map[string]bool{}
		for _, g := range p.gifts {
			if g.upgrade == nil {
				continue
			}
			for _, a := range append(append([]attrDef{}, g.upgrade.models...), g.upgrade.patterns...) {
				if !seen[a.id] {
					seen[a.id] = true
					want++
				}
			}
		}
		if got := len(p.assetIDs()); got != want {
			t.Errorf("pack %q renders %d animations, want %d (an attribute id collides with a gift slug?)", p.id, got, want)
		}
		for _, id := range p.assetIDs() {
			raw, _ := Animation(p.id, id)
			name := p.id + "/" + id
			if !bytes.HasPrefix(raw, []byte(`{"tgs":1,"v":"5.5.2","fr":60,`)) {
				t.Errorf("%s: root must start with tgs/v/fr as a real export does", name)
			}
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if doc["w"] != 512.0 || doc["h"] != 512.0 {
				t.Errorf("%s: canvas must be 512x512", name)
			}
			for _, l := range doc["layers"].([]any) {
				for _, sh := range asSlice(l.(map[string]any)["shapes"]) {
					if sh.(map[string]any)["ty"] != "gr" {
						t.Errorf("%s: layer shape is not wrapped in a group", name)
					}
				}
			}
			var problems []string
			walk(doc, "", &problems)
			for _, pr := range problems {
				t.Errorf("%s: %s", name, pr)
			}
		}
	}
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func walk(v any, path string, problems *[]string) {
	switch o := v.(type) {
	case map[string]any:
		if o["ty"] == "gr" {
			it := asSlice(o["it"])
			if len(it) == 0 || it[len(it)-1].(map[string]any)["ty"] != "tr" {
				*problems = append(*problems, path+": group does not end with tr")
			}
		}
		if o["a"] == 1.0 {
			kfs := asSlice(o["k"])
			for i, raw := range kfs {
				kf := raw.(map[string]any)
				_, hasI := kf["i"]
				_, hasO := kf["o"]
				if i == len(kfs)-1 {
					if hasI || hasO {
						*problems = append(*problems, path+": terminal keyframe carries i/o easing")
					}
					continue
				}
				if xs, ok := kf["i"].(map[string]any)["x"].([]any); ok && len(xs) != len(asSlice(kf["s"])) {
					*problems = append(*problems, path+": easing length does not match value length")
				}
				if i > 0 && kf["t"].(float64) <= kfs[i-1].(map[string]any)["t"].(float64) {
					*problems = append(*problems, path+": keyframe times not increasing")
				}
			}
		}
		for k, child := range o {
			walk(child, path+"."+k, problems)
		}
	case []any:
		for i, child := range o {
			walk(child, fmt.Sprintf("%s[%d]", path, i), problems)
		}
	}
}
