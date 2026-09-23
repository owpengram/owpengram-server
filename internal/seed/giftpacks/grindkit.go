package giftpacks

import (
	"math"

	"telesrv/internal/app/giftpack"
)

// Grind Kit doubles as the reference pack for the whole Star Gift feature
// surface: every gift exercises a different mechanic, so importing it is
// enough to test plain purchase, limited supply and sell-out, premium-only,
// birthday, support-only, per-user limits, collectible upgrade, crafting,
// resale floor and auction end to end.
func init() {
	register(&packDef{
		id:          "grind-kit",
		name:        "Grind Kit",
		author:      "OwpenGram",
		description: "From the first coffee to a safe full of cash: the tech and money of the daily grind. Covers upgrades, crafting, limited supply, resale, premium-only, per-user limits and auction.",
		icon:        "safe",
		gifts: []giftDef{
			{slug: "coffee", title: "Morning Coffee", stars: 15, build: coffee},
			{slug: "phone", title: "Smartphone", stars: 25, build: phone, upgrade: &upgradeDef{
				stars: 50, supply: 100_000,
				models: []attrDef{
					{id: "m-phone-midnight", name: "Midnight", build: func() *scene { return phoneWith(phoneMidnight) }, permille: 400},
					{id: "m-phone-rose", name: "Rose Gold", build: func() *scene { return phoneWith(phoneRose) }, permille: 300},
					{id: "m-phone-mint", name: "Mint", build: func() *scene { return phoneWith(phoneMint) }, permille: 300},
				},
				patterns:  []attrDef{patternHearts, patternSparkle, patternCode},
				backdrops: []string{"Midnight Blue", "Rose Quartz", "Mint Leaf"},
			}},
			{slug: "laptop", title: "Laptop", stars: 35, build: laptop, upgrade: &upgradeDef{
				stars: 60, supply: 100_000,
				models: []attrDef{
					{id: "m-laptop-silver", name: "Silver", build: func() *scene { return laptopWith(laptopSilver) }, permille: 400},
					{id: "m-laptop-spacegray", name: "Space Gray", build: func() *scene { return laptopWith(laptopSpaceGray) }, permille: 300},
					{id: "m-laptop-midnight", name: "Midnight", build: func() *scene { return laptopWith(laptopMidnight) }, permille: 200},
					{id: "m-laptop-starlight", name: "Starlight", build: func() *scene { return laptopWith(laptopStarlight) }, permille: 100},
				},
				patterns:  []attrDef{patternCode, patternSparkle, patternStars},
				backdrops: []string{"Graphite", "Midnight Blue", "Mint Leaf"},
			}},
			{slug: "desktop", title: "Desktop PC", stars: 40, build: desktop,
				spec: giftpack.GiftSpec{RequirePremium: true},
				upgrade: &upgradeDef{
					stars: 70, supply: 100_000,
					models: []attrDef{
						{id: "m-desktop-rgb", name: "RGB", build: func() *scene { return desktopWith(desktopRGB) }, permille: 400},
						{id: "m-desktop-toxic", name: "Toxic", build: func() *scene { return desktopWith(desktopToxic) }, permille: 250},
						{id: "m-desktop-inferno", name: "Inferno", build: func() *scene { return desktopWith(desktopInferno) }, permille: 200},
						{id: "m-desktop-frost", name: "Frost", build: func() *scene { return desktopWith(desktopFrost) }, permille: 150},
					},
					patterns:  []attrDef{patternBolt, patternCode},
					backdrops: []string{"Neon Night", "Graphite", "Crimson"},
				}},
			{slug: "server", title: "Data Server", stars: 55, build: server,
				spec: giftpack.GiftSpec{Limited: true, AvailabilityTotal: 1000, ResellMinStars: 150},
				upgrade: &upgradeDef{
					stars: 100, supply: 1000,
					models: []attrDef{
						{id: "m-server-classic", name: "Classic Rack", build: func() *scene { return serverWith(serverClassic) }, permille: 400},
						{id: "m-server-neon", name: "Neon Rack", build: func() *scene { return serverWith(serverNeon) }, permille: 350},
						{id: "m-server-crimson", name: "Crimson Rack", build: func() *scene { return serverWith(serverCrimson) }, permille: 250},
						{id: "m-server-quantum", name: "Quantum Core", build: func() *scene { return serverWith(serverQuantum) }, rarity: "legendary"},
					},
					patterns:  []attrDef{patternBolt, patternCode},
					backdrops: []string{"Graphite", "Neon Night", "Crimson"},
				}},
			{slug: "headphones", title: "Headphones", stars: 20, build: headphones, upgrade: &upgradeDef{
				stars: 40, supply: 100_000,
				models: []attrDef{
					{id: "m-headphones-midnight", name: "Midnight", build: func() *scene { return headphonesWith(headphonesMidnight) }, permille: 350},
					{id: "m-headphones-starlight", name: "Starlight", build: func() *scene { return headphonesWith(headphonesStarlight) }, permille: 250},
					{id: "m-headphones-sky", name: "Sky Blue", build: func() *scene { return headphonesWith(headphonesSky) }, permille: 150},
					{id: "m-headphones-pink", name: "Pink", build: func() *scene { return headphonesWith(headphonesPink) }, permille: 150},
					{id: "m-headphones-green", name: "Green", build: func() *scene { return headphonesWith(headphonesGreen) }, permille: 100},
				},
				patterns:  []attrDef{patternNotes, patternHearts, patternSparkle},
				backdrops: []string{"Graphite", "Rose Quartz", "Midnight Blue"},
			}},
			{slug: "coin", title: "Lucky Coin", stars: 15, build: coin,
				spec: giftpack.GiftSpec{LimitedPerUser: true, PerUserTotal: 3},
				upgrade: &upgradeDef{
					stars: 25, supply: 100_000,
					models: []attrDef{
						{id: "m-coin-silver", name: "Silver", build: func() *scene { return coinWith(coinSilver) }, permille: 500},
						{id: "m-coin-gold", name: "Gold", build: func() *scene { return coinWith(coinGold) }, permille: 350},
						{id: "m-coin-platinum", name: "Platinum", build: func() *scene { return coinWith(coinPlatinum) }, permille: 150},
					},
					patterns:  []attrDef{patternCoins, patternStars, patternSparkle},
					backdrops: []string{"Gold Rush", "Graphite", "Mint Leaf"},
				}},
			{slug: "wallet", title: "Leather Wallet", stars: 25, build: wallet, upgrade: &upgradeDef{
				stars: 45, supply: 100_000,
				models: []attrDef{
					{id: "m-wallet-classic", name: "Classic", build: func() *scene { return walletWith(walletClassic) }, permille: 400},
					{id: "m-wallet-noir", name: "Noir", build: func() *scene { return walletWith(walletNoir) }, permille: 300},
					{id: "m-wallet-crimson", name: "Crimson", build: func() *scene { return walletWith(walletCrimson) }, permille: 200},
					{id: "m-wallet-navy", name: "Navy", build: func() *scene { return walletWith(walletNavy) }, permille: 100},
				},
				patterns:  []attrDef{patternCoins, patternDiamonds, patternStars},
				backdrops: []string{"Gold Rush", "Crimson", "Mint Leaf"},
			}},
			{slug: "safe", title: "Money Safe", stars: 250, build: safe,
				spec: giftpack.GiftSpec{Auction: true, AuctionSlug: "money-safe", GiftsPerRound: 3, AvailabilityTotal: 15, Limited: true},
				upgrade: &upgradeDef{
					stars: 200, supply: 15,
					models: []attrDef{
						{id: "m-safe-steel", name: "Steel", build: func() *scene { return safeWith(safeSteel) }, permille: 500},
						{id: "m-safe-gold", name: "Gold", build: func() *scene { return safeWith(safeGold) }, permille: 300},
						{id: "m-safe-obsidian", name: "Obsidian", build: func() *scene { return safeWith(safeObsidian) }, permille: 200},
					},
					patterns:  []attrDef{patternDiamonds, patternCoins},
					backdrops: []string{"Gold Rush", "Midnight Blue", "Graphite"},
				}},
		},
		backdrops: map[string]giftpack.BackdropSpec{
			"Midnight Blue": {Center: "#3B6CF6", Edge: "#172466", Pattern: "#0E1A4F", Text: "#FFFFFF", Permille: 500},
			"Rose Quartz":   {Center: "#FF9AB8", Edge: "#B8406A", Pattern: "#8E2A50", Text: "#FFFFFF", Permille: 500},
			"Mint Leaf":     {Center: "#5EE6C1", Edge: "#138A7A", Pattern: "#0B5E53", Text: "#FFFFFF", Permille: 500},
			"Neon Night":    {Center: "#8A5CFF", Edge: "#2A1566", Pattern: "#FF4FD8", Text: "#FFFFFF", Permille: 500},
			"Gold Rush":     {Center: "#FFD24A", Edge: "#B8801A", Pattern: "#8A5A0A", Text: "#3A2A00", Permille: 500},
			"Graphite":      {Center: "#6A7384", Edge: "#262B34", Pattern: "#1A1D23", Text: "#FFFFFF", Permille: 500},
			"Crimson":       {Center: "#FF5A5A", Edge: "#8A1620", Pattern: "#5E0E15", Text: "#FFFFFF", Permille: 500},
		},
	})
}

