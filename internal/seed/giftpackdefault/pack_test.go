package giftpackdefault

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"

	"telesrv/internal/app/giftpack"
	stargiftsapp "telesrv/internal/app/stargifts"
	"telesrv/internal/store/memory"
)

// fakeBlobs mirrors internal/app/giftpack's test double -- no production
// stargifts.BlobBackend implementation is convenient to reuse in a unit test.
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

// TestPackImportsCleanly is the real point of this package: every hand-built
// Lottie animation must pass stargifts.Service.PrepareAnimation's structural
// validator, and every gift's flag combination (limited/auction/craft/
// resale floor/birthday/premium/support-only) must pass the domain lifecycle
// and collectible validators -- exercised here exactly the way the admin
// panel's "Import default pack" button will exercise it.
func TestPackImportsCleanly(t *testing.T) {
	svc := stargiftsapp.NewService(memory.NewStarGiftStore(), newFakeBlobs(), 2)
	manifest, assets := Pack()
	ctx := context.Background()

	result, err := giftpack.Import(ctx, svc, manifest, assets, giftpack.ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(result.Gifts) != 7 {
		t.Fatalf("Gifts = %d, want 7", len(result.Gifts))
	}
	for _, g := range result.Gifts {
		if g.Status != "created" {
			t.Errorf("gift %q status = %q error=%q, want created", g.Title, g.Status, g.Error)
		}
	}

	catalog, err := svc.Catalog(ctx)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(catalog) != 7 {
		t.Fatalf("catalog has %d gifts, want 7", len(catalog))
	}

	byTitle := make(map[string]bool, len(catalog))
	for _, g := range catalog {
		byTitle[g.Title] = true
		switch g.Title {
		case "Golden Trophy":
			if !g.Limited || g.AvailabilityTotal != 50 || g.ResellMinStars != 100 {
				t.Errorf("Golden Trophy = %+v, want limited availability_total=50 resell_min_stars=100", g)
			}
		case "Diamond Crown":
			if !g.Limited || !g.Auction || g.AuctionSlug != "diamond-crown" || g.GiftsPerRound != 3 {
				t.Errorf("Diamond Crown = %+v, want limited+auction slug=diamond-crown gifts_per_round=3", g)
			}
		case "Birthday Cupcake":
			if !g.Birthday {
				t.Errorf("Birthday Cupcake.Birthday = false, want true")
			}
		case "Velvet Rose":
			if !g.RequirePremium {
				t.Errorf("Velvet Rose.RequirePremium = false, want true")
			}
		case "Guardian Badge":
			if !g.SupportOnly {
				t.Errorf("Guardian Badge.SupportOnly = false, want true")
			}
		}
	}
	for _, want := range []string{"Cozy Candle", "Lucky Clover", "Golden Trophy", "Birthday Cupcake", "Velvet Rose", "Guardian Badge", "Diamond Crown"} {
		if !byTitle[want] {
			t.Errorf("catalog is missing gift %q", want)
		}
	}
}

// TestPackIsIdempotent confirms the default pack can be re-imported safely
// (the "Import default pack" button in the admin panel is not one-shot).
func TestPackIsIdempotent(t *testing.T) {
	svc := stargiftsapp.NewService(memory.NewStarGiftStore(), newFakeBlobs(), 2)
	manifest, assets := Pack()
	ctx := context.Background()
	if _, err := giftpack.Import(ctx, svc, manifest, assets, giftpack.ImportOptions{}); err != nil {
		t.Fatalf("first Import: %v", err)
	}
	result, err := giftpack.Import(ctx, svc, manifest, assets, giftpack.ImportOptions{})
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	for _, g := range result.Gifts {
		if g.Status != "skipped" {
			t.Errorf("gift %q status = %q on re-import, want skipped", g.Title, g.Status)
		}
	}
}

func TestListMatchesPack(t *testing.T) {
	manifest, _ := Pack()
	summaries := List()
	if len(summaries) != len(manifest.Gifts) {
		t.Fatalf("List() has %d entries, Pack() has %d gifts", len(summaries), len(manifest.Gifts))
	}
	for i, s := range summaries {
		if s.Title != manifest.Gifts[i].Title {
			t.Errorf("summary[%d].Title = %q, want %q", i, s.Title, manifest.Gifts[i].Title)
		}
		// Flags must round-trip as JSON "[]", never "null" -- the admin
		// panel renders it with gift.flags.map(...) directly.
		if s.Flags == nil {
			t.Errorf("summary[%d] (%q).Flags is nil, want a non-nil (possibly empty) slice", i, s.Title)
		}
	}
}
