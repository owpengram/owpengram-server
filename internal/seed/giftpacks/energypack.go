package giftpacks

import (
	"telesrv/internal/app/giftpack"
)

// Energy Pack is the donation-driver pack: a single gift, deliberately
// scarce (a hard supply cap AND a per-account cap), upgradeable into a very
// large flavour pool, with four cans obtainable only by crafting. One gift
// with fifty collectible faces gives a donor something to keep chasing,
// where a dozen ordinary gifts give them one purchase and nothing after it.
//
// The flavours are named after a real drink line-up; the art is this
// repo's own -- a generic can silhouette in each flavour's colours, drawn
// with the same DSL and the same staging as every other pack here. No logo,
// wordmark or trade dress of any real brand is reproduced.
func init() {
	register(&packDef{
		id:          "energy-pack",
		name:        "Energy Pack",
		author:      "OwpenGram",
		description: "One can, fifty flavours. Strictly limited, upgradeable, craftable.",
		icon:        "can",
		gifts: []giftDef{
			{slug: "can", title: "Energy Can", stars: 300, build: can,
				spec: giftpack.GiftSpec{
					ConvertStars: 240,
					// Scarce on both axes: the pack only ever mints 5000, and
					// nobody can corner it -- five per account.
					Limited: true, AvailabilityTotal: 5000,
					LimitedPerUser: true, PerUserTotal: 5,
					ResellMinStars: 500,
				},
				upgrade: &upgradeDef{
					stars: 400, supply: 5000,
					models:    canModels(),
					patterns:  []attrDef{patternBolt, patternIce, patternFlame, patternSparkle},
					backdrops: []string{"Toxic Green", "Ice Blue", "Crimson", "Mango", "Ultra White", "Midnight", "Violet", "Sunrise"},
				}},
		},
		backdrops: map[string]giftpack.BackdropSpec{
			"Toxic Green": {Center: "#8CFF00", Edge: "#1E5E00", Pattern: "#123E00", Text: "#0A2200", Permille: 500},
			"Ice Blue":    {Center: "#BFE8FF", Edge: "#1E6A9A", Pattern: "#0E4668", Text: "#062033", Permille: 500},
			"Crimson":     {Center: "#FF5A5A", Edge: "#8A1620", Pattern: "#5E0E15", Text: "#FFFFFF", Permille: 500},
			"Mango":       {Center: "#FFC24A", Edge: "#A85A10", Pattern: "#733A06", Text: "#2E1600", Permille: 500},
			"Ultra White": {Center: "#FFFFFF", Edge: "#9AA6B4", Pattern: "#6E7A88", Text: "#1A1F26", Permille: 500},
			"Midnight":    {Center: "#3A4250", Edge: "#0B0D12", Pattern: "#05060A", Text: "#FFFFFF", Permille: 500},
			"Violet":      {Center: "#C9A8FF", Edge: "#5E2FB8", Pattern: "#3E1C80", Text: "#FFFFFF", Permille: 500},
			"Sunrise":     {Center: "#FFD78A", Edge: "#D0561E", Pattern: "#8A3410", Text: "#2E1000", Permille: 500},
		},
	})
}

// ------------------------------------------------------------ flavours

// canStyle is one can's paint: body is the cylinder's light/mid/dark tones
// (the horizontal gradient that makes it read as round), accent is the
// emblem and the flavour band.
type canStyle struct {
	name   string
	body   [3]int
	accent int
}

var (
	bodyBlack  = [3]int{0x4A505A, 0x23262C, 0x0A0B0E}
	bodyWhite  = [3]int{0xFFFFFF, 0xDFE5ED, 0x9AA4B2}
	bodySilver = [3]int{0xF4F8FD, 0xBFC8D4, 0x6E7784}
)