// ------------------------------------------------------------ patterns
//
// Collectible patterns are single glyphs the client tiles over the backdrop
// and tints with the backdrop's pattern colour, so they stay flat and white.

func glyph(name string, items ...any) func() *scene {
	return func() *scene {
		s := &scene{name: name}
		k := pivot(0, 0)
		k.P = sv(pt3(cx, cy, 0))
		s.shape("glyph", 0, k, items...)
		return s
	}
}

var (
	patternSparkle  = attrDef{id: "p-sparkle", name: "Sparkle", permille: 400, build: glyph("Sparkle", G(star(0, 0, 4, 170, 44, 0), fill(white)))}
	patternStars    = attrDef{id: "p-stars", name: "Stars", permille: 350, build: glyph("Stars", G(star(0, 0, 5, 170, 76, 0), fill(white)))}
	patternHearts   = attrDef{id: "p-hearts", name: "Hearts", permille: 350, build: glyph("Hearts", G(heart(0, 12, 320), fill(white)))}
	patternDiamonds = attrDef{id: "p-diamonds", name: "Diamonds", permille: 500, build: glyph("Diamonds", G(polyPath(true, P{0, -170}, P{130, 0}, P{0, 170}, P{-130, 0}), fill(white)))}
	patternCoins    = attrDef{id: "p-coins", name: "Coins", permille: 450, build: glyph("Coins",
		G(ell(0, 0, 280, 280), stroke(white, 44)),
		G(ell(0, 0, 100, 100), fill(white)))}
	patternBolt = attrDef{id: "p-bolt", name: "Bolt", permille: 550, build: glyph("Bolt",
		G(polyPath(true, P{40, -180}, P{-110, 25}, P{-12, 25}, P{-45, 180}, P{110, -35}, P{12, -35}), fill(white)))}
	patternNotes = attrDef{id: "p-notes", name: "Notes", permille: 500, build: glyph("Notes",
		GRot(-40, 90, -22, G(ell(-40, 90, 120, 92), fill(white))),
		G(rect(8, -10, 24, 210, 12), fill(white)),
		G(smoothPath(false, 1, P{12, -110}, P{80, -62}, P{76, 6}), stroke(white, 26)))}
	patternCode = attrDef{id: "p-code", name: "Code", permille: 450, build: glyph("Code",
		G(polyPath(false, P{-70, -100}, P{-170, 0}, P{-70, 100}), stroke(white, 40)),
		G(polyPath(false, P{70, -100}, P{170, 0}, P{70, 100}), stroke(white, 40)),
		G(polyPath(false, P{36, -150}, P{-36, 150}), stroke(white, 34)))}
)

// heart path centred on (x,y), size = overall width.
func heart(x, y, size float64) shItem {
	s := size / 90
	v := []P{{50, 30}, {27, 10}, {5, 33}, {50, 90}, {95, 33}, {73, 10}}
	in := []P{{0, -10}, {13, 0}, {0, -11}, {-20, -20}, {0, 22}, {15, 0}}
	out := []P{{0, -10}, {-15, 0}, {0, 22}, {20, -20}, {0, -11}, {-13, 0}}
	for i := range v {
		v[i] = P{x + (v[i][0]-50)*s, y + (v[i][1]-50)*s}
		in[i] = P{in[i][0] * s, in[i][1] * s}
		out[i] = P{out[i][0] * s, out[i][1] * s}
	}
	return pathFrom(true, v, in, out)
}

func vgrad(x, top, bottom float64, a, b col) gfItem {
	return lin(x, top, x, bottom, S(0, a), S(1, b))
}

func silver(x0, y0, x1, y1 float64) gfItem {
	return lin(x0, y0, x1, y1, S(0, hx(0xF4F6F9)), S(0.5, hx(0xC3CAD4)), S(1, hx(0x858E9B)))
}

func goldRad(x, y, r float64) gfItem {
	return rad(x-r*0.3, y-r*0.35, r*1.3, S(0, hx(0xFFF4B8)), S(0.45, hx(0xFFCC3D)), S(1, hx(0xD98F12)))
}

func glow(x, y, r float64, c col, a float64) grItem {
	return G(ell(x, y, r*2, r*2), rad(x, y, r, SA(0, c, a), SA(1, c, 0)))
}

// ------------------------------------------------------------ coffee

func coffee() *scene {
	s := &scene{name: "Morning Coffee"}
	root := s.stage(200, 380, 10)

	// Each wisp rises, widens and sways more as it goes, fading in fast and
	// dissolving slowly: evaporation, not a loop. It is invisible at both
	// ends of its cycle, so the restart is never seen.
	for i, x0 := range []float64{-50, 4, 56} {
		x, ph := x0, float64(i)/3
		kk := pivot(x, -96)
		kk.P = apLin(sampled(30, ph, func(u float64) []float64 {
			return []float64{x + 16*u*wave(u*1.2, 0), -96 - 95*u, 0}
		})...)
		kk.O = avLin(sampled(30, ph, func(u float64) []float64 {
			return []float64{88 * smooth01(0, 0.18, u) * (1 - smooth01(0.3, 1, u))}
		})...)
		kk.S = avLin(sampled(30, ph, func(u float64) []float64 { return []float64{70 + 70*u, 80 + 45*u, 100} })...)
		s.shape("steam", root, kk,
			G(smoothPath(false, 1, P{x, -70}, P{x + 14, -98}, P{x - 12, -128}, P{x + 9, -158}), strokeA(white, 15, 0.85)))
	}

	s.shape("saucer", root, pivot(0, 0),
		G(ell(0, 156, 400, 92), vgrad(0, 110, 202, hx(0xFFFFFF), hx(0xBDB5AA))),
		G(ell(0, 150, 384, 78), vgrad(0, 111, 189, hx(0xFBF8F4), hx(0xDCD4C9))),
		G(ell(0, 146, 262, 50), vgrad(0, 121, 171, hx(0xD8CFC3), hx(0xF6F1EA))),
	)

	body := roundedPath([]float64{8, 8, 52, 52}, P{-132, -74}, P{132, -74}, P{108, 140}, P{-108, 140})
	s.shape("mug", root, pivot(0, 0),
		G(ell(134, 30, 124, 134), glin(32, 72, 0, 196, 0, S(0, hx(0xC23E26)), S(0.55, hx(0xFF8A60)), S(1, hx(0xB5381F)))),
		G(ell(134, 30, 124, 134), glin(8, 72, -37, 196, 97, SA(0, white, 0.0), SA(0.5, white, 0.35), SA(1, white, 0))),
		G(body, lin(-132, 0, 132, 0, S(0, hx(0xFFA27A)), S(0.4, hx(0xFF7049)), S(1, hx(0xC23C24)))),
		G(roundedPath([]float64{30}, P{-108, 60}, P{108, 60}, P{108, 140}, P{-108, 140}), lin(0, 60, 0, 140, SA(0, black, 0), SA(1, black, 0.22))),
		G(roundedPath([]float64{12}, P{-110, -52}, P{-84, -52}, P{-72, 104}, P{-90, 104}), lin(0, -52, 0, 104, SA(0, white, 0.6), SA(1, white, 0))),
		G(heart(10, 40, 84), fill(hx(0xB8331F))),
		G(heart(6, 34, 84), fill(hx(0xFFF3E6))),
		G(ell(0, -74, 266, 62), vgrad(0, -105, -43, hx(0xFFC2A4), hx(0xE8603E))),
		G(ell(0, -72, 240, 48), fill(hx(0x9E2F1B))),
		G(ell(0, -66, 228, 38), rad(-24, -72, 130, S(0, hx(0xA36B45)), S(0.65, hx(0x5E361F)), S(1, hx(0x3B200F)))),
		G(ell(4, -66, 140, 20), rad(4, -66, 70, SA(0, hx(0xF0C79A), 0.95), SA(0.7, hx(0xD9A06B), 0.6), SA(1, hx(0xC98E5E), 0))),
		G(heart(4, -67, 34), fillA(hx(0xFFF1DE), 0.55)),
		G(ell(-60, -71, 46, 7), fillA(white, 0.45)),
	)
	s.sparkle(root, 120, -120, 22, hx(0xFFE9B8), 0.55)
	return s
}

// ------------------------------------------------------------ phone

type app struct{ top, bottom int }

