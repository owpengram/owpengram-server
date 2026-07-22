// Package giftdemo is an in-memory catalog of small, fully original demo Star
// Gifts (geometric Lottie authored here — not Telegram's copyrighted assets).
// It backs the admin console's "Default gifts" import source: operators can
// import these to demo the complete gift surface (upgrade + craft) without any
// third-party artwork. Nothing here is enabled automatically; gifts appear in
// the catalog only once an operator imports them.
package giftdemo

import (
	"context"
	"fmt"

	"telesrv/internal/domain"
)

// Preparer normalizes raw Lottie bytes into the canonical Star Gift animation
// pair. *stargifts.Service satisfies it.
type Preparer interface {
	PrepareAnimation(fileName string, data []byte) (domain.StarGiftAnimation, error)
}

const (
	seedActor     = "system-default-gifts"
	seedCommandID = "default-gifts-v1"
)

type attrSpec struct {
	name     string
	spec     lottieSpec
	permille int  // >0 for a normal (drawable) attribute
	crafted  bool // craft-only model; permille must be 0 and rarity named
	rarity   domain.StarGiftAttributeRarityKind
}

type backdropSpec struct {
	name     string
	center   int
	edge     int
	pattern  int
	text     int
	permille int
}

type upgradeSpec struct {
	upgradeStars int64
	supplyTotal  int
	slug         string
	models       []attrSpec
	patterns     []attrSpec
	backdrops    []backdropSpec
}

type giftSpec struct {
	title          string
	stars          int64
	convert        int64
	base           lottieSpec
	limited        bool
	availability   int
	requirePremium bool
	birthday       bool
	upgrade        *upgradeSpec
}

// GiftInfo is a catalog listing entry for the import picker.
type GiftInfo struct {
	ID             int    `json:"id"`
	Title          string `json:"title"`
	Stars          int64  `json:"stars"`
	ConvertStars   int64  `json:"convert_stars"`
	UpgradeStars   int64  `json:"upgrade_stars"`
	Upgradeable    bool   `json:"upgradeable"`
	Craftable      bool   `json:"craftable"`
	Limited        bool   `json:"limited"`
	Availability   int    `json:"availability"`
	RequirePremium bool   `json:"require_premium"`
	ModelCount     int    `json:"model_count"`
	PatternCount   int    `json:"pattern_count"`
	BackdropCount  int    `json:"backdrop_count"`
	CraftedCount   int    `json:"crafted_count"`
}

// List returns the built-in demo gifts, ids 1..N in display order.
func List() []GiftInfo {
	gifts := demoGifts()
	out := make([]GiftInfo, 0, len(gifts))
	for i, spec := range gifts {
		info := GiftInfo{
			ID: i + 1, Title: spec.title, Stars: spec.stars, ConvertStars: spec.convert,
			Limited: spec.limited, Availability: spec.availability, RequirePremium: spec.requirePremium,
		}
		if spec.upgrade != nil {
			info.Upgradeable = true
			info.UpgradeStars = spec.upgrade.upgradeStars
			info.ModelCount = len(spec.upgrade.models)
			info.PatternCount = len(spec.upgrade.patterns)
			info.BackdropCount = len(spec.upgrade.backdrops)
			for _, m := range spec.upgrade.models {
				if m.crafted {
					info.CraftedCount++
				}
			}
			info.Craftable = info.CraftedCount > 0
		}
		out = append(out, info)
	}
	return out
}

func specByID(id int) (giftSpec, int, bool) {
	gifts := demoGifts()
	if id < 1 || id > len(gifts) {
		return giftSpec{}, 0, false
	}
	return gifts[id-1], id - 1, true
}

// BaseAnimationJSON returns the normalized Lottie JSON of a gift's base sticker,
// used by the admin preview player.
func BaseAnimationJSON(prep Preparer, id int) ([]byte, bool, error) {
	spec, _, ok := specByID(id)
	if !ok {
		return nil, false, nil
	}
	anim, err := prepare(prep, spec.title, spec.base)
	if err != nil {
		return nil, false, err
	}
	return anim.JSON, true, nil
}

// BuildBundle assembles the complete catalog+collectible write for one demo
// gift. The bundle carries no official provenance (OfficialGiftID stays 0), so
// these import as ordinary locally-authored catalog entries.
func BuildBundle(prep Preparer, id, sortOrder int) (domain.StarGiftCatalogBundleWrite, string, error) {
	spec, _, ok := specByID(id)
	if !ok {
		return domain.StarGiftCatalogBundleWrite{}, "", fmt.Errorf("unknown demo gift id %d", id)
	}
	write, err := buildBundle(prep, spec, sortOrder)
	if err != nil {
		return domain.StarGiftCatalogBundleWrite{}, "", err
	}
	return write, spec.title, nil
}