// canFlavours is the whole line-up, in the order the pool is drawn from.
// Anything with a non-empty rarity is craft-only: never handed out by the
// random upgrade, only assembled from duplicates.
var canFlavours = []struct {
	id, name string
	style    canStyle
	permille int
	rarity   string
}{
	// --- the original black cans
	{"m-can-og", "Original Green", canStyle{"Original Green", bodyBlack, 0x8CFF00}, 60, ""},
	{"m-can-zero", "Zero Sugar", canStyle{"Zero Sugar", bodyBlack, 0xD8FF8A}, 55, ""},
	{"m-can-strawberry-shot", "Strawberry Shot", canStyle{"Strawberry Shot", bodyBlack, 0xFF3B5C}, 40, ""},
	{"m-can-zero-strawberry", "Zero Strawberry Shot", canStyle{"Zero Strawberry Shot", bodyBlack, 0xFF7A94}, 35, ""},
	{"m-can-electric-blue", "Electric Blue", canStyle{"Electric Blue", bodyBlack, 0x2FC8FF}, 40, ""},
	{"m-can-dreamsicle", "Orange Dreamsicle", canStyle{"Orange Dreamsicle", bodyBlack, 0xFF9A3C}, 35, ""},
	{"m-can-lo-carb", "Lo-Carb", canStyle{"Lo-Carb", bodyBlack, 0x4FA8FF}, 35, ""},

	// --- the white Ultra line
	{"m-can-ultra-white", "Zero Ultra", canStyle{"Zero Ultra", bodyWhite, 0xBFE8FF}, 50, ""},
	{"m-can-ultra-razz", "Red White Blue Razz", canStyle{"Red White Blue Razz", bodyWhite, 0x3B6CF6}, 30, ""},
	{"m-can-ultra-punk", "Punk Punch", canStyle{"Punk Punch", bodyWhite, 0xFF4FD8}, 28, ""},
	{"m-can-ultra-hawaiian", "Blue Hawaiian", canStyle{"Blue Hawaiian", bodyWhite, 0x2FC8FF}, 28, ""},
	{"m-can-ultra-guava", "Vice Guava", canStyle{"Vice Guava", bodyWhite, 0xFF7BAA}, 26, ""},
	{"m-can-ultra-passion", "Wild Passion", canStyle{"Wild Passion", bodyWhite, 0xFF5E7E}, 26, ""},
	{"m-can-ultra-dreams", "Strawberry Dreams", canStyle{"Strawberry Dreams", bodyWhite, 0xFF9AB8}, 26, ""},
	{"m-can-ultra-sunrise", "Sunrise", canStyle{"Sunrise", bodyWhite, 0xFFC24A}, 26, ""},
	{"m-can-ultra-violet", "Violet", canStyle{"Violet", bodyWhite, 0x9A6CFF}, 24, ""},
	{"m-can-ultra-peachy", "Peachy Keen", canStyle{"Peachy Keen", bodyWhite, 0xFFB07A}, 24, ""},
	{"m-can-ultra-ruby", "Fantasy Ruby Red", canStyle{"Fantasy Ruby Red", bodyWhite, 0xE0243C}, 22, ""},
	{"m-can-ultra-paradise", "Paradise", canStyle{"Paradise", bodyWhite, 0x4FE0A0}, 22, ""},
	{"m-can-ultra-mango", "Fiesta Mango", canStyle{"Fiesta Mango", bodyWhite, 0xFFA02A}, 22, ""},
	{"m-can-ultra-watermelon", "Watermelon", canStyle{"Watermelon", bodyWhite, 0xFF5A5A}, 22, ""},
	{"m-can-ultra-rosa", "Rosá", canStyle{"Rosa", bodyWhite, 0xFF8FA8}, 20, ""},
	{"m-can-ultra-red", "Ultra Red", canStyle{"Ultra Red", bodyWhite, 0xE23A3A}, 20, ""},
	{"m-can-ultra-blue", "Ultra Blue", canStyle{"Ultra Blue", bodyWhite, 0x3B7CF6}, 20, ""},
	{"m-can-ultra-black", "Ultra Black", canStyle{"Ultra Black", bodyBlack, 0xEDEFF2}, 18, ""},

	// --- coffee
	{"m-can-mean-bean", "Mean Bean", canStyle{"Mean Bean", [3]int{0xEADCC4, 0xB08A5E, 0x5E4026}, 0x8B5A2B}, 18, ""},
	{"m-can-loca-moca", "Loca Moca", canStyle{"Loca Moca", [3]int{0xE0C9AC, 0x9A7048, 0x543018}, 0x7A4A22}, 18, ""},
	{"m-can-salted-caramel", "Salted Caramel", canStyle{"Salted Caramel", [3]int{0xF4E0C2, 0xC49A5E, 0x7A5222}, 0xE0A04A}, 16, ""},
	{"m-can-cafe-latte", "Café Latte", canStyle{"Cafe Latte", [3]int{0xF7E9D4, 0xCFAE86, 0x8A6440}, 0xC08A4A}, 16, ""},
	{"m-can-irish-creme", "Irish Crème", canStyle{"Irish Creme", [3]int{0xF2E2CE, 0xC9A67E, 0x7E5A38}, 0xA8763C}, 16, ""},
	{"m-can-brew-moca", "Killer Brew Loca Moca", canStyle{"Killer Brew Loca Moca", [3]int{0xC9AE8E, 0x7E5A34, 0x3A2410}, 0xD08A3A}, 14, ""},
	{"m-can-brew-bean", "Killer Brew Mean Bean", canStyle{"Killer Brew Mean Bean", [3]int{0xD4BE9C, 0x8A6540, 0x402A14}, 0xBE8034}, 14, ""},

	// --- juice
	{"m-can-mango-loco", "Mango Loco", canStyle{"Mango Loco", [3]int{0xFFE9B0, 0xF0A83C, 0x9A5A10}, 0xFFA51F}, 22, ""},
	{"m-can-voodoo-grape", "Voodoo Grape", canStyle{"Voodoo Grape", [3]int{0xE0C6FF, 0x8A4FD8, 0x3E1C80}, 0xB07AFF}, 20, ""},
	{"m-can-strawberry-lemonade", "Strawberry Lemonade", canStyle{"Strawberry Lemonade", [3]int{0xFFE6C8, 0xFF8FA8, 0xA8324C}, 0xFF6A8A}, 20, ""},
	{"m-can-pacific-punch", "Pacific Punch", canStyle{"Pacific Punch", [3]int{0xC6EBFF, 0x3DA8E0, 0x155A7A}, 0x2FC8FF}, 20, ""},
	{"m-can-viking-berry", "Viking Berry", canStyle{"Viking Berry", [3]int{0xC6CCFF, 0x5A6CFF, 0x232A7A}, 0x7A8AFF}, 18, ""},
	{"m-can-bad-apple", "Bad Apple", canStyle{"Bad Apple", [3]int{0xDCFFB0, 0x7ACC1F, 0x2E5A0A}, 0x8CFF00}, 18, ""},
	{"m-can-rio-punch", "Rio Punch", canStyle{"Rio Punch", [3]int{0xFFD8B0, 0xFF8A3C, 0xA84A10}, 0xFF7A3C}, 18, ""},
	{"m-can-pipeline-punch", "Pipeline Punch", canStyle{"Pipeline Punch", [3]int{0xFFD6E4, 0xFF7AAA, 0xA83A6A}, 0xFF9AC2}, 18, ""},

	// --- tea
	{"m-can-tea-lemonade", "Tea + Lemonade", canStyle{"Tea Lemonade", [3]int{0xFFF3C0, 0xF0C84A, 0x9A7A10}, 0xFFD24A}, 16, ""},
	{"m-can-peach-tea", "Peach Tea", canStyle{"Peach Tea", [3]int{0xFFE6D4, 0xF0A078, 0xA85A38}, 0xFFA87A}, 16, ""},
	{"m-can-wild-berry-tea", "Wild Berry Tea", canStyle{"Wild Berry Tea", [3]int{0xFFC8DE, 0xD05A9A, 0x7A1E4A}, 0xE07AB0}, 16, ""},
	{"m-can-green-tea", "Green Tea", canStyle{"Green Tea", [3]int{0xE2F4C8, 0x8AC44A, 0x3E6A18}, 0x9ADC5A}, 16, ""},

	// --- craft only: the cans you can never buy or roll
	{"m-can-reserve-dreamsicle", "Reserve Orange Dreamsicle", canStyle{"Reserve Orange Dreamsicle", [3]int{0xFFF3DC, 0xE8C79A, 0xA87B4A}, 0xFFB347}, 0, "rare"},
	{"m-can-reserve-peaches", "Reserve Peaches n' Crème", canStyle{"Reserve Peaches", [3]int{0xFFEFE4, 0xF0C4AC, 0xB07E64}, 0xFFC2A0}, 0, "epic"},
	{"m-can-nitro", "Nitro Super Dry", canStyle{"Nitro Super Dry", bodySilver, 0x3DE0FF}, 0, "epic"},
	{"m-can-import", "Super-Premium Import", canStyle{"Super-Premium Import", [3]int{0xFFF0B8, 0xE0B84A, 0x8A6A14}, 0xFFD24A}, 0, "legendary"},
}