var appColors = []app{
	{0xFFC56B, 0xFF7A1A}, {0x8BEA86, 0x2FB344}, {0x7FD0FF, 0x2B7BE4},
	{0xFF9ACB, 0xE8448F}, {0xFFE68A, 0xF4A800}, {0xCBA8FF, 0x7B4DFF},
	{0x7BF5DC, 0x16B39A}, {0xFF8A80, 0xE53935}, {0xB0EDFF, 0x42A5F5},
}

func appGlyph(i int, x, y float64) grItem {
	w := fillA(white, 0.95)
	switch i % 5 {
	case 0:
		return G(ell(x, y, 18, 18), w)
	case 1:
		return G(star(x, y, 5, 12, 5.5, 0), w)
	case 2:
		return G(rect(x, y, 18, 14, 4), w)
	case 3:
		return G(heart(x, y+1, 20), w)
	default:
		return G(polyPath(true, P{x - 7, y - 9}, P{x + 9, y}, P{x - 7, y + 9}), w)
	}
}

type phoneStyle struct {
	name         string
	frame        [4]int
	wall         [3]int
	glowA, glowB int
}

var (
	phoneMidnight = phoneStyle{"Smartphone", [4]int{0xA3AAB6, 0x40465A, 0x6A7284, 0x23262E}, [3]int{0x7A5CFF, 0x3B6CF6, 0x172466}, 0xFF6FB5, 0x3DE0FF}
	phoneRose     = phoneStyle{"Rose Gold", [4]int{0xFBE3D6, 0xC08A75, 0xE9BFAD, 0x7A4E42}, [3]int{0xFFB08A, 0xFF6A88, 0x6E2456}, 0xFFE66F, 0xFF9AE0}
	phoneMint     = phoneStyle{"Mint", [4]int{0xE3F7F0, 0x6E9E90, 0xB5DDCF, 0x355A50}, [3]int{0x6FF2CF, 0x19B3A5, 0x0A4A57}, 0xB4FF6F, 0x3DE0FF}
)

func phone() *scene { return phoneWith(phoneMidnight) }

func phoneWith(st phoneStyle) *scene {
	s := &scene{name: st.name}
	root := s.stage(214, 320, 12)
	tk := pivot(0, 40)
	tk.R = avLin(sampled(18, 0, func(u float64) []float64 { return []float64{-9 + 3*wave(u, 0)} })...)
	tilt := s.null("tilt", root, tk)

	items := []any{
		G(rect(-119, -92, 8, 46, 3), fill(hx(st.frame[1]))),
		G(rect(-119, -36, 8, 46, 3), fill(hx(st.frame[1]))),
		G(rect(119, -64, 8, 74, 3), fill(hx(st.frame[1]))),
		G(rect(0, 0, 236, 416, 52), lin(-118, -208, 118, 208, S(0, hx(st.frame[0])), S(0.3, hx(st.frame[1])), S(0.65, hx(st.frame[2])), S(1, hx(st.frame[3])))),
		G(rect(0, 0, 222, 402, 45), fill(hx(0x0A0B0E))),
		G(rect(0, 0, 204, 384, 36), lin(0, -192, 0, 192, S(0, hx(st.wall[0])), S(0.55, hx(st.wall[1])), S(1, hx(st.wall[2])))),
		glow(38, 112, 62, hx(st.glowA), 0.8),
		glow(-40, -70, 60, hx(st.glowB), 0.6),
		G(rect(0, -170, 76, 22, 11), fill(hx(0x040405))),
		G(ell(22, -170, 8, 8), fill(hx(0x1B2340))),
		G(rect(-68, -170, 30, 9, 4.5), fillA(white, 0.9)),
		G(rect(68, -170, 26, 12, 3.5), strokeA(white, 2, 0.9)),
		G(rect(66, -170, 17, 6, 1.5), fillA(white, 0.9)),
	}
	idx := 0
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			x, y := -60+60*float64(c), -96+64*float64(r)
			a := appColors[idx]
			items = append(items,
				G(rect(x, y+3, 46, 46, 13), fillA(black, 0.18)),
				G(rect(x, y, 46, 46, 13), vgrad(x, y-23, y+23, hx(a.top), hx(a.bottom))),
				G(rect(x, y-12, 38, 14, 7), fillA(white, 0.18)),
				appGlyph(idx, x, y),
			)
			idx++
		}
	}
	items = append(items,
		G(rect(0, 150, 184, 62, 24), fillA(white, 0.22)),
	)
	for i, c := range []int{1, 2, 7, 5} {
		x := -63 + 42*float64(i)
		a := appColors[c]
		items = append(items,
			G(rect(x, 150, 36, 36, 10), vgrad(x, 132, 168, hx(a.top), hx(a.bottom))),
			appGlyph(c+i, x, 150),
		)
	}
	items = append(items,
		G(rect(0, 184, 70, 5, 2.5), fillA(white, 0.8)),
		G(polyPath(true, P{-20, -186}, P{50, -186}, P{-100, 40}, P{-100, -66}), fillA(white, 0.07)),
	)
	s.shape("phone", tilt, pivot(0, 0), items...)

	nk := pivot(0, -112)
	nk.S = av(k(0, 0, 0, 100), k(16, 0, 0, 100), k(30, 110, 110, 100), k(40, 100, 100, 100), k(138, 100, 100, 100), k(154, 0, 0, 100), k(180, 0, 0, 100))
	s.shape("notif", tilt, nk,
		G(rect(0, -108, 186, 58, 19), fillA(black, 0.25)),
		G(rect(0, -112, 186, 58, 19), fillA(white, 0.96)),
		G(rect(-66, -112, 36, 36, 10), vgrad(-66, -130, -94, hx(0x8BEA86), hx(0x2FB344))),
		G(ell(-66, -114, 20, 16), fill(white)),
		G(polyPath(true, P{-72, -108}, P{-64, -107}, P{-74, -100}), fill(white)),
		G(rect(12, -122, 108, 10, 5), fill(hx(0x2B2F3A))),
		G(rect(-4, -103, 76, 8, 4), fill(hx(0xA3A9B4))),
	)

	bk := pivot(84, -66)
	bk.S = av(k(0, 0, 0, 100), k(30, 0, 0, 100), k(40, 120, 120, 100), k(48, 100, 100, 100), k(140, 100, 100, 100), k(150, 0, 0, 100), k(180, 0, 0, 100))
	s.shape("badge", tilt, bk,
		G(ell(84, -66, 24, 24), fill(hx(0xFF3B30))),
		G(ell(84, -66, 24, 24), strokeA(white, 2.5, 1)),
		G(ell(84, -66, 7, 7), fill(white)),
	)
	return s
}

// ------------------------------------------------------------ laptop

type seg struct {
	off, w float64
	c      int
}

type laptopStyle struct {
	name  string
	shell [3]int
	base  [2]int
	keys  int
}

var (
	laptopSilver    = laptopStyle{"Laptop", [3]int{0xF4F6F9, 0xC3CAD4, 0x858E9B}, [2]int{0xF2F4F7, 0x9EA5B0}, 0x6E7580}
	laptopSpaceGray = laptopStyle{"Space Gray", [3]int{0xB5B9C0, 0x6E737B, 0x3C4047}, [2]int{0xA7ABB2, 0x5A5E66}, 0x2E3136}
	laptopMidnight  = laptopStyle{"Midnight", [3]int{0x6A7A9E, 0x33405F, 0x1A2238}, [2]int{0x55648A, 0x252F4A}, 0x131A2C}
	laptopStarlight = laptopStyle{"Starlight", [3]int{0xFFF3DE, 0xE3D2B4, 0xA99474}, [2]int{0xF7EBD6, 0xC4AF8E}, 0x8C7A5E}
)

func laptop() *scene { return laptopWith(laptopSilver) }

