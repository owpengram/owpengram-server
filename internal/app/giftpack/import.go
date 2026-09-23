package giftpack

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"telesrv/internal/domain"
)

// Preparer normalizes a raw animation file into the canonical form the
// domain write structs require. *stargifts.Service satisfies this as-is.
type Preparer interface {
	PrepareAnimation(fileName string, data []byte) (domain.StarGiftAnimation, error)
}

// Service is the minimal stargifts.Service surface Import needs.
// *stargifts.Service satisfies it as-is.
type Service interface {
	Preparer
	Catalog(ctx context.Context) ([]domain.StarGift, error)
	CreateCatalogBundle(ctx context.Context, write domain.StarGiftCatalogBundleWrite) (domain.StarGiftCatalogBundleResult, error)
}

// ImportOptions configures one Import call.
type ImportOptions struct {
	// DryRun stops just short of CreateCatalogBundle: every asset is still
	// resolved and PrepareAnimation'd (so a bad file is still caught), and
	// lifecycle validation still runs, but nothing is written to the store.
	DryRun bool
	// Now overrides the clock (tests only); defaults to time.Now.
	Now func() time.Time
}

// GiftImportOutcome reports what happened to one gift in the pack.
type GiftImportOutcome struct {
	Title  string `json:"title"`
	Status string `json:"status"` // "created" | "would_create" | "skipped" | "failed"
	Error  string `json:"error,omitempty"`
	GiftID int64  `json:"gift_id,omitempty"`
}

// ImportResult is the full outcome of one Import call, one entry per gift in
// the manifest, in manifest order.
type ImportResult struct {
	PackName string              `json:"pack_name"`
	Gifts    []GiftImportOutcome `json:"gifts"`
}

// Import publishes every gift in manifest that isn't already present (by
// title -- the same idempotency check the deleted internal/seed/giftdemo
// used) through svc.CreateCatalogBundle. A gift whose manifest is malformed
// or whose animation fails validation is reported as "failed" and does not
// stop the rest of the pack from importing.
func Import(ctx context.Context, svc Service, manifest Manifest, assets AssetResolver, opts ImportOptions) (ImportResult, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	existing, err := svc.Catalog(ctx)
	if err != nil {
		return ImportResult{}, fmt.Errorf("read existing catalog: %w", err)
	}
	existingTitles := make(map[string]bool, len(existing))
	for _, g := range existing {
		existingTitles[g.Title] = true
	}

	result := ImportResult{PackName: manifest.PackName}
	for _, spec := range manifest.Gifts {
		outcome := GiftImportOutcome{Title: spec.Title}
		if existingTitles[spec.Title] {
			outcome.Status = "skipped"
			result.Gifts = append(result.Gifts, outcome)
			continue
		}
		bundle, err := buildBundle(svc, spec, assets)
		if err != nil {
			outcome.Status, outcome.Error = "failed", err.Error()
			result.Gifts = append(result.Gifts, outcome)
			continue
		}
		bundle.Catalog.NormalizeLifecycleAuthoring(int(now().Unix()))
		if err := bundle.Catalog.ValidateLifecycleAuthoring(int(now().Unix())); err != nil {
			outcome.Status, outcome.Error = "failed", err.Error()
			result.Gifts = append(result.Gifts, outcome)
			continue
		}
		if opts.DryRun {
			outcome.Status = "would_create"
			result.Gifts = append(result.Gifts, outcome)
			continue
		}
		created, err := createWithFreeSlugPrefix(ctx, svc, bundle)
		if err != nil {
			outcome.Status, outcome.Error = "failed", err.Error()
			result.Gifts = append(result.Gifts, outcome)
			continue
		}
		outcome.Status, outcome.GiftID = "created", created.Catalog.Gift.ID
		result.Gifts = append(result.Gifts, outcome)
	}
	return result, nil
}

// maxSlugPrefixAttempts bounds how many "-N" suffixes Import tries before
// giving up on a gift whose slug prefix keeps colliding.
const maxSlugPrefixAttempts = 50

// createWithFreeSlugPrefix publishes bundle, moving its collectible slug
// prefix to "<prefix>-2", "<prefix>-3", ... while another gift already owns
// it. Re-importing a pack after disabling its old gifts creates new gift
// identities with the pack's same prefixes; reusing one would mint slugs
// that already exist and fail every upgrade. A "-N" suffix never collides
// with the original prefix's own slugs, since a slug's number is always the
// part after its last hyphen.
func createWithFreeSlugPrefix(ctx context.Context, svc Service, bundle domain.StarGiftCatalogBundleWrite) (domain.StarGiftCatalogBundleResult, error) {
	if bundle.Collectible == nil {
		return svc.CreateCatalogBundle(ctx, bundle)
	}
	base := bundle.Collectible.SlugPrefix
	for attempt := 1; ; attempt++ {
		prefix := base
		if attempt > 1 {
			suffix := "-" + strconv.Itoa(attempt)
			if len(base)+len(suffix) > 48 {
				prefix = strings.TrimRight(base[:48-len(suffix)], "-") + suffix
			} else {
				prefix = base + suffix
			}
		}
		collectible := *bundle.Collectible
		collectible.SlugPrefix = prefix
		bundle.Collectible = &collectible
		created, err := svc.CreateCatalogBundle(ctx, bundle)
		if !errors.Is(err, domain.ErrStarGiftCollectibleSlugTaken) || attempt >= maxSlugPrefixAttempts {
			return created, err
		}
	}
}