func canModels() []attrDef {
	out := make([]attrDef, 0, len(canFlavours))
	for _, f := range canFlavours {
		style := f.style
		out = append(out, attrDef{
			id: f.id, name: f.name, permille: f.permille, rarity: f.rarity,
			build: func() *scene { return canWith(style) },
		})
	}
	return out
}

// ------------------------------------------------------------ patterns

var (
	patternIce = attrDef{id: "p-ice", name: "Ice", permille: 450, build: glyph("Ice",
		G(polyPath(false, P{0, -180}, P{0, 180}), stroke(white, 30)),
		GRot(0, 0, 60, G(polyPath(false, P{0, -180}, P{0, 180}), stroke(white, 30))),
		GRot(0, 0, -60, G(polyPath(false, P{0, -180}, P{0, 180}), stroke(white, 30))),
		G(polyPath(false, P{-56, -128}, P{0, -84}, P{56, -128}), stroke(white, 26)),
		G(polyPath(false, P{-56, 128}, P{0, 84}, P{56, 128}), stroke(white, 26)))}

	patternFlame = attrDef{id: "p-flame", name: "Flame", permille: 400, build: glyph("Flame",
		G(sharpSpline([]int{0, 4}, 1,
			P{0, -186}, P{62, -52}, P{90, 38}, P{74, 142}, P{0, 96}, P{-74, 142}, P{-90, 38}, P{-62, -52}),
			fill(white)))}
)