func laptopWith(st laptopStyle) *scene {
	s := &scene{name: st.name}
	root := s.stage(178, 480, 8)
	baseHi, baseLo := hx(st.base[0]), hx(st.base[1])

	items := []any{
		G(rect(0, -52, 394, 262, 26), lin(-197, -183, 197, 79, S(0, hx(st.shell[0])), S(0.5, hx(st.shell[1])), S(1, hx(st.shell[2])))),
		G(rect(0, -52, 382, 250, 20), lin(-191, -177, 191, 73, S(0, hx(0x2C313B)), S(1, hx(0x121418)))),
		G(ell(0, -166, 7, 7), fill(hx(0x3B404B))),
		G(rect(0, -44, 354, 214, 8), vgrad(0, -151, 63, hx(0x252A45), hx(0x151827))),
		G(roundedPath([]float64{8, 8, 0, 0}, P{-177, -151}, P{177, -151}, P{177, -128}, P{-177, -128}), fill(hx(0x30375A))),
		G(ell(-161, -140, 10, 10), fill(hx(0xFF5F57))),
		G(ell(-145, -140, 10, 10), fill(hx(0xFEBC2E))),
		G(ell(-129, -140, 10, 10), fill(hx(0x28C840))),
		G(rect(10, -140, 120, 10, 5), fillA(white, 0.12)),
		G(roundedPath([]float64{0, 0, 0, 8}, P{-177, -128}, P{-126, -128}, P{-126, 63}, P{-177, 63}), fill(hx(0x1C2036))),
	}
	for i := 0; i < 6; i++ {
		y := -112 + 17*float64(i)
		a := 0.3
		w := 30.0
		if i == 2 {
			a, w = 0.85, 34
			items = append(items, G(rect(-151.5, y, 44, 14, 4), fillA(hx(0x5B6CFF), 0.45)))
		}
		items = append(items, G(rect(-160+w/2, y, w, 5, 2.5), fillA(white, a)))
	}
	items = append(items,
		G(rect(0, 80, 394, 12, 4), vgrad(0, 74, 86, hx(0x575D69), hx(0x2A2E36))),
		G(roundedPath([]float64{6, 6, 16, 16}, P{-204, 84}, P{204, 84}, P{244, 136}, P{-244, 136}), vgrad(0, 84, 136, baseHi, baseLo)),
		G(roundedPath([]float64{4}, P{-170, 92}, P{170, 92}, P{186, 118}, P{-186, 118}), fillA(baseLo.d(0.1), 0.35)),
	)
	for r := 0; r < 2; r++ {
		y := 99 + 11*float64(r)
		n := 15 + r
		span := 330 + 18*float64(r)
		for c := 0; c < n; c++ {
			x := -span/2 + span*(float64(c)+0.5)/float64(n)
			items = append(items, G(rect(x, y, span/float64(n)-4, 7, 2), fill(hx(st.keys))))
		}
	}
	items = append(items,
		G(roundedPath([]float64{3}, P{-54, 121}, P{54, 121}, P{57, 132}, P{-57, 132}), fill(baseHi.d(0.15))),
		G(rect(0, 139, 488, 8, 4), fill(baseLo.d(0.2))),
		G(rect(0, 88, 64, 5, 2.5), fill(baseLo.d(0.05))),
	)
	s.shape("laptop", root, pivot(0, 0), items...)

	pal := []col{hx(0xFF79C6), hx(0x8BE9FD), hx(0x50FA7B), hx(0xF1FA8C), hx(0xBD93F9), hx(0x6272A4), hx(0xE6E8F0), hx(0xFFB86C)}
	lines := [][]seg{
		{{0, 34, 0}, {40, 64, 1}, {110, 26, 6}},
		{{18, 52, 4}, {76, 86, 3}},
		{{18, 44, 1}, {68, 36, 2}, {110, 60, 6}},
		{{36, 64, 3}, {106, 54, 2}, {166, 24, 7}},
		{{36, 46, 0}, {88, 84, 1}},
		{{18, 26, 4}, {50, 116, 5}},
		{{18, 76, 2}, {100, 40, 6}},
		{{0, 14, 6}},
	}
	x0 := -110.0
	var lastEnd, lastY float64
	for i, ln := range lines {
		y := -112 + 22*float64(i)
		ti := 10 + 13*float64(i)
		var gs []any
		for _, sg := range ln {
			gs = append(gs, G(rect(x0+sg.off+sg.w/2, y, sg.w, 9, 4.5), fill(pal[sg.c])))
			lastEnd = x0 + sg.off + sg.w
		}
		lastY = y
		lk := pivot(x0, y)
		lk.S = av(k(0, 0, 100, 100), k(ti, 0, 100, 100), k(ti+10, 100, 100, 100), k(170, 100, 100, 100), k(171, 0, 100, 100), k(180, 0, 100, 100))
		lk.O = av(k(0, 100), k(156, 100), k(170, 0), k(180, 0))
		s.shape("code", root, lk, gs...)
	}
	ck := pivot(lastEnd+8, lastY)
	ck.O = avLin(stepKeys(func(t float64) float64 {
		if int(t/15)%2 == 0 {
			return 100
		}
		return 0
	})...)
	s.shape("cursor", root, ck, G(rect(lastEnd+8, lastY, 4, 16, 1), fill(hx(0xE6E8F0))))

	s.shape("glare", root, pivot(0, 0), G(polyPath(true, P{60, -151}, P{150, -151}, P{-10, 63}, P{-100, 63}), fillA(white, 0.05)))
	return s
}

// ------------------------------------------------------------ desktop

// desktopStyle is the build's lighting: case colours, the RGB accent trio
// (strip ends a/b, fan glow mid), fan blades, chart bars and power LED.
type desktopStyle struct {
	name        string
	caseOuter   [2]int
	caseInner   int
	a, mid, b   int
	blade, bars [2]int
	power       int
}

var (
	desktopRGB     = desktopStyle{"Desktop PC", [2]int{0x505869, 0x1C1F26}, 0x121419, 0xFF4FD8, 0x9B6BFF, 0x3DE0FF, [2]int{0xC9B3FF, 0x6A4BD6}, [2]int{0x6CF6FF, 0x2B5CF6}, 0x3DFFB0}
	desktopToxic   = desktopStyle{"Toxic", [2]int{0x505869, 0x1C1F26}, 0x121419, 0x7CFF4F, 0x2FE07A, 0xFFE34F, [2]int{0xD6FFB8, 0x2FB344}, [2]int{0xC8FF6F, 0x21A34B}, 0x7CFF4F}
	desktopInferno = desktopStyle{"Inferno", [2]int{0x5A4A4A, 0x1F1616}, 0x140E0E, 0xFF4F4F, 0xFF7A3D, 0xFFC23D, [2]int{0xFFD0B3, 0xD6452B}, [2]int{0xFFD06C, 0xE8452B}, 0xFF7A3D}
	desktopFrost   = desktopStyle{"Frost", [2]int{0xF4F6F9, 0xAEB5C0}, 0xDDE3EA, 0xFFFFFF, 0x8FD8FF, 0x3DB8FF, [2]int{0xEAF7FF, 0x7FB8E0}, [2]int{0xBFEFFF, 0x3D9BE0}, 0x8FD8FF}
)

func fan(s *scene, parent int, st desktopStyle, x, y float64, phase float64) {
	gk := pivot(x, y)
	gk.O = avLin(sampled(18, phase, func(u float64) []float64 { return []float64{60 + 40*bump(u, 0)} })...)
	s.shape("fanglow", parent, gk, glow(x, y, 58, hx(st.mid), 0.55))
	s.shape("fan", parent, pivot(0, 0),
		G(ell(x, y, 82, 82), fill(hx(0x0C0D11))),
		G(ell(x, y, 82, 82), glin(5, x-41, y-41, x+41, y+41, S(0, hx(st.a)), S(0.5, hx(st.mid)), S(1, hx(st.b)))),
	)
	var blades []any
	for i := 0; i < 7; i++ {
		a := float64(i) * 360 / 7
		blades = append(blades, GRot(x, y, a, G(ell(x+6, y-19, 17, 34), lin(x, y-36, x, y, SA(0, hx(st.blade[0]), 0.9), SA(1, hx(st.blade[1]), 0.9)))))
	}
	bk := pivot(x, y)
	bk.R = avLin(k(0, 0), k(op, 720))
	s.shape("blades", parent, bk, append(blades, G(ell(x, y, 22, 22), rad(x-3, y-3, 12, S(0, hx(0x5A5F6B)), S(1, hx(0x1A1C22)))))...)
}

func desktop() *scene { return desktopWith(desktopRGB) }

