package giftpacks

import "math"

func init() {
	register(&packDef{
		id:          "grind-kit",
		name:        "Grind Kit",
		author:      "OwpenGram",
		description: "From the first coffee to a safe full of cash: the tech and money of the daily grind.",
		icon:        "safe",
		gifts: []giftDef{
			{"coffee", "Morning Coffee", 15, coffee},
			{"phone", "Smartphone", 25, phone},
			{"laptop", "Laptop", 35, laptop},
			{"desktop", "Desktop PC", 40, desktop},
			{"server", "Data Server", 55, server},
			{"headphones", "Headphones", 20, headphones},
			{"coin", "Gold Coin", 15, coin},
			{"wallet", "Leather Wallet", 25, wallet},
			{"safe", "Money Safe", 70, safe},
		},
	})
}

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

	for i, x0 := range []float64{-50, 4, 56} {
		x, ph := x0, float64(i)/3
		kk := pivot(x, -96)
		kk.P = apLin(sampled(24, ph, func(u float64) []float64 {
			return []float64{x + 9*wave(u*1.5, 0), -96 - 70*u, 0}
		})...)
		kk.O = avLin(sampled(24, ph, func(u float64) []float64 { return []float64{90 * math.Pow(math.Sin(math.Pi*u), 1.3)} })...)
		kk.S = avLin(sampled(24, ph, func(u float64) []float64 { v := 75 + 40*u; return []float64{v, v, 100} })...)
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

func phone() *scene {
	s := &scene{name: "Smartphone"}
	root := s.stage(214, 320, 12)
	tk := pivot(0, 40)
	tk.R = avLin(sampled(18, 0, func(u float64) []float64 { return []float64{-9 + 3*wave(u, 0)} })...)
	tilt := s.null("tilt", root, tk)

	items := []any{
		G(rect(-119, -92, 8, 46, 3), fill(hx(0x4A4F5B))),
		G(rect(-119, -36, 8, 46, 3), fill(hx(0x4A4F5B))),
		G(rect(119, -64, 8, 74, 3), fill(hx(0x4A4F5B))),
		G(rect(0, 0, 236, 416, 52), lin(-118, -208, 118, 208, S(0, hx(0xA3AAB6)), S(0.3, hx(0x40465A)), S(0.65, hx(0x6A7284)), S(1, hx(0x23262E)))),
		G(rect(0, 0, 222, 402, 45), fill(hx(0x0A0B0E))),
		G(rect(0, 0, 204, 384, 36), lin(0, -192, 0, 192, S(0, hx(0x7A5CFF)), S(0.55, hx(0x3B6CF6)), S(1, hx(0x172466)))),
		glow(38, 112, 62, hx(0xFF6FB5), 0.8),
		glow(-40, -70, 60, hx(0x3DE0FF), 0.6),
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

func laptop() *scene {
	s := &scene{name: "Laptop"}
	root := s.stage(178, 480, 8)

	gk := pivot(0, -52)
	gk.O = avLin(sampled(12, 0, func(u float64) []float64 { return []float64{65 + 35*wave(u, 0)} })...)
	s.shape("glow", root, gk, glow(0, -52, 250, hx(0x5AA9FF), 0.4))

	items := []any{
		G(rect(0, -52, 394, 262, 26), silver(-197, -183, 197, 79)),
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
		G(roundedPath([]float64{6, 6, 16, 16}, P{-204, 84}, P{204, 84}, P{244, 136}, P{-244, 136}), vgrad(0, 84, 136, hx(0xF2F4F7), hx(0x9EA5B0))),
		G(roundedPath([]float64{4}, P{-170, 92}, P{170, 92}, P{186, 118}, P{-186, 118}), fillA(hx(0x8C939F), 0.35)),
	)
	for r := 0; r < 2; r++ {
		y := 99 + 11*float64(r)
		n := 15 + r
		span := 330 + 18*float64(r)
		for c := 0; c < n; c++ {
			x := -span/2 + span*(float64(c)+0.5)/float64(n)
			items = append(items, G(rect(x, y, span/float64(n)-4, 7, 2), fill(hx(0x6E7580))))
		}
	}
	items = append(items,
		G(roundedPath([]float64{3}, P{-54, 121}, P{54, 121}, P{57, 132}, P{-57, 132}), fill(hx(0xB9BFC8))),
		G(rect(0, 139, 488, 8, 4), fill(hx(0x7E8591))),
		G(rect(0, 88, 64, 5, 2.5), fill(hx(0x8C939F))),
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

func fan(s *scene, parent int, x, y float64, phase float64) {
	gk := pivot(x, y)
	gk.O = avLin(sampled(18, phase, func(u float64) []float64 { return []float64{60 + 40*bump(u, 0)} })...)
	s.shape("fanglow", parent, gk, glow(x, y, 58, hx(0x9B6BFF), 0.55))
	s.shape("fan", parent, pivot(0, 0),
		G(ell(x, y, 82, 82), fill(hx(0x0C0D11))),
		G(ell(x, y, 82, 82), glin(5, x-41, y-41, x+41, y+41, S(0, hx(0xFF4FD8)), S(0.5, hx(0x9B6BFF)), S(1, hx(0x3DE0FF)))),
	)
	var blades []any
	for i := 0; i < 7; i++ {
		a := float64(i) * 360 / 7
		blades = append(blades, GRot(x, y, a, G(ell(x+6, y-19, 17, 34), lin(x, y-36, x, y, SA(0, hx(0xC9B3FF), 0.9), SA(1, hx(0x6A4BD6), 0.9)))))
	}
	bk := pivot(x, y)
	bk.R = avLin(k(0, 0), k(op, 720))
	s.shape("blades", parent, bk, append(blades, G(ell(x, y, 22, 22), rad(x-3, y-3, 12, S(0, hx(0x5A5F6B)), S(1, hx(0x1A1C22)))))...)
}

func desktop() *scene {
	s := &scene{name: "Desktop PC"}
	root := s.stage(200, 470, 8)

	tx, ty := 152.0, 28.0
	s.shape("tower", root, pivot(0, 0),
		G(rect(tx, ty, 122, 316, 17), lin(tx-61, 0, tx+61, 0, S(0, hx(0x505869)), S(1, hx(0x1C1F26)))),
		G(rect(tx, ty, 106, 300, 12), fill(hx(0x121419))),
		G(rect(tx, ty, 106, 300, 12), lin(tx-53, ty-150, tx+53, ty+150, SA(0, hx(0x9B6BFF), 0.2), SA(1, hx(0x3DE0FF), 0.12))),
		G(rect(tx, -102, 72, 7, 3.5), fill(hx(0x2C3038))),
		G(rect(tx, -89, 72, 7, 3.5), fill(hx(0x2C3038))),
		G(rect(tx-50, ty, 3, 280, 1.5), lin(0, ty-140, 0, ty+140, S(0, hx(0xFF4FD8)), S(1, hx(0x3DE0FF)))),
		G(rect(tx+50, ty, 3, 280, 1.5), lin(0, ty-140, 0, ty+140, S(0, hx(0x3DE0FF)), S(1, hx(0xFF4FD8)))),
	)
	fan(s, root, tx, -28, 0)
	fan(s, root, tx, 70, 0.5)
	pk := pivot(tx, 152)
	pk.O = avLin(sampled(12, 0, func(u float64) []float64 { return []float64{55 + 45*bump(u, 0)} })...)
	s.shape("power", root, pk,
		glow(tx, 152, 22, hx(0x3DFFB0), 0.8),
		G(ell(tx, 152, 16, 16), rad(tx-2, 150, 9, S(0, hx(0xD4FFF0)), S(1, hx(0x21C08B)))),
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
				lin(x, base-h, x, base, S(0, hx(0x6CF6FF)), S(1, hx(0x2B5CF6)))),
			G(rect(x, base-h+4, 22, 3, 1.5), fillA(white, 0.55)),
		)
	}
	s.shape("glare", root, pivot(0, 0), G(polyPath(true, P{mx + 40, my - 107}, P{mx + 120, my - 107}, P{mx - 30, my + 93}, P{mx - 110, my + 93}), fillA(white, 0.05)))
	return s
}

// ------------------------------------------------------------ server

func server() *scene {
	s := &scene{name: "Data Server"}
	root := s.stage(220, 380, 8)
	ys := []float64{-141, -47, 47, 141}
	items := []any{
		G(rect(-108, 208, 52, 16, 5), fill(hx(0x22262D))),
		G(rect(108, 208, 52, 16, 5), fill(hx(0x22262D))),
		G(rect(0, 0, 308, 414, 28), lin(-154, -207, 154, 207, S(0, hx(0x5A6376)), S(0.5, hx(0x363D4B)), S(1, hx(0x1A1D25)))),
		G(rect(0, -204, 270, 4, 2), fillA(white, 0.25)),
		G(rect(0, 0, 284, 390, 20), fill(hx(0x0E1015))),
	}
	for _, y := range ys {
		items = append(items,
			G(rect(0, y+3, 266, 84, 13), fillA(black, 0.5)),
			G(rect(0, y, 266, 84, 13), vgrad(0, y-42, y+42, hx(0x5A6375), hx(0x2B303B))),
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
		for j, c := range []col{hx(0x4CFF8A), hx(0x49B6FF)} {
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
		s.shape("data", root, dk, glow(-66, y+22, 18, hx(0x3DE0FF), 0.8), G(rect(-66, y+22, 18, 4, 2), fill(hx(0xCFFAFF))))
	}
	return s
}

// ------------------------------------------------------------ headphones

func headphones() *scene {
	s := &scene{name: "Headphones"}
	root := s.stage(196, 400, 10)
	bk := pivot(0, 60)
	bk.P = sv(pt3(0, 34, 0))
	bk.S = av(k(0, 91, 91, 100), k(14, 96, 96, 100), k(30, 91, 91, 100), k(90, 91, 91, 100), k(104, 96, 96, 100), k(120, 91, 91, 100), k(180, 91, 91, 100))
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

	band := arcPath(0, 16, 152, 196, 166, 374, 8)
	s.shape("band", beat, pivot(0, 0),
		G(arcPath(0, 30, 134, 180, 200, 340, 6), stroke(hx(0x15171C), 18)),
		G(band, glin(32, -152, 0, 152, 0, S(0, hx(0x23262E)), S(0.5, hx(0x5A6072)), S(1, hx(0x23262E)))),
		G(arcPath(0, 10, 150, 190, 205, 335, 6), strokeA(white, 4, 0.22)),
	)
	for _, sx := range []float64{-1, 1} {
		x := sx * 150
		s.shape("cup", beat, pivot(0, 0),
			G(rect(sx*146, 64, 22, 52, 7), silver(sx*146-11, 0, sx*146+11, 0)),
			G(rect(sx*96, 132, 50, 142, 24), lin(sx*71, 0, sx*121, 0, S(0, hx(0x40434E)), S(1, hx(0x17181D)))),
			G(rect(x, 134, 108, 160, 50), fillA(black, 0.3)),
			G(rect(x, 130, 108, 160, 50), lin(x-54, 50, x+54, 210, S(0, hx(0xFF8AA3)), S(0.5, hx(0xF0456A)), S(1, hx(0xA61E42)))),
			G(rect(x, 130, 96, 148, 44), strokeA(white, 2, 0.12)),
			G(ell(x, 132, 62, 62), rad(x, 132, 31, S(0, hx(0xFF6C8C)), S(1, hx(0xC72C51)))),
			G(ell(x, 132, 62, 62), strokeA(white, 3, 0.55)),
			G(ell(x, 132, 16, 16), fillA(white, 0.9)),
			G(ell(x-22, 80, 30, 64), rad(x-22, 80, 32, SA(0, white, 0.55), SA(1, white, 0))),
		)
	}
	return s
}

// ------------------------------------------------------------ coin

func coin() *scene {
	s := &scene{name: "Gold Coin"}
	root := s.stage(204, 330, 16)
	s.shape("aura", root, pivot(0, 0), glow(0, 0, 210, hx(0xFFC83D), 0.35))
	rk := pivot(0, 0)
	rk.R = avLin(sampled(18, 0, func(u float64) []float64 { return []float64{8 * wave(u, 0)} })...)
	rk.S = avLin(sampled(18, 0, func(u float64) []float64 { v := 100 - 9*bump(u*2, 0); return []float64{v, 100, 100} })...)
	rock := s.null("rock", root, rk)

	items := []any{
		G(ell(14, 12, 304, 304), lin(-150, -150, 160, 160, S(0, hx(0xD9961A)), S(1, hx(0x7A4E05)))),
		G(ell(0, 0, 304, 304), goldRad(0, 0, 152)),
		G(ell(0, 0, 294, 294), glin(10, -150, -150, 150, 150, S(0, hx(0xFFF6CC)), S(0.5, hx(0xF2B535)), S(1, hx(0xB06F0A)))),
		G(ell(0, 0, 236, 236), glin(6, -118, -118, 118, 118, S(0, hx(0xB87A0E)), S(1, hx(0xFFF0B0)))),
		G(ell(0, 0, 226, 226), rad(-36, -44, 170, S(0, hx(0xFFE68A)), S(0.6, hx(0xFFC23A)), S(1, hx(0xE9A21C)))),
	}
	for i := 0; i < 28; i++ {
		a := deg(float64(i) * 360 / 28)
		items = append(items, G(ell(131*math.Cos(a), 131*math.Sin(a), 7, 7), fillA(hx(0xFFF3C0), 0.85)))
	}
	items = append(items,
		G(star(7, 9, 5, 80, 36, 0), fillA(hx(0xA8690A), 0.75)),
		G(star(0, 0, 5, 80, 36, 0), lin(-60, -70, 60, 70, S(0, hx(0xFFFBE6)), S(0.5, hx(0xFFD65C)), S(1, hx(0xF0A51E)))),
		G(star(0, 0, 5, 80, 36, 0), strokeA(hx(0xFFF6D6), 2.5, 0.8)),
		GRot(-64, -76, -40, G(ell(-64, -76, 96, 44), rad(-64, -76, 48, SA(0, white, 0.7), SA(1, white, 0)))),
	)
	s.shape("coin", rock, pivot(0, 0), items...)
	s.sparkle(rock, -52, -64, 26, white, 0.1)
	s.sparkle(root, 160, -142, 30, hx(0xFFF1B0), 0.35)
	s.sparkle(root, -168, -108, 22, hx(0xFFF1B0), 0.7)
	s.sparkle(root, 172, 104, 20, hx(0xFFF1B0), 0.9)
	return s
}

// ------------------------------------------------------------ wallet

func bill(x, y, r float64) grItem {
	return GAt(x, y, r,
		G(rect(0, 3, 236, 118, 10), fillA(black, 0.2)),
		G(rect(0, 0, 236, 118, 10), lin(-118, -59, 118, 59, S(0, hx(0x9BEBA8)), S(0.5, hx(0x5CC47A)), S(1, hx(0x2E9E55)))),
		G(rect(0, 0, 212, 94, 6), strokeA(hx(0xEAFFEF), 3, 0.7)),
		G(ell(0, 0, 64, 64), fill(hx(0x3DAA63))),
		G(ell(0, 0, 64, 64), strokeA(white, 3, 0.6)),
		G(star(0, 0, 5, 20, 9, 0), fillA(hx(0xE4FFEB), 0.9)),
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

func wallet() *scene {
	s := &scene{name: "Leather Wallet"}
	root := s.stage(196, 420, 8)

	s.shape("back", root, pivot(0, 0),
		G(rect(0, 42, 356, 240, 34), vgrad(0, -78, 162, hx(0x6E4128), hx(0x3E2213))),
		G(rect(0, -70, 320, 14, 7), fillA(black, 0.35)),
	)
	for i, b := range []struct{ x, y, r, ph float64 }{{-42, -96, -8, 0}, {34, -112, 6, 0.4}} {
		x, y, ph := b.x, b.y, b.ph
		bk := pivot(x, y)
		bk.P = apLin(sampled(18, ph, func(u float64) []float64 { return []float64{x, y - 10*bump(u, 0), 0} })...)
		bk.R = avLin(sampled(18, ph, func(u float64) []float64 { return []float64{2.5 * wave(u, 0)} })...)
		_ = i
		s.shape("bill", root, bk, bill(x, y, b.r))
	}
	ck := pivot(96, -82)
	ck.P = apLin(sampled(18, 0.7, func(u float64) []float64 { return []float64{96, -82 - 7*bump(u, 0), 0} })...)
	s.shape("card", root, ck, GAt(96, -82, 12,
		G(rect(0, 3, 184, 116, 14), fillA(black, 0.2)),
		G(rect(0, 0, 184, 116, 14), lin(-92, -58, 92, 58, S(0, hx(0x6E9BFF)), S(1, hx(0x7B3DFF)))),
		G(rect(-52, -16, 34, 26, 6), lin(-69, -29, -35, -3, S(0, hx(0xFFF0B0)), S(1, hx(0xD9A233)))),
		G(rect(10, 26, 120, 8, 4), fillA(white, 0.55)),
		G(ell(62, -32, 26, 26), fillA(white, 0.3)),
		G(ell(48, -32, 26, 26), fillA(white, 0.3)),
	))

	front := []any{
		G(rect(0, 74, 356, 180, 32), fillA(black, 0.3)),
		G(rect(0, 70, 356, 180, 32), vgrad(0, -20, 160, hx(0xC3875A), hx(0x7A4528))),
		G(rect(0, -17, 324, 3, 1.5), fillA(white, 0.22)),
		G(ell(-86, 22, 220, 100), rad(-86, 22, 110, SA(0, white, 0.2), SA(1, white, 0))),
	}
	front = append(front, stitches(-150, -4, 150, 0, 20, true, hx(0xF2C98E))...)
	front = append(front, stitches(-150, 146, 150, 0, 20, true, hx(0xF2C98E))...)
	front = append(front, stitches(-164, 12, 0, 130, 20, false, hx(0xF2C98E))...)
	front = append(front,
		G(roundedPath([]float64{0, 26, 26, 0}, P{66, 18}, P{186, 18}, P{186, 102}, P{66, 102}), fillA(black, 0.25)),
		G(roundedPath([]float64{0, 26, 26, 0}, P{66, 14}, P{186, 14}, P{186, 98}, P{66, 98}), vgrad(0, 14, 98, hx(0x9A5C37), hx(0x5E321B))),
		G(roundedPath([]float64{0, 20, 20, 0}, P{66, 24}, P{176, 24}, P{176, 88}, P{66, 88}), strokeA(hx(0xF2C98E), 2.5, 0.6)),
		G(ell(104, 56, 38, 38), fillA(black, 0.3)),
		G(ell(102, 54, 38, 38), rad(96, 48, 24, S(0, hx(0xFFF6D6)), S(0.55, hx(0xE9B94F)), S(1, hx(0x9C6B1A)))),
		G(ell(102, 54, 24, 24), strokeA(hx(0x8A5A12), 2, 0.5)),
		G(ell(96, 47, 9, 6), fillA(white, 0.8)),
	)
	s.shape("front", root, pivot(0, 0), front...)
	s.sparkle(root, 112, 42, 18, white, 0.2)
	s.sparkle(root, -120, -150, 22, hx(0xD9FFE2), 0.6)
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

func safe() *scene {
	s := &scene{name: "Money Safe"}
	root := s.stage(220, 420, 6)

	gk := pivot(0, -150)
	gk.O = avLin(sampled(12, 0, func(u float64) []float64 { return []float64{60 + 40*bump(u, 0)} })...)
	s.shape("goldglow", root, gk, glow(0, -150, 170, hx(0xFFD24A), 0.45))

	body := []any{
		G(rect(-118, 212, 66, 18, 6), fill(hx(0x1E2228))),
		G(rect(118, 212, 66, 18, 6), fill(hx(0x1E2228))),
		G(rect(0, 44, 348, 338, 36), lin(-174, -125, 174, 213, S(0, hx(0x7A8596)), S(0.5, hx(0x4A5362)), S(1, hx(0x232830)))),
		G(rect(0, -121, 300, 4, 2), fillA(white, 0.28)),
		G(rect(-4, 48, 294, 284, 26), fill(hx(0x1A1D23))),
		G(rect(-4, 44, 284, 274, 22), lin(-146, -93, 138, 181, S(0, hx(0x8894A5)), S(1, hx(0x3A424E)))),
		G(rect(-4, 44, 256, 246, 14), glin(5, -132, -79, 124, 167, S(0, hx(0x2E343E)), S(1, hx(0xB4BECB)))),
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