func buildBundle(prep Preparer, spec GiftSpec, assets AssetResolver) (domain.StarGiftCatalogBundleWrite, error) {
	baseData, err := assets.Open(spec.BaseAnimation)
	if err != nil {
		return domain.StarGiftCatalogBundleWrite{}, err
	}
	baseAnim, err := prep.PrepareAnimation(spec.BaseAnimation, baseData)
	if err != nil {
		return domain.StarGiftCatalogBundleWrite{}, fmt.Errorf("gift %q: base animation: %w", spec.Title, err)
	}
	bundle := domain.StarGiftCatalogBundleWrite{
		Catalog: domain.StarGiftCatalogWrite{
			Title:                spec.Title,
			Stars:                spec.Stars,
			ConvertStars:         spec.ConvertStars,
			Enabled:              true,
			Animation:            baseAnim,
			Limited:              spec.Limited,
			Birthday:             spec.Birthday,
			RequirePremium:       spec.RequirePremium,
			SupportOnly:          spec.SupportOnly,
			LimitedPerUser:       spec.LimitedPerUser,
			Auction:              spec.Auction,
			AvailabilityTotal:    spec.AvailabilityTotal,
			ResellMinStars:       spec.ResellMinStars,
			PerUserTotal:         spec.PerUserTotal,
			AuctionSlug:          spec.AuctionSlug,
			GiftsPerRound:        spec.GiftsPerRound,
			AuctionStartDate:     spec.AuctionStartDate,
			AuctionRoundDuration: spec.AuctionRoundDuration,
		},
	}
	if spec.Upgrade == nil {
		return bundle, nil
	}
	models, err := buildAttributes(prep, spec.Title, domain.StarGiftCollectibleModel, spec.Upgrade.Models, assets)
	if err != nil {
		return domain.StarGiftCatalogBundleWrite{}, err
	}
	patterns, err := buildAttributes(prep, spec.Title, domain.StarGiftCollectiblePattern, spec.Upgrade.Patterns, assets)
	if err != nil {
		return domain.StarGiftCatalogBundleWrite{}, err
	}
	backdrops, err := buildBackdrops(spec.Title, spec.Upgrade.Backdrops)
	if err != nil {
		return domain.StarGiftCatalogBundleWrite{}, err
	}
	bundle.Collectible = &domain.StarGiftCollectibleWrite{
		UpgradeStars: spec.Upgrade.UpgradeStars,
		SupplyTotal:  spec.Upgrade.SupplyTotal,
		SlugPrefix:   spec.Upgrade.SlugPrefix,
		Models:       models,
		Patterns:     patterns,
		Backdrops:    backdrops,
		CommandID:    "giftpack:" + spec.Title,
	}
	return bundle, nil
}

func buildAttributes(prep Preparer, giftTitle string, kind domain.StarGiftCollectibleAttributeKind, specs []AttrSpec, assets AssetResolver) ([]domain.StarGiftCollectibleAttribute, error) {
	out := make([]domain.StarGiftCollectibleAttribute, 0, len(specs))
	for i, a := range specs {
		data, err := assets.Open(a.Animation)
		if err != nil {
			return nil, err
		}
		anim, err := prep.PrepareAnimation(a.Animation, data)
		if err != nil {
			return nil, fmt.Errorf("gift %q: %s %q: %w", giftTitle, kind, a.Name, err)
		}
		rarity := domain.StarGiftAttributeRarityKind(strings.TrimSpace(a.Rarity))
		if rarity == "" {
			rarity = domain.StarGiftRarityPermille
		}
		out = append(out, domain.StarGiftCollectibleAttribute{
			Kind:           kind,
			Name:           a.Name,
			RarityKind:     rarity,
			RarityPermille: a.Permille,
			Crafted:        a.Crafted,
			SortOrder:      i,
			Animation:      &anim,
		})
	}
	return out, nil
}

func buildBackdrops(giftTitle string, specs []BackdropSpec) ([]domain.StarGiftCollectibleAttribute, error) {
	out := make([]domain.StarGiftCollectibleAttribute, 0, len(specs))
	for i, b := range specs {
		center, err := parseHexColor(b.Center)
		if err != nil {
			return nil, fmt.Errorf("gift %q: backdrop %q: center: %w", giftTitle, b.Name, err)
		}
		edge, err := parseHexColor(b.Edge)
		if err != nil {
			return nil, fmt.Errorf("gift %q: backdrop %q: edge: %w", giftTitle, b.Name, err)
		}
		pattern, err := parseHexColor(b.Pattern)
		if err != nil {
			return nil, fmt.Errorf("gift %q: backdrop %q: pattern: %w", giftTitle, b.Name, err)
		}
		text, err := parseHexColor(b.Text)
		if err != nil {
			return nil, fmt.Errorf("gift %q: backdrop %q: text: %w", giftTitle, b.Name, err)
		}
		out = append(out, domain.StarGiftCollectibleAttribute{
			Kind:           domain.StarGiftCollectibleBackdrop,
			Name:           b.Name,
			BackdropID:     i + 1,
			CenterColor:    center,
			EdgeColor:      edge,
			PatternColor:   pattern,
			TextColor:      text,
			RarityKind:     domain.StarGiftRarityPermille,
			RarityPermille: b.Permille,
			SortOrder:      i,
		})
	}
	return out, nil
}

// parseHexColor parses a "#RRGGBB" (or bare "RRGGBB") string into a packed
// 24-bit int, the form domain.StarGiftCollectibleAttribute's color fields want.
func parseHexColor(s string) (int, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return 0, fmt.Errorf("invalid color %q, want #RRGGBB", s)
	}
	v, err := strconv.ParseInt(s, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid color %q: %w", s, err)
	}
	return int(v), nil
}