func desktopWith(st desktopStyle) *scene {
	s := &scene{name: st.name}
	root := s.stage(200, 470, 8)

	tx, ty := 152.0, 28.0
	inner := hx(st.caseInner)
	s.shape("tower", root, pivot(0, 0),
		G(rect(tx, ty, 122, 316, 17), lin(tx-61, 0, tx+61, 0, S(0, hx(st.caseOuter[0])), S(1, hx(st.caseOuter[1])))),
		G(rect(tx, ty, 106, 300, 12), fill(inner)),
		G(rect(tx, ty, 106, 300, 12), lin(tx-53, ty-150, tx+53, ty+150, SA(0, hx(st.mid), 0.2), SA(1, hx(st.b), 0.12))),
		G(rect(tx, -102, 72, 7, 3.5), fill(inner.mix(hx(0x7A808C), 0.25))),
		G(rect(tx, -89, 72, 7, 3.5), fill(inner.mix(hx(0x7A808C), 0.25))),
		G(rect(tx-50, ty, 3, 280, 1.5), lin(0, ty-140, 0, ty+140, S(0, hx(st.a)), S(1, hx(st.b)))),
		G(rect(tx+50, ty, 3, 280, 1.5), lin(0, ty-140, 0, ty+140, S(0, hx(st.b)), S(1, hx(st.a)))),
	)
	fan(s, root, st, tx, -28, 0)
	fan(s, root, st, tx, 70, 0.5)
	pk := pivot(tx, 152)
	pk.O = avLin(sampled(12, 0, func(u float64) []float64 { return []float64{55 + 45*bump(u, 0)} })...)
	s.shape("power", root, pk,
		glow(tx, 152, 22, hx(st.power), 0.8),
		G(ell(tx, 152, 16, 16), rad(tx-2, 150, 9, S(0, hx(st.power).l(0.8)), S(1, hx(st.power).d(0.25)))),
	)

	mx, my := -52.0, -32.0
	s.shape("monitor", root, pivot(0, 0),
		G(ell(mx, 178, 188, 30), vgrad(mx, 163, 193, hx(0xEEF1F5), hx(0x858E9B))),
		G(ell(mx, 174, 160, 20), vgrad(mx, 164, 184, hx(0xFFFFFF), hx(0xC7CDD6))),
		G(roundedPath([]float64{4}, P{mx - 20, 76}, P{mx + 20, 76}, P{mx + 26, 172}, P{mx - 26, 172}), lin(mx-26, 0, mx+26, 0, S(0, hx(0x9AA2AE)), S(0.5, hx(0xECEFF3)), S(1, hx(0x8A929E)))),
		G(rect(mx, my, 344, 232, 20), lin(mx-172, my-116, mx+172, my+116, S(0, hx(0x4A5160)), S(1, hx(0x121418)))),
		G(rect(mx, my-7, 324, 200, 9), vgrad(mx, my-107, my+93, hx(0x16244F), hx(0x0A1030))),
		G(ell(mx, my+102, 7, 7), fillA(white, 0.45)),
		G(rect(mx-110, my-86, 80, 11, 5.5), fillA(white, 0.85)),
		G(rect(mx-110, my-66, 56, 8, 4), fillA(white, 0.35)),
		G(rect(mx+96, my-80, 76, 22, 11), fill(hx(0x1F8A5A))),
		G(polyPath(true, P{mx + 72, my - 76}, P{mx + 82, my - 86}, P{mx + 92, my - 76}), fill(hx(0x7CFFB2))),
		G(rect(mx+114, my-80, 30, 8, 4), fill(hx(0x7CFFB2))),
	)
	base := my + 70.0
	for i := 0; i < 3; i++ {
		y := base - 38*float64(i)
		s.shape("grid", root, pivot(0, 0), G(rect(mx+6, y, 280, 2, 1), fillA(white, 0.08+0.08*float64(1-min(i, 1)))))
	}
	heights := []float64{62, 92, 74, 118, 100, 132}
	for i, h := range heights {
		x := mx - 118 + 47*float64(i)
		ph := float64(i) * 0.17
		bk := pivot(x, base)
		bk.S = avLin(sampled(18, ph, func(u float64) []float64 { return []float64{100, 62 + 38*bump(u, 0), 100} })...)
		s.shape("bar", root, bk,
			G(roundedPath([]float64{6, 6, 0, 0}, P{x - 15, base - h}, P{x + 15, base - h}, P{x + 15, base}, P{x - 15, base}),
				lin(x, base-h, x, base, S(0, hx(st.bars[0])), S(1, hx(st.bars[1])))),
			G(rect(x, base-h+4, 22, 3, 1.5), fillA(white, 0.55)),
		)
	}
	s.shape("glare", root, pivot(0, 0), G(polyPath(true, P{mx + 40, my - 107}, P{mx + 120, my - 107}, P{mx - 30, my + 93}, P{mx - 110, my + 93}), fillA(white, 0.05)))
	return s
}

// ------------------------------------------------------------ server

type serverStyle struct {
	name    string
	cabinet [3]int
	unit    [2]int
	leds    [2]int
	data    int
}

var (
	serverClassic = serverStyle{"Data Server", [3]int{0x5A6376, 0x363D4B, 0x1A1D25}, [2]int{0x5A6375, 0x2B303B}, [2]int{0x4CFF8A, 0x49B6FF}, 0x3DE0FF}
	serverNeon    = serverStyle{"Neon Rack", [3]int{0x5B4A9A, 0x302658, 0x150F2B}, [2]int{0x4A3C85, 0x241C47}, [2]int{0xFF4FD8, 0x3DE0FF}, 0xFF4FD8}
	serverCrimson = serverStyle{"Crimson Rack", [3]int{0x7A3542, 0x4A1F28, 0x220C11}, [2]int{0x6E2C38, 0x3A161D}, [2]int{0xFF4D4D, 0xFFB23D}, 0xFF7A5C}
	serverQuantum = serverStyle{"Quantum Core", [3]int{0xFFE08A, 0xC99A2E, 0x6E4A0E}, [2]int{0x3A2C5E, 0x1A1230}, [2]int{0xB18CFF, 0xFFFFFF}, 0xFFD166}
)

func server() *scene { return serverWith(serverClassic) }

func serverWith(st serverStyle) *scene {
	s := &scene{name: st.name}
	root := s.stage(220, 380, 8)
	ys := []float64{-141, -47, 47, 141}
	items := []any{
		G(rect(-108, 208, 52, 16, 5), fill(hx(0x22262D))),
		G(rect(108, 208, 52, 16, 5), fill(hx(0x22262D))),
		G(rect(0, 0, 308, 414, 28), lin(-154, -207, 154, 207, S(0, hx(st.cabinet[0])), S(0.5, hx(st.cabinet[1])), S(1, hx(st.cabinet[2])))),
		G(rect(0, -204, 270, 4, 2), fillA(white, 0.25)),
		G(rect(0, 0, 284, 390, 20), fill(hx(0x0E1015))),
	}
	for _, y := range ys {
		items = append(items,
			G(rect(0, y+3, 266, 84, 13), fillA(black, 0.5)),
			G(rect(0, y, 266, 84, 13), vgrad(0, y-42, y+42, hx(st.unit[0]), hx(st.unit[1]))),
			G(rect(0, y-39, 250, 3, 1.5), fillA(white, 0.2)),
			G(rect(-125, y, 5, 60, 2.5), fill(hx(0x1C2027))),
			G(rect(125, y, 5, 60, 2.5), fill(hx(0x1C2027))),
		)
		for j := 0; j < 7; j++ {
			x := -104 + 11*float64(j)
			items = append(items, G(rect(x, y-6, 5, 46, 2.5), fill(hx(0x15181E))))
		}
		for j := 0; j < 3; j++ {
			x := -12 + 40*float64(j)
			items = append(items,
				G(rect(x, y-10, 34, 16, 4), fill(hx(0x1A1D24))),
				G(rect(x, y-10, 34, 16, 4), strokeA(white, 1.5, 0.12)),
				G(rect(x-11, y-10, 4, 8, 1), fill(hx(0x6B7280))),
			)
		}
		items = append(items, G(rect(8, y+22, 150, 6, 3), fill(hx(0x14171C))))
	}
	s.shape("rack", root, pivot(0, 0), items...)

	for i, y0 := range ys {
		y, ph := y0, float64(i)*0.27
		for j, c := range []col{hx(st.leds[0]), hx(st.leds[1])} {
			x := 98.0 + 20*float64(j)
			period := []float64{20, 30, 45, 60}[(i+j)%4]
			off := float64((i*7 + j*11) % int(period))
			lk := pivot(x, y-10)
			lk.O = avLin(stepKeys(func(t float64) float64 {
				if math.Mod(t+off, period) < period*0.55 {
					return 100
				}
				return 25
			})...)
			s.shape("led", root, lk, glow(x, y-10, 16, c, 0.75), G(ell(x, y-10, 9, 9), fill(c.l(0.4))))
		}
		dk := pivot(-66, y+22)
		dk.P = apLin(sampled(18, ph, func(u float64) []float64 { return []float64{-66 + 148*u, y + 22, 0} })...)
		dk.O = avLin(sampled(18, ph, func(u float64) []float64 { return []float64{100 * math.Sin(math.Pi*u)} })...)
		s.shape("data", root, dk, glow(-66, y+22, 18, hx(st.data), 0.8), G(rect(-66, y+22, 18, 4, 2), fill(hx(st.data).l(0.75))))
	}
	return s
}

// ------------------------------------------------------------ headphones

// Over-ear headphones in the AirPods Max mould: a knit mesh canopy under a
// stainless steel frame, telescoping arms and rounded-rectangle aluminium
// cups, Digital Crown and noise-control button on the right cup.
type headphonesStyle struct {
	name    string
	canopy  [2]int
	cup     [3]int
	cushion int
}

var (
	headphonesMidnight  = headphonesStyle{"Headphones", [2]int{0x3A3C42, 0x16171A}, [3]int{0x6E727B, 0x363940, 0x141518}, 0x1B1C20}
	headphonesStarlight = headphonesStyle{"Starlight", [2]int{0xF0EBE2, 0xC7BFB1}, [3]int{0xFBFBFC, 0xCFD3DA, 0x8C929C}, 0xDCD5C8}
	headphonesSky       = headphonesStyle{"Sky Blue", [2]int{0xB2D3EF, 0x6E9CC6}, [3]int{0xE6F1FA, 0xA5C5E1, 0x5E83A8}, 0x93B8DA}
	headphonesPink      = headphonesStyle{"Pink", [2]int{0xFAD2D9, 0xE39AA8}, [3]int{0xFEEAEE, 0xF2BCC6, 0xC77F8D}, 0xF0B7C1}
	headphonesGreen     = headphonesStyle{"Green", [2]int{0xC8E2C0, 0x86B07B}, [3]int{0xE6F2E2, 0xB0D1A6, 0x6E9763}, 0xA3C898}
)

