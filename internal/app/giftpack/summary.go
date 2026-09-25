package giftpack

import (
	"fmt"
	"strings"
)

// PackSummary is one pack rendered for the admin panel: everything needed to
// preview it -- prices, mechanics, and the full collectible pool -- without
// importing anything. It is derived entirely from the pack's own manifest,
// so an uploaded pack and a pack authored in this repo describe themselves
// exactly the same way.
type PackSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Author      string `json:"author"`
	Description string `json:"description"`
	// Icon is the slug of the gift whose animation represents the pack.
	Icon  string        `json:"icon"`
	Gifts []GiftSummary `json:"gifts"`
}

type GiftSummary struct {
	Slug         string          `json:"slug"`
	Title        string          `json:"title"`
	Stars        int             `json:"stars"`
	ConvertStars int             `json:"convert_stars"`
	Flags        []string        `json:"flags"`
	Upgrade      *UpgradeSummary `json:"upgrade,omitempty"`
}

// UpgradeSummary is a gift's collectible pool, so the panel can show the
// upgraded variants before import. Model/pattern IDs are animation slugs.
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
	Name     string `json:"name"`
	Center   string `json:"center"`
	Edge     string `json:"edge"`
	Pattern  string `json:"pattern"`
	Text     string `json:"text"`
	Permille int    `json:"permille"`
}

// Summarize renders a manifest for the panel. Every animation gets a slug --
// a URL-safe handle the preview endpoint resolves back to an archive path
// (see AssetPaths), because a raw path inside the archive is neither safe to
// put in a URL nor safe to accept back from one.
func Summarize(packID string, m Manifest) PackSummary {
	out := PackSummary{ID: packID, Name: m.PackName, Author: m.Author, Description: m.Description, Gifts: []GiftSummary{}}
	if out.Author == "" {
		out.Author = "unknown"
	}
	icon := Slugify(m.Icon)
	for _, spec := range m.Gifts {
		slug := giftSlug(spec)
		if out.Icon == "" || slug == icon {
			out.Icon = slug
		}
		convert := int(spec.ConvertStars)
		if convert == 0 {
			convert = int(spec.Stars)
		}
		g := GiftSummary{Slug: slug, Title: spec.Title, Stars: int(spec.Stars), ConvertStars: convert, Flags: Flags(spec)}
		if u := spec.Upgrade; u != nil {
			us := &UpgradeSummary{Stars: u.UpgradeStars, Supply: u.SupplyTotal,
				Models: []AttrSummary{}, Patterns: []AttrSummary{}, Backdrops: []BackdropSummary{}}
			for i, a := range u.Models {
				us.Models = append(us.Models, AttrSummary{ID: attrSlug("m", slug, i), Name: a.Name, Permille: a.Permille, Rarity: a.Rarity})
			}
			for i, a := range u.Patterns {
				us.Patterns = append(us.Patterns, AttrSummary{ID: attrSlug("p", slug, i), Name: a.Name, Permille: a.Permille})
			}
			for _, b := range u.Backdrops {
				us.Backdrops = append(us.Backdrops, BackdropSummary{Name: b.Name, Center: b.Center, Edge: b.Edge, Pattern: b.Pattern, Text: b.Text, Permille: b.Permille})
			}
			g.Upgrade = us
		}
		out.Gifts = append(out.Gifts, g)
	}
	return out
}

// AssetPaths maps every slug Summarize hands out to the animation's path
// inside the pack. The preview endpoint resolves through this map and never
// through a caller-supplied path, so no request can read an arbitrary
// archive entry.
func AssetPaths(m Manifest) map[string]string {
	out := map[string]string{}
	for _, spec := range m.Gifts {
		slug := giftSlug(spec)
		out[slug] = spec.BaseAnimation
		if spec.Upgrade == nil {
			continue
		}
		for i, a := range spec.Upgrade.Models {
			out[attrSlug("m", slug, i)] = a.Animation
		}
		for i, a := range spec.Upgrade.Patterns {
			out[attrSlug("p", slug, i)] = a.Animation
		}
	}
	return out
}

// Flags names the mechanics one gift exercises, for the panel's preview.
func Flags(sp GiftSpec) []string {
	out := []string{}
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
	if sp.Upgrade != nil {
		out = append(out, "upgradeable")
		for _, m := range sp.Upgrade.Models {
			if m.Crafted || m.Rarity != "" {
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

func giftSlug(spec GiftSpec) string {
	if s := Slugify(spec.IDSlug); s != "" {
		return s
	}
	if s := Slugify(spec.Title); s != "" {
		return s
	}
	return "gift"
}

func attrSlug(kind, giftSlug string, index int) string {
	return fmt.Sprintf("%s-%s-%d", kind, giftSlug, index+1)
}

// Slugify reduces a name to the lowercase [a-z0-9-] token the panel's pack
// and animation URLs are restricted to, or "" when nothing survives.
func Slugify(v string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(v)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 64 {
		out = strings.Trim(out[:64], "-")
	}
	return out
}
