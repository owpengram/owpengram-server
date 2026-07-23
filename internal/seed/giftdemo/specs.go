package giftdemo

import "telesrv/internal/domain"

// Palette (hex) shared across the demo assets.
const (
	colGold     = 0xF5C542
	colAmber    = 0xF59E0B
	colBlue     = 0x2563EB
	colCyan     = 0x38BDF8
	colViolet   = 0x7C3AED
	colEmerald  = 0x10B981
	colRose     = 0xF43F5E
	colWhite    = 0xFFFFFF
	colSlate    = 0x1E293B
	colMidnight = 0x0B1220
	colDeepEm   = 0x065F46
)

func star(points int, fill, stroke int, strokeW float64, m motion) lottieSpec {
	return lottieSpec{kind: shapeStar, points: points, fill: fromHex(fill), stroke: fromHex(stroke), strokeW: strokeW, radius: 168, motion: m}
}

func polygon(points, fill, stroke int, strokeW float64, m motion) lottieSpec {
	return lottieSpec{kind: shapePolygon, points: points, fill: fromHex(fill), stroke: fromHex(stroke), strokeW: strokeW, radius: 150, motion: m}
}

func ring(fill, inner int, m motion) lottieSpec {
	return lottieSpec{kind: shapeRing, fill: fromHex(fill), stroke: fromHex(inner), radius: 150, motion: m}
}

func burst(points, fill int, m motion) lottieSpec {
	return lottieSpec{kind: shapeBurst, points: points, fill: fromHex(fill), radius: 150, motion: m}
}

// Four colour-only backdrops reused across every upgradeable gift (backdrops
// carry no animation asset, so sharing them is free).
func demoBackdrops() []backdropSpec {
	return []backdropSpec{
		{name: "Midnight", center: colSlate, edge: colMidnight, pattern: colCyan, text: colWhite, permille: 400},
		{name: "Sunset", center: colAmber, edge: colRose, pattern: colWhite, text: colSlate, permille: 300},
		{name: "Emerald", center: colEmerald, edge: colDeepEm, pattern: colWhite, text: colWhite, permille: 200},
		{name: "Royal", center: colViolet, edge: colBlue, pattern: colGold, text: colWhite, permille: 100},
	}
}

// demoGifts returns the three demo gifts in display order, one per capability
// tier: plain (not upgradeable), upgradeable (no crafting), and craftable.
func demoGifts() []giftSpec {
	return []giftSpec{
		{
			// #1 — Звичайний: cheapest, plain, not upgradeable.
			title:   "OwpenGram Spark",
			stars:   15,
			convert: 15,
			base:    burst(8, colGold, motionPulse),
		},
		{
			// #2 — Апгрейдебл: standard upgradeable, no crafting.
			title:   "OwpenGram Star",
			stars:   50,
			convert: 50,
			base:    star(5, colBlue, colCyan, 10, motionSpin),
			upgrade: &upgradeSpec{
				upgradeStars: 200,
				supplyTotal:  10000,
				slug:         "owg-star",
				models: []attrSpec{
					{name: "Sapphire", spec: star(5, colBlue, colCyan, 12, motionSpin), permille: 600},
					{name: "Frost", spec: star(6, colCyan, colWhite, 10, motionSpin), permille: 400},
				},
				patterns: []attrSpec{
					{name: "Halo", spec: burst(12, colCyan, motionPulse), permille: 700},
					{name: "Drift", spec: burst(8, colBlue, motionPulse), permille: 300},
				},
				backdrops: demoBackdrops(),
			},
		},
		{
			// #3 — Крафтабл: upgradeable + craftable.
			title:   "OwpenGram Coin",
			stars:   100,
			convert: 75,
			base:    ring(colAmber, colSlate, motionSpin),
			upgrade: &upgradeSpec{
				upgradeStars: 400,
				supplyTotal:  8000,
				slug:         "owg-coin",
				models: []attrSpec{
					{name: "Bronze", spec: ring(colAmber, colSlate, motionSpin), permille: 600},
					{name: "Silver", spec: ring(colWhite, colSlate, motionSpin), permille: 400},
					{name: "Molten", spec: ring(colRose, colAmber, motionSpinPulse), crafted: true, rarity: domain.StarGiftRarityRare},
				},
				patterns: []attrSpec{
					{name: "Gleam", spec: burst(10, colGold, motionPulse), permille: 700},
					{name: "Ember", spec: burst(6, colAmber, motionPulse), permille: 300},
				},
				backdrops: demoBackdrops(),
			},
		},
	}
}