func headphones() *scene { return headphonesWith(headphonesMidnight) }

func luminance(c col) float64 { return 0.3*c.r + 0.59*c.g + 0.11*c.b }

func headphonesWith(st headphonesStyle) *scene {
	s := &scene{name: st.name}
	root := s.stage(204, 400, 10)
	bk := pivot(0, 20)
	bk.P = sv(pt3(0, 14, 0))
	bk.S = av(k(0, 90, 90, 100), k(14, 94, 94, 100), k(30, 90, 90, 100), k(90, 90, 90, 100), k(104, 94, 94, 100), k(120, 90, 90, 100), k(180, 90, 90, 100))
	beat := s.null("beat", root, bk)

	notes := []struct {
		x  float64
		c  col
		ph float64
	}{{-46, hx(0xFFD166), 0}, {40, hx(0x7BDFF2), 0.33}, {-4, hx(0xFFB3C6), 0.66}}
	for _, n := range notes {
		x, ph := n.x, n.ph
		nk := pivot(x, 70)
		nk.P = apLin(sampled(24, ph, func(u float64) []float64 { return []float64{x + 18*wave(u, 0.25), 70 - 150*u, 0} })...)
		nk.O = avLin(sampled(24, ph, func(u float64) []float64 { return []float64{100 * math.Pow(math.Sin(math.Pi*u), 0.8)} })...)
		nk.S = avLin(sampled(24, ph, func(u float64) []float64 { v := 60 + 50*u; return []float64{v, v, 100} })...)
		nk.R = avLin(sampled(24, ph, func(u float64) []float64 { return []float64{12 * wave(u, 0)} })...)
		s.shape("note", beat, nk,
			GRot(x, 70, -22, G(ell(x, 70, 30, 23), fill(n.c))),
			G(rect(x+12, 40, 6, 62, 3), fill(n.c)),
			G(smoothPath(false, 1, P{x + 13, 11}, P{x + 30, 24}, P{x + 29, 44}), stroke(n.c, 7)),
		)
	}

	canopyHi, canopyLo := hx(st.canopy[0]), hx(st.canopy[1])
	cushion := hx(st.cushion)
	meshInk, meshAlpha := white, 0.13
	if luminance(canopyHi) > 0.6 {
		meshInk, meshAlpha = black, 0.1
	}
	steel := func(x0, x1 float64) gfItem {
		return lin(x0, 0, x1, 0, S(0, hx(0x8E959F)), S(0.45, hx(0xF2F4F7)), S(1, hx(0x6E7580)))
	}

	// Frame and arms sit behind the cups; cushions peek out on the inner side.
	back := []any{}
	for _, sx := range []float64{-1, 1} {
		back = append(back,
			G(rect(sx*92, 132, 34, 150, 16), lin(sx*75, 0, sx*109, 0, S(0, cushion.l(0.12)), S(1, cushion.d(0.3)))),
			G(rect(sx*150, 36, 10, 64, 5), steel(sx*150-5, sx*150+5)),
			G(rect(sx*150, 64, 22, 16, 6), steel(sx*150-11, sx*150+11)),
		)
	}
	back = append(back,
		G(arcPath(0, 30, 150, 190, 180, 360, 10), glin(9, -150, 0, 150, 0, S(0, hx(0x7A818C)), S(0.5, hx(0xF4F6F9)), S(1, hx(0x7A818C)))),
		G(arcPath(0, 48, 128, 164, 194, 346, 10), glin(40, -128, 0, 128, 0, S(0, canopyLo), S(0.5, canopyHi), S(1, canopyLo))),
	)
	// Knit mesh texture: two offset rows of dots following the canopy.
	for row, off := range []float64{-8, 8} {
		for a := 198.0 + float64(row)*3; a <= 342; a += 6 {
			r := deg(a)
			back = append(back, G(ell((128+off)*math.Cos(r), 48+(164+off)*math.Sin(r), 3.4, 3.4), fillA(meshInk, meshAlpha)))
		}
	}
	back = append(back, G(arcPath(0, 48, 146, 182, 200, 340, 8), strokeA(white, 2, 0.18)))
	s.shape("band", beat, pivot(0, 0), back...)

	cupHi, cupMid, cupLo := hx(st.cup[0]), hx(st.cup[1]), hx(st.cup[2])
	for _, sx := range []float64{-1, 1} {
		x := sx * 150
		outer := x + sx*56
		items := []any{
			G(rect(x, 135, 112, 168, 40), fillA(black, 0.28)),
			G(rect(x, 130, 112, 168, 40), lin(outer, 46, x-sx*56, 214, S(0, cupHi), S(0.45, cupMid), S(1, cupLo))),
			G(rect(x-sx*50, 130, 8, 150, 4), fillA(cupLo.d(0.35), 0.6)),
			G(roundedPath([]float64{9}, P{outer - sx*30, 64}, P{outer - sx*12, 64}, P{outer - sx*12, 196}, P{outer - sx*30, 196}),
				lin(0, 64, 0, 196, SA(0, white, 0.45), SA(0.6, white, 0.12), SA(1, white, 0))),
			G(rect(x, 130, 104, 160, 36), strokeA(white, 1.5, 0.14)),
		}
		if sx > 0 {
			// Digital Crown and the noise-control button.
			items = append(items,
				G(rect(x+30, 38, 24, 18, 5), steel(x+18, x+42)),
				G(rect(x+24, 38, 2, 12, 1), fillA(black, 0.25)),
				G(rect(x+30, 38, 2, 12, 1), fillA(black, 0.25)),
				G(rect(x+36, 38, 2, 12, 1), fillA(black, 0.25)),
				G(rect(x-16, 43, 28, 8, 4), steel(x-30, x-2)),
			)
		}
		s.shape("cup", beat, pivot(0, 0), items...)
	}
	return s
}

// ------------------------------------------------------------ coin

// coinStyle is a metal: highlight, body and shadow tone; every other shade
// of the coin is derived from these three.
type coinStyle struct {
	name        string
	hi, mid, lo int
}

var (
	coinCopper   = coinStyle{"Lucky Coin", 0xFFD9C2, 0xE08A5A, 0x9A4A24}
	coinSilver   = coinStyle{"Silver", 0xEDEDEE, 0xA8ACB2, 0x62666D}
	coinGold     = coinStyle{"Gold", 0xFFF4B8, 0xFFCC3D, 0xD98F12}
	coinPlatinum = coinStyle{"Platinum", 0xF2FAFF, 0xBFD8EC, 0x6F90AE}
)

func coin() *scene { return coinWith(coinCopper) }

func coinWith(st coinStyle) *scene {
	hi, mid, lo := hx(st.hi), hx(st.mid), hx(st.lo)
	s := &scene{name: st.name}
	root := s.stage(204, 330, 16)
	s.shape("aura", root, pivot(0, 0), glow(0, 0, 210, mid, 0.35))
	rk := pivot(0, 0)
	rk.R = avLin(sampled(18, 0, func(u float64) []float64 { return []float64{8 * wave(u, 0)} })...)
	rk.S = avLin(sampled(18, 0, func(u float64) []float64 { v := 100 - 9*bump(u*2, 0); return []float64{v, 100, 100} })...)
	rock := s.null("rock", root, rk)

	items := []any{
		G(ell(14, 12, 304, 304), lin(-150, -150, 160, 160, S(0, mid.d(0.15)), S(1, lo.d(0.45)))),
		G(ell(0, 0, 304, 304), rad(-45, -53, 198, S(0, hi), S(0.45, mid), S(1, lo))),
		G(ell(0, 0, 294, 294), glin(10, -150, -150, 150, 150, S(0, hi.l(0.4)), S(0.5, mid.d(0.05)), S(1, lo.d(0.25)))),
		G(ell(0, 0, 236, 236), glin(6, -118, -118, 118, 118, S(0, lo.d(0.15)), S(1, hi.l(0.3)))),
		G(ell(0, 0, 226, 226), rad(-36, -44, 170, S(0, mid.l(0.45)), S(0.6, mid), S(1, lo.l(0.15)))),
	}
	for i := 0; i < 28; i++ {
		a := deg(float64(i) * 360 / 28)
		items = append(items, G(ell(131*math.Cos(a), 131*math.Sin(a), 7, 7), fillA(hi.l(0.3), 0.85)))
	}
	items = append(items,
		G(star(7, 9, 5, 80, 36, 0), fillA(lo.d(0.3), 0.75)),
		G(star(0, 0, 5, 80, 36, 0), lin(-60, -70, 60, 70, S(0, hi.l(0.6)), S(0.5, mid.l(0.25)), S(1, lo.l(0.1)))),
		G(star(0, 0, 5, 80, 36, 0), strokeA(hi.l(0.5), 2.5, 0.8)),
		GRot(-64, -76, -40, G(ell(-64, -76, 96, 44), rad(-64, -76, 48, SA(0, white, 0.7), SA(1, white, 0)))),
	)
	s.shape("coin", rock, pivot(0, 0), items...)
	s.sparkle(rock, -52, -64, 26, white, 0.1)
	s.sparkle(root, 160, -142, 30, hi.l(0.3), 0.35)
	s.sparkle(root, -168, -108, 22, hi.l(0.3), 0.7)
	s.sparkle(root, 172, 104, 20, hi.l(0.3), 0.9)
	return s
}

