package giftdemo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"telesrv/internal/app/stargifts"
	"telesrv/internal/store/memory"
)

type fakeBlob struct{ data map[string][]byte }

func (b *fakeBlob) Name() string { return "localfs" }
func (b *fakeBlob) Put(_ context.Context, data []byte) (string, error) {
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])
	b.data[key] = append([]byte(nil), data...)
	return key, nil
}
func (b *fakeBlob) Get(_ context.Context, key string) ([]byte, error) {
	return append([]byte(nil), b.data[key]...), nil
}

func newService() *stargifts.Service {
	return stargifts.NewService(memory.NewStarGiftStore(), &fakeBlob{data: map[string][]byte{}}, 2)
}

func TestListDescribesFullGiftSurface(t *testing.T) {
	list := List()
	if len(list) != 5 {
		t.Fatalf("List has %d gifts, want 5", len(list))
	}
	upgradeable, craftable, limited, premium := 0, 0, 0, 0
	for _, g := range list {
		if g.ID < 1 || g.Title == "" || g.Stars <= 0 {
			t.Fatalf("bad gift info: %+v", g)
		}
		if g.Upgradeable {
			upgradeable++
		}
		if g.Craftable {
			craftable++
		}
		if g.Limited {
			limited++
		}
		if g.RequirePremium {
			premium++
		}
	}
	if upgradeable != 4 || craftable != 3 || limited != 2 || premium != 1 {
		t.Fatalf("surface counts upgradeable=%d craftable=%d limited=%d premium=%d", upgradeable, craftable, limited, premium)
	}
}

// Every demo gift must build into a valid catalog+collectible bundle and
// import cleanly through the real service (which materializes and validates
// the whole pool). Limited/premium flags must survive onto the stored gift.
func TestBuildBundleImportsEndToEnd(t *testing.T) {
	ctx := context.Background()
	svc := newService()

	for _, info := range List() {
		write, title, err := BuildBundle(svc, info.ID, info.ID)
		if err != nil {
			t.Fatalf("build %q: %v", info.Title, err)
		}
		if title != info.Title {
			t.Fatalf("title mismatch %q != %q", title, info.Title)
		}
		if _, err := svc.CreateCatalogBundle(ctx, write); err != nil {
			t.Fatalf("import %q: %v", info.Title, err)
		}
	}

	catalog, err := svc.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 5 {
		t.Fatalf("catalog has %d, want 5", len(catalog))
	}
	limited, premium, upgradeable := 0, 0, 0
	for _, g := range catalog {
		if g.Limited {
			limited++
		}
		if g.RequirePremium {
			premium++
		}
		if g.UpgradeStars > 0 {
			upgradeable++
		}
	}
	if limited != 2 || premium != 1 || upgradeable != 4 {
		t.Fatalf("stored flags limited=%d premium=%d upgradeable=%d", limited, premium, upgradeable)
	}
}

func TestBaseAnimationJSONRenders(t *testing.T) {
	svc := newService()
	data, ok, err := BaseAnimationJSON(svc, 1)
	if err != nil || !ok || len(data) == 0 {
		t.Fatalf("base animation id=1: ok=%v err=%v len=%d", ok, err, len(data))
	}
	if _, ok, _ := BaseAnimationJSON(svc, 99); ok {
		t.Fatalf("id=99 should not exist")
	}
}

func TestPresentTitles(t *testing.T) {
	ctx := context.Background()
	svc := newService()
	write, _, err := BuildBundle(svc, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateCatalogBundle(ctx, write); err != nil {
		t.Fatal(err)
	}
	present, err := PresentTitles(ctx, svc)
	if err != nil {
		t.Fatal(err)
	}
	if len(present) != 1 {
		t.Fatalf("present=%v, want exactly the one imported title", present)
	}
	if _, ok := present["OwpenGram Spark"]; !ok {
		t.Fatalf("expected Spark present, got %v", present)
	}
}
