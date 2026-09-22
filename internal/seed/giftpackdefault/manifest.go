package giftpackdefault

import (
	"fmt"

	"telesrv/internal/app/giftpack"
)

// GiftSummary is a short, protocol-neutral description of one gift in the
// pack, for the admin panel's "what's in the default pack" preview before
// import.
type GiftSummary struct {
	Title string   `json:"title"`
	Theme string   `json:"theme"`
	Flags []string `json:"flags"`
}

// asset renders one icon to Lottie JSON, registers it in assets under path,
// and returns path -- a small helper so Pack() reads as a list of gifts
// rather than a list of manual map-and-render steps.
func asset(assets giftpack.MapAssetResolver, path string, parts []part) string {
	data, err := renderIcon(path, parts)
	if err != nil {
		// Every icon here is a fixed, hand-verified part list; a failure can
		// only mean a bug in this package, not bad input.
		panic(fmt.Sprintf("giftpackdefault: render %s: %v", path, err))
	}
	assets[path] = data
	return path
}

// Pack builds OwpenGram's built-in default gift pack: 7 original gifts,
// each isolating one part of the Star Gift feature surface. See the package
// doc comment for why the art is procedural rather than shipped as files.
func Pack() (giftpack.Manifest, giftpack.MapAssetResolver) {
	assets := giftpack.MapAssetResolver{}

	candle := asset(assets, "candle.json", candleParts())

	cloverClassic := asset(assets, "clover_classic.json", cloverParts(fromHex(0x3FAE5C)))
	cloverGolden := asset(assets, "clover_golden.json", cloverParts(fromHex(0xE8B830)))
	patternDotAsset := asset(assets, "pattern_dot.json", patternDot())
	patternSparkAsset := asset(assets, "pattern_spark.json", patternSpark())

	trophyClassic := asset(assets, "trophy_classic.json", trophyParts(fromHex(0xF5C542)))
	trophySilver := asset(assets, "trophy_silver.json", trophyParts(fromHex(0xC7CDD6)))
	trophyChampion := asset(assets, "trophy_champion.json", trophyParts(fromHex(0xFF5C7A)))
	patternStarAsset := asset(assets, "pattern_star.json", patternStar())
	patternDiamondAsset := asset(assets, "pattern_diamond.json", patternDiamond())

	cupcake := asset(assets, "cupcake.json", cupcakeParts())
	rose := asset(assets, "rose.json", roseParts())
	badge := asset(assets, "badge.json", badgeParts())

	crownClassic := asset(assets, "crown_classic.json", crownParts(fromHex(0xF5C542)))
	crownSapphire := asset(assets, "crown_sapphire.json", crownParts(fromHex(0x4F7BFF)))

	manifest := giftpack.Manifest{
		PackName: "OwpenGram Originals",
		Author:   "OwpenGram",
		Gifts: []giftpack.GiftSpec{
			{
				IDSlug: "cozy-candle", Title: "Cozy Candle", Stars: 15,
				BaseAnimation: candle,
			},
			{
				IDSlug: "lucky-clover", Title: "Lucky Clover", Stars: 25, ConvertStars: 25,
				BaseAnimation: cloverClassic,
				Upgrade: &giftpack.UpgradeSpec{
					UpgradeStars: 50, SupplyTotal: 100_000, SlugPrefix: "clover",
					Models: []giftpack.AttrSpec{
						{Name: "Classic Clover", Animation: cloverClassic, Permille: 700},
						{Name: "Golden Clover", Animation: cloverGolden, Permille: 300},
					},
					Patterns: []giftpack.AttrSpec{
						{Name: "Dot", Animation: patternDotAsset, Permille: 600},
						{Name: "Spark", Animation: patternSparkAsset, Permille: 400},
					},
					Backdrops: backdropSpecs("Emerald", "Golden Hour"),
				},
			},
			{
				IDSlug: "golden-trophy", Title: "Golden Trophy", Stars: 40, ConvertStars: 40,
				BaseAnimation: trophyClassic,
				Limited:       true, AvailabilityTotal: 50, ResellMinStars: 100,
				Upgrade: &giftpack.UpgradeSpec{
					UpgradeStars: 80, SupplyTotal: 50, SlugPrefix: "trophy",
					Models: []giftpack.AttrSpec{
						{Name: "Classic Trophy", Animation: trophyClassic, Permille: 600},
						{Name: "Silver Trophy", Animation: trophySilver, Permille: 400},
						{Name: "Champion Trophy", Animation: trophyChampion, Crafted: true, Rarity: "legendary"},
					},
					Patterns: []giftpack.AttrSpec{
						{Name: "Star", Animation: patternStarAsset, Permille: 500},
						{Name: "Diamond", Animation: patternDiamondAsset, Permille: 500},
					},
					Backdrops: backdropSpecs("Golden Hour", "Slate"),
				},
			},
			{
				IDSlug: "birthday-cupcake", Title: "Birthday Cupcake", Stars: 20,
				BaseAnimation: cupcake, Birthday: true,
			},
			{
				IDSlug: "velvet-rose", Title: "Velvet Rose", Stars: 30,
				BaseAnimation: rose, RequirePremium: true,
			},
			{
				IDSlug: "guardian-badge", Title: "Guardian Badge", Stars: 20,
				BaseAnimation: badge, SupportOnly: true,
			},
			{
				IDSlug: "diamond-crown", Title: "Diamond Crown", Stars: 500, ConvertStars: 500,
				BaseAnimation: crownClassic,
				Limited:       true, Auction: true, AuctionSlug: "diamond-crown",
				GiftsPerRound: 3, AvailabilityTotal: 15,
				Upgrade: &giftpack.UpgradeSpec{
					UpgradeStars: 200, SupplyTotal: 15, SlugPrefix: "crown",
					Models: []giftpack.AttrSpec{
						{Name: "Classic Crown", Animation: crownClassic, Permille: 600},
						{Name: "Sapphire Crown", Animation: crownSapphire, Permille: 400},
					},
					Patterns: []giftpack.AttrSpec{
						{Name: "Diamond", Animation: patternDiamondAsset, Permille: 500},
						{Name: "Spark", Animation: patternSparkAsset, Permille: 500},
					},
					Backdrops: backdropSpecs("Slate", "Azure"),
				},
			},
		},
	}
	return manifest, assets
}

// List summarizes the pack for the admin panel's preview, without touching
// PrepareAnimation/the store -- just the manifest's own declared shape.
func List() []GiftSummary {
	manifest, _ := Pack()
	out := make([]GiftSummary, 0, len(manifest.Gifts))
	for _, g := range manifest.Gifts {
		flags := []string{}
		if g.Limited {
			flags = append(flags, "limited")
		}
		if g.Auction {
			flags = append(flags, "auction")
		}
		if g.Birthday {
			flags = append(flags, "birthday")
		}
		if g.RequirePremium {
			flags = append(flags, "requires premium")
		}
		if g.SupportOnly {
			flags = append(flags, "support only")
		}
		if g.Upgrade != nil {
			flags = append(flags, "upgradeable")
			for _, m := range g.Upgrade.Models {
				if m.Crafted {
					flags = append(flags, "craftable")
					break
				}
			}
		}
		if g.ResellMinStars > 0 {
			flags = append(flags, "resale floor")
		}
		out = append(out, GiftSummary{Title: g.Title, Theme: g.IDSlug, Flags: flags})
	}
	return out
}