// ------------------------------------------------------------ wallet

// billStyle is a banknote's paper: light/mid/dark tone and the centre seal.
type billStyle [4]int

var (
	billGreen  = billStyle{0x9BEBA8, 0x5CC47A, 0x2E9E55, 0x3DAA63}
	billPurple = billStyle{0xDCC2FF, 0x9E72FF, 0x5E2FB8, 0x7B4DFF}
	billOrange = billStyle{0xFFD8B0, 0xFF9E57, 0xD4621F, 0xE8762E}
)

func bill(x, y, r float64, bs billStyle) grItem {
	seal := hx(bs[3])
	return GAt(x, y, r,
		G(rect(0, 3, 236, 118, 10), fillA(black, 0.2)),
		G(rect(0, 0, 236, 118, 10), lin(-118, -59, 118, 59, S(0, hx(bs[0])), S(0.5, hx(bs[1])), S(1, hx(bs[2])))),
		G(rect(0, 0, 212, 94, 6), strokeA(hx(bs[0]).l(0.7), 3, 0.7)),
		G(ell(0, 0, 64, 64), fill(seal)),
		G(ell(0, 0, 64, 64), strokeA(white, 3, 0.6)),
		G(star(0, 0, 5, 20, 9, 0), fillA(seal.l(0.85), 0.9)),
		G(ell(-86, -28, 18, 18), fillA(white, 0.35)),
		G(ell(86, 28, 18, 18), fillA(white, 0.35)),
		G(rect(-70, 30, 40, 6, 3), fillA(white, 0.3)),
		G(rect(70, -30, 40, 6, 3), fillA(white, 0.3)),
	)
}

func stitches(x0, y0, x1, y1, step float64, horizontal bool, c col) []any {
	var out []any
	if horizontal {
		for x := x0; x <= x1; x += step {
			out = append(out, G(rect(x, y0, 10, 3, 1.5), fillA(c, 0.9)))
		}
	} else {
		for y := y0; y <= y1; y += step {
			out = append(out, G(rect(x0, y, 3, 10, 1.5), fillA(c, 0.9)))
		}
	}
	return out
}

// walletStyle is the leather (back, front pocket, strap, stitching), the snap
// metal and what's tucked inside.
type walletStyle struct {
	name               string
	back, front, strap [2]int
	stitch             int
	snap               [3]int
	contents           func(s *scene, root int)
}

var (
	snapGold   = [3]int{0xFFF6D6, 0xE9B94F, 0x9C6B1A}
	snapSilver = [3]int{0xFFFFFF, 0xC9CED6, 0x6E7580}

	walletClassic = walletStyle{"Leather Wallet", [2]int{0x6E4128, 0x3E2213}, [2]int{0xC3875A, 0x7A4528}, [2]int{0x9A5C37, 0x5E321B}, 0xF2C98E, snapGold, walletCash}
	walletNoir    = walletStyle{"Noir", [2]int{0x2E3035, 0x111215}, [2]int{0x5A5E66, 0x23252A}, [2]int{0x44474E, 0x1A1B1F}, 0xC9CED6, snapSilver, walletCards}
	walletCrimson = walletStyle{"Crimson", [2]int{0x6E1A22, 0x3A0A0F}, [2]int{0xD45252, 0x7A1C22}, [2]int{0xA8323A, 0x5E1217}, 0xFFD6A8, snapGold, walletCoins}
	walletNavy    = walletStyle{"Navy", [2]int{0x1C3566, 0x0C1A36}, [2]int{0x4174CC, 0x1D3A75}, [2]int{0x2E5AA8, 0x152B55}, 0xFFE0A8, snapGold, walletForeign}
)

// bobbing adds one contents layer that floats gently in and out of the wallet.
func bobbing(s *scene, root int, name string, x, y, amp, sway, phase float64, g grItem) {
	k := pivot(x, y)
	k.P = apLin(sampled(18, phase, func(u float64) []float64 { return []float64{x, y - amp*bump(u, 0), 0} })...)
	if sway != 0 {
		k.R = avLin(sampled(18, phase, func(u float64) []float64 { return []float64{sway * wave(u, 0)} })...)
	}
	s.shape(name, root, k, g)
}

func card(x, y, r float64, face [2]int, chipDark bool) grItem {
	chip := [2]int{0xFFF0B0, 0xD9A233}
	if chipDark {
		chip = [2]int{0xF4F6F9, 0x8E959F}
	}
	return GAt(x, y, r,
		G(rect(0, 3, 184, 116, 14), fillA(black, 0.2)),
		G(rect(0, 0, 184, 116, 14), lin(-92, -58, 92, 58, S(0, hx(face[0])), S(1, hx(face[1])))),
		G(rect(-52, -16, 34, 26, 6), lin(-69, -29, -35, -3, S(0, hx(chip[0])), S(1, hx(chip[1])))),
		G(rect(10, 26, 120, 8, 4), fillA(white, 0.55)),
		G(ell(62, -32, 26, 26), fillA(white, 0.3)),
		G(ell(48, -32, 26, 26), fillA(white, 0.3)),
	)
}

func walletCash(s *scene, root int) {
	bobbing(s, root, "bill", -42, -96, 10, 2.5, 0, bill(-42, -96, -8, billGreen))
	bobbing(s, root, "bill", 34, -112, 10, 2.5, 0.4, bill(34, -112, 6, billGreen))
	bobbing(s, root, "card", 96, -82, 7, 0, 0.7, card(96, -82, 12, [2]int{0x6E9BFF, 0x7B3DFF}, false))
}

func walletCards(s *scene, root int) {
	bobbing(s, root, "card", -70, -92, 8, 2, 0, card(-70, -92, -12, [2]int{0x44474E, 0x0E0F12}, true))
	bobbing(s, root, "card", 4, -110, 9, 2, 0.33, card(4, -110, -2, [2]int{0xFFE9A8, 0xC9962E}, false))
	bobbing(s, root, "card", 80, -94, 8, 2, 0.66, card(80, -94, 12, [2]int{0xF0F4F7, 0x98A6B2}, true))
}

func walletCoins(s *scene, root int) {
	bobbing(s, root, "bill", -46, -94, 8, 2, 0, bill(-46, -94, -8, billGreen))
	var stack []any
	for i := 0; i < 4; i++ {
		stack = append(stack, coinDisc(70, -96-15*float64(i))...)
	}
	stack = append(stack, G(star(70, -141, 5, 9, 4, 0), fillA(hx(0xFFF6D6), 0.9)))
	bobbing(s, root, "coins", 70, -96, 8, 0, 0.5, G(stack...))
	s.sparkle(root, 112, -170, 20, hx(0xFFF1B0), 0.3)
}

func walletForeign(s *scene, root int) {
	bobbing(s, root, "bill", -42, -96, 10, 2.5, 0, bill(-42, -96, -8, billPurple))
	bobbing(s, root, "bill", 36, -110, 10, 2.5, 0.45, bill(36, -110, 7, billOrange))
}

func wallet() *scene { return walletWith(walletClassic) }