// CatalogReader is the read side used to skip gifts already present by title.
type CatalogReader interface {
	Catalog(ctx context.Context) ([]domain.StarGift, error)
}

// PresentTitles returns the set of demo gift titles already in the catalog, so
// callers can import idempotently.
func PresentTitles(ctx context.Context, reader CatalogReader) (map[string]struct{}, error) {
	existing, err := reader.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	demo := map[string]struct{}{}
	for _, spec := range demoGifts() {
		demo[spec.title] = struct{}{}
	}
	present := map[string]struct{}{}
	for _, gift := range existing {
		if _, ok := demo[gift.Title]; ok {
			present[gift.Title] = struct{}{}
		}
	}
	return present, nil
}

func buildBundle(prep Preparer, spec giftSpec, sortOrder int) (domain.StarGiftCatalogBundleWrite, error) {
	baseAnim, err := prepare(prep, spec.title, spec.base)
	if err != nil {
		return domain.StarGiftCatalogBundleWrite{}, err
	}
	catalog := domain.StarGiftCatalogWrite{
		Title:        spec.title,
		Stars:        spec.stars,
		ConvertStars: spec.convert,
		Enabled:      true,
		SortOrder:    sortOrder,
		Animation:    baseAnim,
		Actor:        seedActor,
		CommandID:    seedCommandID,
	}
	if spec.limited {
		catalog.Limited = true
		catalog.AvailabilityTotal = spec.availability
		catalog.AvailabilityRemains = spec.availability
	}
	catalog.RequirePremium = spec.requirePremium
	catalog.Birthday = spec.birthday

	write := domain.StarGiftCatalogBundleWrite{Catalog: catalog}
	if spec.upgrade == nil {
		return write, nil
	}

	up := spec.upgrade
	models, err := buildAttributes(prep, up.models, domain.StarGiftCollectibleModel)
	if err != nil {
		return domain.StarGiftCatalogBundleWrite{}, err
	}
	patterns, err := buildAttributes(prep, up.patterns, domain.StarGiftCollectiblePattern)
	if err != nil {
		return domain.StarGiftCatalogBundleWrite{}, err
	}
	backdrops := make([]domain.StarGiftCollectibleAttribute, 0, len(up.backdrops))
	for i, b := range up.backdrops {
		backdrops = append(backdrops, domain.StarGiftCollectibleAttribute{
			Kind: domain.StarGiftCollectibleBackdrop, Name: b.name, BackdropID: i + 1,
			CenterColor: b.center, EdgeColor: b.edge, PatternColor: b.pattern, TextColor: b.text,
			RarityKind: domain.StarGiftRarityPermille, RarityPermille: b.permille, SortOrder: i,
		})
	}
	write.Collectible = &domain.StarGiftCollectibleWrite{
		UpgradeStars: up.upgradeStars,
		SupplyTotal:  up.supplyTotal,
		SlugPrefix:   up.slug,
		Models:       models,
		Patterns:     patterns,
		Backdrops:    backdrops,
		Actor:        seedActor,
		CommandID:    seedCommandID,
	}
	return write, nil
}

func buildAttributes(prep Preparer, specs []attrSpec, kind domain.StarGiftCollectibleAttributeKind) ([]domain.StarGiftCollectibleAttribute, error) {
	out := make([]domain.StarGiftCollectibleAttribute, 0, len(specs))
	for i, s := range specs {
		anim, err := prepare(prep, s.name, s.spec)
		if err != nil {
			return nil, err
		}
		attr := domain.StarGiftCollectibleAttribute{
			Kind: kind, Name: s.name, SortOrder: i, Animation: &anim,
		}
		if s.crafted {
			attr.Crafted = true
			attr.RarityKind = s.rarity
			attr.RarityPermille = 0
		} else {
			attr.RarityKind = domain.StarGiftRarityPermille
			attr.RarityPermille = s.permille
		}
		out = append(out, attr)
	}
	return out, nil
}

func prepare(prep Preparer, name string, spec lottieSpec) (domain.StarGiftAnimation, error) {
	data, err := renderLottie(spec)
	if err != nil {
		return domain.StarGiftAnimation{}, err
	}
	return prep.PrepareAnimation(name+".json", data)
}
