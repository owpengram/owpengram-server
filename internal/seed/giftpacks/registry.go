package giftpacks

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"telesrv/internal/app/giftpack"
)

type giftDef struct {
	slug, title string
	stars       int
	build       func() *scene
	// spec carries the lifecycle flags (limited, auction, premium, ...);
	// identity, price and animation fields are filled in by Manifest.
	// ConvertStars defaults to the full price when left 0.
	spec    giftpack.GiftSpec
	upgrade *upgradeDef
}

// attrDef is one collectible model or pattern. A non-empty rarity makes it a
// craft-only model (never drawn by the random upgrade).
type attrDef struct {
	id, name string
	build    func() *scene
	permille int
	rarity   string
}

type upgradeDef struct {
	stars     int64
	supply    int
	models    []attrDef
	patterns  []attrDef
	backdrops []string
}

type packDef struct {
	id, name, author, description string
	// icon is the slug of the gift whose animation represents the pack.
	icon      string
	gifts     []giftDef
	backdrops map[string]giftpack.BackdropSpec

	once     sync.Once
	rendered map[string][]byte
}

var registry []*packDef

func register(p *packDef) {
	registry = append(registry, p)
}

func find(id string) *packDef {
	for _, p := range registry {
		if p.id == id {
			return p
		}
	}
	return nil
}

// animations renders every gift and collectible attribute once, keyed by
// gift slug / attribute id; the art is deterministic, so the result is
// cached for the process lifetime.
func (p *packDef) animations() map[string][]byte {
	p.once.Do(func() {
		p.rendered = map[string][]byte{}
		for _, g := range p.gifts {
			p.rendered[g.slug] = g.build().json()
			if g.upgrade == nil {
				continue
			}
			for _, a := range append(append([]attrDef{}, g.upgrade.models...), g.upgrade.patterns...) {
				if _, done := p.rendered[a.id]; !done {
					p.rendered[a.id] = a.build().json()
				}
			}
		}
	})
	return p.rendered
}

// IDs is every pack authored in this repo, in registration order.
func IDs() []string {
	out := make([]string, 0, len(registry))
	for _, p := range registry {
		out = append(out, p.id)
	}
	return out
}

// Archive renders a pack as the distributable .zip an operator uploads:
// pack.json at the root plus every animation it references. Packs are not
// compiled into the running server any more -- this is the only way one
// leaves this package.
func Archive(packID string) ([]byte, error) {
	manifest, assets, ok := Manifest(packID)
	if !ok {
		return nil, fmt.Errorf("unknown gift pack %q", packID)
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode pack.json: %w", err)
	}
	paths := make([]string, 0, len(assets))
	for path := range assets {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// Deterministic output: the same pack always produces byte-identical
	// bytes, so re-exporting one is a visible no-op rather than a new file
	// to re-upload.
	write := func(name string, data []byte) error {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Unix(0, 0).UTC()}
		w, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	if err := write("pack.json", manifestJSON); err != nil {
		return nil, err
	}
	for _, path := range paths {
		if err := write(path, assets[path]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Manifest builds the pack as a regular giftpack manifest, so a pack
// authored here imports through exactly the same path as any uploaded .zip.
func Manifest(packID string) (giftpack.Manifest, giftpack.MapAssetResolver, bool) {
	p := find(packID)
	if p == nil {
		return giftpack.Manifest{}, nil, false
	}
	anims := p.animations()
	assets := giftpack.MapAssetResolver{}
	asset := func(id string) string {
		path := id + ".json"
		assets[path] = anims[id]
		return path
	}
	m := giftpack.Manifest{PackName: p.name, Author: p.author, Description: p.description, Icon: p.icon}
	for _, g := range p.gifts {
		spec := g.spec
		spec.IDSlug, spec.Title, spec.Stars, spec.BaseAnimation = g.slug, g.title, int64(g.stars), asset(g.slug)
		if spec.ConvertStars == 0 {
			spec.ConvertStars = int64(g.stars)
		}
		if u := g.upgrade; u != nil {
			us := &giftpack.UpgradeSpec{UpgradeStars: u.stars, SupplyTotal: u.supply, SlugPrefix: g.slug}
			for _, a := range u.models {
				us.Models = append(us.Models, giftpack.AttrSpec{Name: a.name, Animation: asset(a.id), Permille: a.permille, Crafted: a.rarity != "", Rarity: a.rarity})
			}
			for _, a := range u.patterns {
				us.Patterns = append(us.Patterns, giftpack.AttrSpec{Name: a.name, Animation: asset(a.id), Permille: a.permille})
			}
			for _, name := range u.backdrops {
				b, ok := p.backdrops[name]
				if !ok {
					panic(fmt.Sprintf("giftpacks: pack %q gift %q uses unknown backdrop %q", p.id, g.slug, name))
				}
				b.Name = name
				us.Backdrops = append(us.Backdrops, b)
			}
			spec.Upgrade = us
		}
		m.Gifts = append(m.Gifts, spec)
	}
	return m, assets, true
}

// assetIDs is every rendered animation id of a pack, sorted (tests).
func (p *packDef) assetIDs() []string {
	ids := make([]string, 0, len(p.animations()))
	for id := range p.animations() {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