func walletWith(st walletStyle) *scene {
	s := &scene{name: st.name}
	root := s.stage(196, 420, 8)
	stitch := hx(st.stitch)

	s.shape("back", root, pivot(0, 0),
		G(rect(0, 42, 356, 240, 34), vgrad(0, -78, 162, hx(st.back[0]), hx(st.back[1]))),
		G(rect(0, -70, 320, 14, 7), fillA(black, 0.35)),
	)
	st.contents(s, root)

	front := []any{
		G(rect(0, 74, 356, 180, 32), fillA(black, 0.3)),
		G(rect(0, 70, 356, 180, 32), vgrad(0, -20, 160, hx(st.front[0]), hx(st.front[1]))),
		G(rect(0, -17, 324, 3, 1.5), fillA(white, 0.22)),
		G(ell(-86, 22, 220, 100), rad(-86, 22, 110, SA(0, white, 0.2), SA(1, white, 0))),
	}
	front = append(front, stitches(-150, -4, 150, 0, 20, true, stitch)...)
	front = append(front, stitches(-150, 146, 150, 0, 20, true, stitch)...)
	front = append(front, stitches(-164, 12, 0, 130, 20, false, stitch)...)
	front = append(front,
		G(roundedPath([]float64{0, 26, 26, 0}, P{66, 18}, P{186, 18}, P{186, 102}, P{66, 102}), fillA(black, 0.25)),
		G(roundedPath([]float64{0, 26, 26, 0}, P{66, 14}, P{186, 14}, P{186, 98}, P{66, 98}), vgrad(0, 14, 98, hx(st.strap[0]), hx(st.strap[1]))),
		G(roundedPath([]float64{0, 20, 20, 0}, P{66, 24}, P{176, 24}, P{176, 88}, P{66, 88}), strokeA(stitch, 2.5, 0.6)),
		G(ell(104, 56, 38, 38), fillA(black, 0.3)),
		G(ell(102, 54, 38, 38), rad(96, 48, 24, S(0, hx(st.snap[0])), S(0.55, hx(st.snap[1])), S(1, hx(st.snap[2])))),
		G(ell(102, 54, 24, 24), strokeA(hx(st.snap[2]).d(0.1), 2, 0.5)),
		G(ell(96, 47, 9, 6), fillA(white, 0.8)),
	)
	s.shape("front", root, pivot(0, 0), front...)
	s.sparkle(root, 112, 42, 18, white, 0.2)
	s.sparkle(root, -120, -150, 22, hx(0xFFF6D6), 0.6)
	return s
}

// ------------------------------------------------------------ safe

func cashBrick(x, y float64) []any {
	return []any{
		G(rect(x, y, 132, 28, 5), vgrad(x, y-14, y+14, hx(0x7EDB8E), hx(0x2F9E55))),
		G(rect(x, y-11, 124, 3, 1.5), fillA(white, 0.35)),
		G(rect(x-30, y, 22, 28, 2), vgrad(x, y-14, y+14, hx(0xFFF6DD), hx(0xD9C79E))),
		G(rect(x+30, y, 22, 28, 2), vgrad(x, y-14, y+14, hx(0xFFF6DD), hx(0xD9C79E))),
	}
}

func coinDisc(x, y float64) []any {
	return []any{
		G(ell(x, y+6, 76, 26), fill(hx(0x9C650A))),
		G(rect(x, y+3, 76, 6, 0), fill(hx(0x9C650A))),
		G(ell(x, y, 76, 26), goldRad(x, y, 38)),
		G(ell(x, y, 56, 18), strokeA(hx(0xFFF3C0), 2, 0.7)),
	}
}

type safeStyle struct {
	name  string
	body  [3]int
	door  [2]int
	bevel [2]int
}

var (
	safeSteel    = safeStyle{"Money Safe", [3]int{0x7A8596, 0x4A5362, 0x232830}, [2]int{0x8894A5, 0x3A424E}, [2]int{0x2E343E, 0xB4BECB}}
	safeGold     = safeStyle{"Gold", [3]int{0xF5D27A, 0xC9962E, 0x6E4A10}, [2]int{0xFFE39A, 0xA87720}, [2]int{0x6E4A10, 0xFFF0B8}}
	safeObsidian = safeStyle{"Obsidian", [3]int{0x4A4F5C, 0x23262E, 0x0B0C10}, [2]int{0x3E4350, 0x121418}, [2]int{0x08090C, 0x6A7080}}
)

func safe() *scene { return safeWith(safeSteel) }

func safeWith(st safeStyle) *scene {
	s := &scene{name: st.name}
	root := s.stage(220, 420, 6)

	body := []any{
		G(rect(-118, 212, 66, 18, 6), fill(hx(0x1E2228))),
		G(rect(118, 212, 66, 18, 6), fill(hx(0x1E2228))),
		G(rect(0, 44, 348, 338, 36), lin(-174, -125, 174, 213, S(0, hx(st.body[0])), S(0.5, hx(st.body[1])), S(1, hx(st.body[2])))),
		G(rect(0, -121, 300, 4, 2), fillA(white, 0.28)),
		G(rect(-4, 48, 294, 284, 26), fill(hx(0x1A1D23))),
		G(rect(-4, 44, 284, 274, 22), lin(-146, -93, 138, 181, S(0, hx(st.door[0])), S(1, hx(st.door[1])))),
		G(rect(-4, 44, 256, 246, 14), glin(5, -132, -79, 124, 167, S(0, hx(st.bevel[0])), S(1, hx(st.bevel[1])))),
		G(rect(-152, -34, 16, 58, 6), silver(-160, 0, -144, 0)),
		G(rect(-152, 124, 16, 58, 6), silver(-160, 0, -144, 0)),
	}
	for _, c := range []P{{-118, -68}, {110, -68}, {-118, 156}, {110, 156}} {
		body = append(body, G(ell(c[0], c[1], 13, 13), rad(c[0]-2, c[1]-2, 8, S(0, hx(0xF4F6F9)), S(1, hx(0x6B7482)))))
	}
	dx, dy := -40.0, 22.0
	body = append(body,
		G(ell(dx, dy+4, 156, 156), fillA(black, 0.35)),
		G(ell(dx, dy, 156, 156), rad(dx-20, dy-24, 100, S(0, hx(0xEEF2F6)), S(1, hx(0x7B8594)))),
		G(ell(dx, dy, 156, 156), strokeA(hx(0x2B313A), 3, 0.5)),
		G(polyPath(true, P{dx - 7, dy - 86}, P{dx + 7, dy - 86}, P{dx, dy - 74}), fill(hx(0xFFC542))),
	)
	s.shape("safe", root, pivot(0, 0), body...)

	dial := []any{
		G(ell(dx, dy, 126, 126), lin(dx-63, dy-63, dx+63, dy+63, S(0, hx(0xDCE2E9)), S(1, hx(0x9AA4B2)))),
	}
	for i := 0; i < 24; i++ {
		long := i%3 == 0
		w, h := 3.0, 8.0
		if long {
			w, h = 4, 14
		}
		dial = append(dial, GRot(dx, dy, float64(i)*15, G(rect(dx, dy-52+h/2, w, h, 1.5), fill(hx(0x2B313A)))))
	}
	dial = append(dial,
		G(ell(dx, dy, 58, 58), rad(dx-8, dy-8, 36, S(0, hx(0x566072)), S(1, hx(0x15181D)))),
		G(rect(dx, dy-18, 6, 18, 3), fill(hx(0xFFC542))),
		G(ell(dx, dy, 14, 14), goldRad(dx, dy, 7)),
	)
	dk := pivot(dx, dy)
	dk.R = av(k(0, 0), k(34, 135), k(56, 135), k(92, 15), k(110, 15), k(150, 360), k(180, 360))
	s.shape("dial", root, dk, dial...)

	hx0, hy := 84.0, 110.0
	var wheel []any
	for _, a := range []float64{0, 120, 240} {
		wheel = append(wheel, GRot(hx0, hy, a,
			G(rect(hx0, hy-24, 10, 48, 5), silver(hx0-5, 0, hx0+5, 0)),
			G(ell(hx0, hy-52, 20, 20), rad(hx0-3, hy-55, 12, S(0, hx(0xFFFFFF)), S(1, hx(0x7B8594)))),
		))
	}
	wheel = append(wheel, G(ell(hx0, hy, 36, 36), goldRad(hx0, hy, 18)), G(ell(hx0, hy, 36, 36), strokeA(hx(0x8A5A12), 2, 0.5)))
	wk := pivot(hx0, hy)
	wk.R = av(k(0, 0), k(150, 0), k(164, -60), k(172, -60), k(180, 0))
	s.shape("handle", root, wk, wheel...)

	var money []any
	for i := 0; i < 3; i++ {
		money = append(money, cashBrick(-80, -139-26*float64(i))...)
	}
	money = append(money, GAt(-80, -219, -5, cashBrick(0, 0)...))
	for i := 0; i < 4; i++ {
		money = append(money, coinDisc(78, -136-15*float64(i))...)
	}
	money = append(money, G(star(78, -181, 5, 9, 4, 0), fillA(hx(0xFFF6D6), 0.9)))
	s.shape("money", root, pivot(0, 0), money...)

	ck := pivot(128, -138)
	ck.P = ap(k(0, 128, -138, 0), k(60, 128, -138, 0), k(78, 128, -178, 0), k(96, 128, -138, 0), k(104, 128, -150, 0), k(112, 128, -138, 0), k(180, 128, -138, 0))
	ck.R = av(k(0, 0), k(60, 0), k(96, 180), k(180, 180))
	s.shape("hopcoin", root, ck, coinDisc(128, -138)...)

	s.sparkle(root, 96, -206, 26, hx(0xFFF1B0), 0.15)
	s.sparkle(root, -118, -206, 20, hx(0xE4FFEB), 0.5)
	s.sparkle(root, 170, -120, 18, hx(0xFFF1B0), 0.8)
	return s
}
