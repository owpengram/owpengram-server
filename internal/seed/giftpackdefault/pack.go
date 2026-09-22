package giftpackdefault

import (
	"fmt"

	"telesrv/internal/app/giftpack"
)

// --- icon builders ---------------------------------------------------------
//
// Each function returns the composite part list for one icon, back-to-front
// draw order. Where a gift has model variants, the dominant colour is a
// parameter so a "palette swap" model costs one call, not a second hand-laid
// icon.

func candleParts() []part {
	return []part{
		{shape: shapeRect, w: 60, h: 170, rounded: 14, fill: fromHex(0xFFF4E0), offsetY: 40},
		{shape: shapeRect, w: 4, h: 18, rounded: 2, fill: fromHex(0x4A3728), offsetY: -50},
		{shape: shapeEllipse, w: 34, h: 52, fill: fromHex(0xFFB13C), offsetY: -85, motion: motionPulse},
		{shape: shapeEllipse, w: 14, h: 22, fill: fromHex(0xFFE59A), offsetY: -92, motion: motionPulse},
	}
}

func cloverParts(leaf rgb) []part {
	out := petalRing(4, 55, part{shape: shapeEllipse, w: 90, h: 70, fill: leaf})
	return append(out, part{shape: shapeRect, w: 10, h: 70, rounded: 5, fill: shade(leaf, 0.7), offsetY: 95})
}

func trophyParts(gold rgb) []part {
	shine := shade(gold, 1.5)
	base := shade(gold, 0.75)
	return []part{
		{shape: shapeEllipse, w: 46, h: 66, noFill: true, strokeColor: gold, strokeWidth: 14, offsetX: -95, offsetY: -30},
		{shape: shapeEllipse, w: 46, h: 66, noFill: true, strokeColor: gold, strokeWidth: 14, offsetX: 95, offsetY: -30},
		{shape: shapeEllipse, w: 150, h: 110, fill: gold, offsetY: -40},
		{shape: shapeEllipse, w: 110, h: 70, fill: shine, offsetY: -55},
		{shape: shapeRect, w: 26, h: 60, rounded: 6, fill: gold, offsetY: 45},
		{shape: shapeRect, w: 90, h: 22, rounded: 8, fill: base, offsetY: 85},
	}
}

func cupcakeParts() []part {
	return []part{
		{shape: shapeRect, w: 140, h: 90, rounded: 14, fill: fromHex(0xFF8FB3), offsetY: 60},
		{shape: shapeStar, points: 8, innerRatio: 0.7, w: 160, fill: fromHex(0xFFF1DE), offsetY: -10},
		{shape: shapeRect, w: 10, h: 50, rounded: 3, fill: fromHex(0xFFFFFF), offsetY: -75},
		{shape: shapeEllipse, w: 20, h: 28, fill: fromHex(0xFFB13C), offsetY: -108, motion: motionPulse},
	}
}

func roseParts() []part {
	petals := petalRing(6, 42, part{shape: shapeEllipse, w: 72, h: 50, fill: fromHex(0xE23B5D), offsetY: -20})
	out := []part{{shape: shapeRect, w: 10, h: 90, rounded: 5, fill: fromHex(0x2C7A42), offsetY: 110}}
	out = append(out, petals...)
	return append(out, part{shape: shapeEllipse, w: 40, h: 40, fill: fromHex(0xB81F3E), offsetY: -20})
}

func badgeParts() []part {
	return []part{
		{shape: shapeStar, points: 5, innerRatio: 1, w: 260, fill: fromHex(0x3E7BFA), rotationDeg: 180},
		{shape: shapeStar, points: 5, innerRatio: 0.5, w: 110, fill: fromHex(0xFFFFFF), offsetY: -10, motion: motionPulse},
	}
}

func crownParts(gold rgb) []part {
	gem := fromHex(0x4FD8E8)
	return []part{
		// shapePolygon (not shapeStar) for the spikes: a "star" with
		// innerRatio 1 still alternates outer/inner vertices at the SAME
		// radius, doubling to 2*points corners (a near-hexagon for
		// points:3, not a triangle) -- shapePolygon emits exactly `points`
		// corners, the clean spike shape this actually wants.
		{shape: shapePolygon, points: 3, w: 110, fill: gold, offsetX: -70, offsetY: -5},
		{shape: shapePolygon, points: 3, w: 150, fill: gold, offsetY: -30},
		{shape: shapePolygon, points: 3, w: 110, fill: gold, offsetX: 70, offsetY: -5},
		{shape: shapeRect, w: 200, h: 50, rounded: 10, fill: gold, offsetY: 60},
		{shape: shapePolygon, points: 4, w: 60, fill: gem, offsetY: 50, motion: motionPulse},
	}
}

// --- pattern builders --------------------------------------------------------
//
// Patterns are small single-shape glyphs; real Telegram clients tint a
// pattern with the backdrop's own pattern colour, so the fill used here only
// matters for our own untinted admin preview.

func patternDot() []part {
	return []part{{shape: shapeEllipse, w: 140, h: 140, fill: fromHex(0xFFFFFF)}}
}

func patternStar() []part {
	return []part{{shape: shapeStar, points: 5, innerRatio: 0.5, w: 180, fill: fromHex(0xFFFFFF)}}
}

func patternDiamond() []part {
	return []part{{shape: shapePolygon, points: 4, w: 170, fill: fromHex(0xFFFFFF)}}
}

func patternSpark() []part {
	return []part{{shape: shapeStar, points: 8, innerRatio: 0.35, w: 190, fill: fromHex(0xFFFFFF)}}
}

// --- shared backdrop palette -------------------------------------------------

type backdrop struct {
	name                                  string
	center, edge, pattern, text, permille int
}

func backdropPalette() []backdrop {
	return []backdrop{
		{"Azure", 0x1F8FFF, 0x0B4FA8, 0x0B4FA8, 0xFFFFFF, 500},
		{"Blush", 0xFF8FB3, 0xC23A63, 0xC23A63, 0xFFFFFF, 500},
		{"Golden Hour", 0xFFC542, 0xB8811A, 0xB8811A, 0x3A2A00, 500},
		{"Emerald", 0x3FAE5C, 0x1F6E38, 0x1F6E38, 0xFFFFFF, 500},
		{"Slate", 0x5B6472, 0x2E343C, 0x2E343C, 0xFFFFFF, 500},
	}
}

func hexString(v int) string { return fmt.Sprintf("#%06X", v) }

func backdropSpecs(names ...string) []giftpack.BackdropSpec {
	all := backdropPalette()
	byName := make(map[string]backdrop, len(all))
	for _, b := range all {
		byName[b.name] = b
	}
	out := make([]giftpack.BackdropSpec, 0, len(names))
	for _, n := range names {
		b, ok := byName[n]
		if !ok {
			panic("giftpackdefault: unknown backdrop " + n)
		}
		out = append(out, giftpack.BackdropSpec{
			Name: b.name, Center: hexString(b.center), Edge: hexString(b.edge),
			Pattern: hexString(b.pattern), Text: hexString(b.text), Permille: b.permille,
		})
	}
	return out
}
