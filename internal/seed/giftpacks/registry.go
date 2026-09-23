package giftpacks

import (
	"sync"

	"telesrv/internal/app/giftpack"
)

type giftDef struct {
	slug, title string
	stars       int
	build       func() *scene
}

type packDef struct {
	id, name, author, description string
	// icon is the slug of the gift whose animation represents the pack.
	icon  string
	gifts []giftDef

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

// animations renders every gift once; the art is deterministic, so the
// result is cached for the process lifetime.
func (p *packDef) animations() map[string][]byte {
	p.once.Do(func() {
		p.rendered = make(map[string][]byte, len(p.gifts))
		for _, g := range p.gifts {
			p.rendered[g.slug] = g.build().json()
		}
	})
	return p.rendered
}

type GiftSummary struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Stars int    `json:"stars"`
}

type PackSummary struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Author      string        `json:"author"`
	Description string        `json:"description"`
	Icon        string        `json:"icon"`
	Gifts       []GiftSummary `json:"gifts"`
}

// List describes every built-in pack for the admin panel, in registration
// order.
func List() []PackSummary {
	out := make([]PackSummary, 0, len(registry))
	for _, p := range registry {
		s := PackSummary{ID: p.id, Name: p.name, Author: p.author, Description: p.description, Icon: p.icon, Gifts: make([]GiftSummary, 0, len(p.gifts))}
		for _, g := range p.gifts {
			s.Gifts = append(s.Gifts, GiftSummary{Slug: g.slug, Title: g.title, Stars: g.stars})
		}
		out = append(out, s)
	}
	return out
}

// Animation returns one gift's Lottie JSON, for previews before import.
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
	m := giftpack.Manifest{PackName: p.name, Author: p.author}
	for _, g := range p.gifts {
		path := g.slug + ".json"
		assets[path] = anims[g.slug]
		m.Gifts = append(m.Gifts, giftpack.GiftSpec{
			IDSlug: g.slug, Title: g.title, Stars: int64(g.stars), ConvertStars: int64(g.stars), BaseAnimation: path,
		})
	}
	return m, assets, true
}
