package giftpacks

import (
	"fmt"
	"sort"
	"sync"

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

type GiftSummary struct {
	Slug    string          `json:"slug"`
	Title   string          `json:"title"`
	Stars   int             `json:"stars"`
	Flags   []string        `json:"flags"`
	Upgrade *UpgradeSummary `json:"upgrade,omitempty"`
}

// UpgradeSummary is a gift's collectible pool, for previewing the upgraded
// versions before import. Model/pattern IDs are Animation keys.
type UpgradeSummary struct {
	Stars     int64             `json:"stars"`
	Supply    int               `json:"supply"`
	Models    []AttrSummary     `json:"models"`
	Patterns  []AttrSummary     `json:"patterns"`
	Backdrops []BackdropSummary `json:"backdrops"`
}

type AttrSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Permille int    `json:"permille"`
	Rarity   string `json:"rarity,omitempty"`
}

type BackdropSummary struct {
	Name    string `json:"name"`
	Center  string `json:"center"`
	Edge    string `json:"edge"`
	Pattern string `json:"pattern"`
	Text    string `json:"text"`
}

func (p *packDef) upgradeSummary(g giftDef) *UpgradeSummary {
	u := g.upgrade
	if u == nil {
		return nil
	}
	out := &UpgradeSummary{Stars: u.stars, Supply: u.supply, Models: []AttrSummary{}, Patterns: []AttrSummary{}, Backdrops: []BackdropSummary{}}
	for _, a := range u.models {
		out.Models = append(out.Models, AttrSummary{ID: a.id, Name: a.name, Permille: a.permille, Rarity: a.rarity})
	}
	for _, a := range u.patterns {
		out.Patterns = append(out.Patterns, AttrSummary{ID: a.id, Name: a.name, Permille: a.permille})
	}
	for _, name := range u.backdrops {
		b := p.backdrops[name]
		out.Backdrops = append(out.Backdrops, BackdropSummary{Name: name, Center: b.Center, Edge: b.Edge, Pattern: b.Pattern, Text: b.Text})
	}
	return out
}

type PackSummary struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Author      string        `json:"author"`
	Description string        `json:"description"`
	Icon        string        `json:"icon"`
	Gifts       []GiftSummary `json:"gifts"`
}

// flags names the mechanics a gift exercises, for the admin panel preview.
func (g giftDef) flags() []string {
	out := []string{}
	sp := g.spec
	switch {
	case sp.Auction:
		out = append(out, fmt.Sprintf("auction · %d per round, %d total", sp.GiftsPerRound, sp.AvailabilityTotal))
	case sp.Limited || sp.AvailabilityTotal > 0:
		out = append(out, fmt.Sprintf("limited · %d", sp.AvailabilityTotal))
	}
	if sp.RequirePremium {
		out = append(out, "premium only")
	}
	if sp.Birthday {
		out = append(out, "birthday")
	}
	if sp.SupportOnly {
		out = append(out, "support only")
	}
	if sp.LimitedPerUser {
		out = append(out, fmt.Sprintf("%d per user", sp.PerUserTotal))
	}
	if g.upgrade != nil {
		out = append(out, "upgradeable")
		for _, m := range g.upgrade.models {
			if m.rarity != "" {
				out = append(out, "craftable")
				break
			}
		}
	}
	if sp.ResellMinStars > 0 {
		out = append(out, fmt.Sprintf("resale from %d", sp.ResellMinStars))
	}
	return out
}

// List describes every built-in pack for the admin panel, in registration
// order.
func List() []PackSummary {
	out := make([]PackSummary, 0, len(registry))
	for _, p := range registry {
		s := PackSummary{ID: p.id, Name: p.name, Author: p.author, Description: p.description, Icon: p.icon, Gifts: make([]GiftSummary, 0, len(p.gifts))}
		for _, g := range p.gifts {
			s.Gifts = append(s.Gifts, GiftSummary{Slug: g.slug, Title: g.title, Stars: g.stars, Flags: g.flags(), Upgrade: p.upgradeSummary(g)})
		}
		out = append(out, s)
	}
	return out
}

// Animation returns one gift's (or collectible attribute's) Lottie JSON, for
// previews before import.
func Animation(packID, slug string) ([]byte, bool) {
	p := find(packID)
	if p == nil {
		return nil, false
	}
	data, ok := p.animations()[slug]
	return data, ok
}

// Manifest builds the pack as a regular giftpack manifest, so a built-in
// pack imports through exactly the same path as an uploaded .zip.
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
	m := giftpack.Manifest{PackName: p.name, Author: p.author}
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