// sharpSpline is smoothPath for a closed shape with deliberate corners: the
// listed vertices keep zero handles, so the edges meeting there arrive
// straight and form a point, while every other vertex is rounded. A flame
// needs exactly that -- its tip and the notch between its two feet -- since
// a fully smoothed spline rounds both off into a leaf, and hand-written
// handles get the tangent signs wrong in a way that only shows up as a dent
// once rendered.
func sharpSpline(sharp []int, tension float64, pts ...P) shItem {
	n := len(pts)
	in := make([]P, n)
	out := make([]P, n)
	corner := make(map[int]bool, len(sharp))
	for _, i := range sharp {
		corner[i] = true
	}
	for i := 0; i < n; i++ {
		if corner[i] {
			continue
		}
		prev, next := pts[(i-1+n)%n], pts[(i+1)%n]
		tx := (next[0] - prev[0]) / 6 * tension
		ty := (next[1] - prev[1]) / 6 * tension
		out[i] = P{tx, ty}
		in[i] = P{-tx, -ty}
	}
	return pathFrom(true, pts, in, out)
}

// ------------------------------------------------------------ the can

func can() *scene { return canWith(canFlavours[0].style) }

// canWith draws one 500ml can: a tapered cylinder shaded by a horizontal
// three-stop gradient (what makes it read as round rather than as a
// rectangle), a brushed-aluminium lid with a pull tab, a flavour band, a
// bolt emblem, and the pack-standard staging -- soft floor shadow, slow
// float, slight tilt sway.
func canWith(st canStyle) *scene {
	s := &scene{name: st.name}
	root := s.stage(206, 300, 12)
	accent := hx(st.accent)

	// The aura is the "energy": a slow accent-coloured pulse behind the can,
	// the one thing that makes a shelf of 50 flavour variants feel alive.
	ak := pivot(0, 0)
	ak.S = avLin(sampled(18, 0, func(u float64) []float64 {
		v := 88 + 18*bump(u, 0)
		return []float64{v, v, 100}
	})...)
	ak.O = avLin(sampled(18, 0, func(u float64) []float64 { return []float64{34 + 36*bump(u, 0)} })...)
	s.shape("aura", root, ak, glow(0, 0, 230, accent, 0.55))

	tk := pivot(0, 150)
	tk.R = avLin(sampled(18, 0, func(u float64) []float64 { return []float64{-1.4 + 1.4*wave(u, 0)} })...)
	tilt := s.null("tilt", root, tk)

	light, mid, dark := hx(st.body[0]), hx(st.body[1]), hx(st.body[2])
	// Cylinder shading: dark rim, bright specular a third of the way in,
	// darker again on the shaded right edge.
	barrel := lin(-84, 0, 84, 0,
		S(0, dark), S(0.12, mid), S(0.34, light), S(0.62, mid), S(0.86, dark.d(0.15)), S(1, dark.d(0.35)))

	body := roundedPath([]float64{18, 14, 10, 10, 14, 18},
		P{-84, 152}, P{-84, -104}, P{-58, -150}, P{58, -150}, P{84, -104}, P{84, 152})

	items := []any{
		G(body, barrel),
		// Base: the ellipse that stops the bottom reading as a flat cut.
		G(ell(0, 148, 164, 34), lin(-82, 0, 82, 0, S(0, dark.d(0.3)), S(0.4, mid.d(0.25)), S(1, dark.d(0.45)))),
		G(ell(0, 154, 130, 20), fillA(black, 0.28)),
		// Specular strip and the shaded edge opposite it.
		G(rect(-46, 6, 20, 240, 10), fillA(white, 0.3)),
		G(rect(-26, 6, 8, 214, 4), fillA(white, 0.18)),
		G(rect(64, 10, 22, 226, 11), fillA(black, 0.16)),
		// Flavour stripe: a slim band low on the can, not a slab across its
		// bottom third. The emblem already carries the flavour colour, and a
		// block that size swallowed the whole can on the dark variants.
		G(rect(0, 118, 92, 20, 9), fillA(accent, 0.92)),
		// Emblem.
		GAt(0, -34, 0, G(polyPath(true, P{22, -74}, P{-46, 10}, P{-5, 10}, P{-19, 74}, P{46, -14}, P{5, -14}), fill(accent))),
		GAt(0, -34, 0, G(polyPath(true, P{22, -74}, P{-46, 10}, P{-5, 10}, P{-19, 74}, P{46, -14}, P{5, -14}), strokeA(white, 3, 0.55))),
		// Shoulder highlight, then the brushed-aluminium lid: an outer rim,
		// a recessed inner face and the pull tab.
		G(rect(-30, -126, 44, 16, 8), fillA(white, 0.22)),
		G(ell(0, -144, 122, 24), fillA(black, 0.25)),
		G(ell(0, -152, 124, 28), silver(-62, -166, 62, -138)),
		G(ell(0, -152, 124, 28), strokeA(white, 2, 0.5)),
		G(ell(0, -152, 100, 19), lin(-50, 0, 50, 0, S(0, hx(0x9BA3AF)), S(0.42, hx(0xEFF3F8)), S(1, hx(0x828B98)))),
		G(ell(0, -153, 64, 11), fillA(black, 0.18)),
		G(ell(-17, -153, 27, 10), strokeA(white, 2.5, 0.85)),
		G(rect(11, -153, 36, 9, 4.5), fillA(white, 0.9)),
		// Condensation.
		G(ell(-54, -62, 9, 13), fillA(white, 0.35)),
		G(ell(-38, 34, 7, 10), fillA(white, 0.28)),
		G(ell(50, -8, 8, 11), fillA(white, 0.22)),
		G(ell(38, 118, 6, 9), fillA(white, 0.3)),
	}
	s.shape("can", tilt, pivot(0, 0), items...)

	// Fizz: three bubbles rising off the lid, each fading in fast and
	// dissolving slowly, so the loop never shows a bubble popping into
	// existence mid-air.
	for _, b := range []struct{ x, size, phase float64 }{{-22, 13, 0}, {6, 9, 0.38}, {26, 11, 0.72}} {
		bk := pivot(0, 0)
		bk.P = apLin(sampled(18, b.phase, func(u float64) []float64 {
			return []float64{9 * wave(u, 0), -74 * u, 0}
		})...)
		bk.O = avLin(sampled(18, b.phase, func(u float64) []float64 {
			return []float64{100 * smooth01(0, 0.16, u) * (1 - smooth01(0.5, 1, u))}
		})...)
		s.shape("fizz", tilt, bk,
			G(ell(b.x, -178, b.size, b.size), fillA(accent, 0.55)),
			G(ell(b.x, -178, b.size, b.size), strokeA(white, 2, 0.7)),
			G(ell(b.x-b.size*0.22, -178-b.size*0.22, b.size*0.3, b.size*0.3), fillA(white, 0.9)),
		)
	}

	s.sparkle(tilt, -124, -118, 32, accent, 0.15)
	s.sparkle(tilt, 122, 24, 24, white, 0.6)
	return s
}
