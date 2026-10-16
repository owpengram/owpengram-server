package lottieraster

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"golang.org/x/image/vector"
)

// renderFrame draws frame t, scaled to fit a size x size square.
func renderFrame(a *animation, t float64, size int) *image.NRGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	scale := float64(size) / math.Max(a.W, a.H)
	root := affine{a: scale, d: scale, e: (float64(size) - a.W*scale) / 2, f: (float64(size) - a.H*scale) / 2}
	r := &renderer{dst: dst, raster: vector.NewRasterizer(size, size)}

	byInd := make(map[int]*layer, len(a.Layers))
	for i := range a.Layers {
		if l := &a.Layers[i]; l.Ind != nil {
			byInd[*l.Ind] = l
		}
	}
	for i := len(a.Layers) - 1; i >= 0; i-- {
		l := &a.Layers[i]
		if l.Ty != 4 || l.Hidden || t < l.IP || t >= l.OP {
			continue
		}
		lt := layerTime(l, t)
		opacity := l.KS.O.number(lt, 100) / 100
		if opacity <= 0 {
			continue
		}
		r.group(l.Shapes, root.mul(worldMatrix(l, byInd, t, 0)), opacity, lt)
	}

	out := image.NewNRGBA(dst.Bounds())
	draw.Draw(out, out.Bounds(), dst, image.Point{}, draw.Src)
	return out
}

func layerTime(l *layer, t float64) float64 {
	sr := l.SR
	if sr == 0 {
		sr = 1
	}
	return (t - l.ST) / sr
}

// worldMatrix is a layer's own transform under its parent chain's.
func worldMatrix(l *layer, byInd map[int]*layer, t float64, depth int) affine {
	lt := layerTime(l, t)
	rot := &l.KS.R
	if !rot.set {
		rot = &l.KS.RZ
	}
	m := matrix(lt, &l.KS.A, &l.KS.P, &l.KS.S, rot, &l.KS.SK, &l.KS.SA)
	if l.Parent != nil && depth < 32 {
		if p, ok := byInd[*l.Parent]; ok && p != l {
			return worldMatrix(p, byInd, t, depth+1).mul(m)
		}
	}
	return m
}

type renderer struct {
	dst    *image.RGBA
	raster *vector.Rasterizer
}

// groupTransform applies a group's "tr" item, if it has one.
func groupTransform(items []shape, m affine, opacity, t float64) (affine, float64) {
	for i := range items {
		if tr := &items[i]; tr.Ty == "tr" {
			gm := m.mul(matrix(t, &tr.A, &tr.P, &tr.S, &tr.R, &tr.SK, &tr.SA))
			return gm, opacity * tr.O.number(t, 100) / 100
		}
	}
	return m, opacity
}

// group draws a shape list bottom (last item) to top: a group draws its own
// contents in place, and a fill or stroke paints every path above it in the
// same list, nested groups' paths included.
func (r *renderer) group(items []shape, m affine, opacity, t float64) {
	gm, op := groupTransform(items, m, opacity, t)
	if op <= 0 {
		return
	}
	for i := len(items) - 1; i >= 0; i-- {
		it := &items[i]
		if it.Hidden {
			continue
		}
		switch it.Ty {
		case "gr":
			r.group(it.Items, gm, op, t)
		case "fl", "gf", "st", "gs":
			paths := collect(items[:i], gm, t)
			if len(paths) > 0 {
				r.paint(it, paths, gm, op, t)
			}
		}
	}
}

func collect(items []shape, m affine, t float64) []polyline {
	var out []polyline
	for i := range items {
		it := &items[i]
		if it.Hidden {
			continue
		}
		switch it.Ty {
		case "el":
			cx, cy := it.P.vec2(t, 0, 0)
			w, h := it.S.vec2(t, 0, 0)
			out = append(out, flattenBezier(ellipseBezier(cx, cy, w, h), m))
		case "rc":
			cx, cy := it.P.vec2(t, 0, 0)
			w, h := it.S.vec2(t, 0, 0)
			out = append(out, flattenBezier(rectBezier(cx, cy, w, h, it.R.number(t, 0)), m))
		case "sh":
			if b, ok := it.KS.path(t); ok {
				out = append(out, flattenBezier(b, m))
			}
		case "sr":
			out = append(out, flattenBezier(starBezier(it, t), m))
		case "gr":
			gm, _ := groupTransform(it.Items, m, 1, t)
			out = append(out, collect(it.Items, gm, t)...)
		}
	}
	return out
}

func (r *renderer) paint(it *shape, paths []polyline, m affine, opacity, t float64) {
	opacity *= it.O.number(t, 100) / 100
	if opacity <= 0 {
		return
	}
	size := r.dst.Bounds().Size()
	r.raster.Reset(size.X, size.Y)
	switch it.Ty {
	case "fl", "gf":
		for _, pl := range paths {
			r.addPolygon(pl.pts)
		}
	case "st", "gs":
		width := it.W.number(t, 0) * m.scale()
		if width <= 0 {
			return
		}
		for _, pl := range paths {
			r.addStroke(pl, width/2)
		}
	}
	mask := image.NewAlpha(r.dst.Bounds())
	r.raster.Draw(mask, mask.Bounds(), image.Opaque, image.Point{})

	var src image.Image
	switch it.Ty {
	case "fl", "st":
		c, ok := it.C.numbers(t)
		if !ok || len(c) < 3 {
			return
		}
		rgba := normalizeColor(c)
		src = image.NewUniform(color.NRGBA64{
			R: unit16(rgba[0]), G: unit16(rgba[1]), B: unit16(rgba[2]),
			A: unit16(rgba[3] * opacity),
		})
	default:
		g, ok := newGradientImage(it, m, opacity, t)
		if !ok {
			return
		}
		src = g
	}
	draw.DrawMask(r.dst, r.dst.Bounds(), src, image.Point{}, mask, image.Point{}, draw.Over)
}

// addPolygon adds a closed outline; overlapping outlines of the same
// orientation union (the rasterizer clamps accumulated coverage).
func (r *renderer) addPolygon(pts []point) {
	if len(pts) < 3 {
		return
	}
	r.raster.MoveTo(float32(pts[0].x), float32(pts[0].y))
	for _, p := range pts[1:] {
		r.raster.LineTo(float32(p.x), float32(p.y))
	}
	r.raster.ClosePath()
}

// addOriented adds a convex polygon wound counter-clockwise, so stroke
// pieces never cancel each other out where they overlap.
func (r *renderer) addOriented(pts []point) {
	area := 0.0
	for i := range pts {
		j := (i + 1) % len(pts)
		area += pts[i].x*pts[j].y - pts[j].x*pts[i].y
	}
	if area < 0 {
		for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
			pts[i], pts[j] = pts[j], pts[i]
		}
	}
	r.addPolygon(pts)
}

// addStroke outlines a polyline as quads per segment plus round joins/caps.
func (r *renderer) addStroke(pl polyline, half float64) {
	pts := pl.pts
	n := len(pts)
	if n == 0 {
		return
	}
	segments := n - 1
	if pl.closed {
		segments = n
	}
	for i := 0; i < segments; i++ {
		a, b := pts[i], pts[(i+1)%n]
		dx, dy := b.x-a.x, b.y-a.y
		l := math.Hypot(dx, dy)
		if l < 1e-9 {
			continue
		}
		nx, ny := -dy/l*half, dx/l*half
		r.addOriented([]point{{a.x + nx, a.y + ny}, {b.x + nx, b.y + ny}, {b.x - nx, b.y - ny}, {a.x - nx, a.y - ny}})
	}
	if half < 0.75 {
		return // sub-pixel joins are invisible
	}
	for _, p := range pts {
		circle := make([]point, 12)
		for k := range circle {
			angle := 2 * math.Pi * float64(k) / 12
			circle[k] = point{p.x + half*math.Cos(angle), p.y + half*math.Sin(angle)}
		}
		r.addOriented(circle)
	}
}

// normalizeColor returns RGBA in 0..1; some exporters write 0..255.
func normalizeColor(c []float64) [4]float64 {
	out := [4]float64{0, 0, 0, 1}
	copy(out[:], c)
	if out[0] > 1 || out[1] > 1 || out[2] > 1 {
		out[0], out[1], out[2] = out[0]/255, out[1]/255, out[2]/255
	}
	for i := range out {
		out[i] = math.Max(0, math.Min(1, out[i]))
	}
	return out
}

func unit16(v float64) uint16 {
	return uint16(math.Round(math.Max(0, math.Min(1, v)) * 0xffff))
}

// gradientImage is a linear or radial Lottie gradient evaluated per pixel
// in the shape's own coordinate space.
type gradientImage struct {
	inv            affine
	radial         bool
	sx, sy, ex, ey float64
	stops          []colorStop
	alphas         []alphaStop
	opacity        float64
	bounds         image.Rectangle
}

type colorStop struct{ at, r, g, b float64 }
type alphaStop struct{ at, a float64 }

func newGradientImage(it *shape, m affine, opacity, t float64) (*gradientImage, bool) {
	inv, ok := m.invert()
	if !ok {
		return nil, false
	}
	raw, ok := it.G.K.numbers(t)
	count := it.G.P
	if !ok || count <= 0 || len(raw) < count*4 {
		return nil, false
	}
	g := &gradientImage{inv: inv, radial: it.T == 2, opacity: opacity, bounds: image.Rect(-1<<20, -1<<20, 1<<20, 1<<20)}
	g.sx, g.sy = it.S.vec2(t, 0, 0)
	g.ex, g.ey = it.E.vec2(t, 0, 0)
	for i := 0; i < count; i++ {
		c := normalizeColor([]float64{raw[i*4+1], raw[i*4+2], raw[i*4+3]})
		g.stops = append(g.stops, colorStop{raw[i*4], c[0], c[1], c[2]})
	}
	for rest := raw[count*4:]; len(rest) >= 2; rest = rest[2:] {
		g.alphas = append(g.alphas, alphaStop{rest[0], rest[1]})
	}
	return g, true
}

func (g *gradientImage) ColorModel() color.Model { return color.NRGBA64Model }
func (g *gradientImage) Bounds() image.Rectangle { return g.bounds }

func (g *gradientImage) At(x, y int) color.Color {
	lx, ly := g.inv.apply(float64(x)+0.5, float64(y)+0.5)
	dx, dy := g.ex-g.sx, g.ey-g.sy
	var f float64
	if g.radial {
		radius := math.Hypot(dx, dy)
		if radius > 0 {
			f = math.Hypot(lx-g.sx, ly-g.sy) / radius
		}
	} else if l2 := dx*dx + dy*dy; l2 > 0 {
		f = ((lx-g.sx)*dx + (ly-g.sy)*dy) / l2
	}
	f = math.Max(0, math.Min(1, f))
	r, gg, b := g.colorAt(f)
	return color.NRGBA64{R: unit16(r), G: unit16(gg), B: unit16(b), A: unit16(g.alphaAt(f) * g.opacity)}
}

func (g *gradientImage) colorAt(f float64) (float64, float64, float64) {
	s := g.stops
	if f <= s[0].at {
		return s[0].r, s[0].g, s[0].b
	}
	for i := 1; i < len(s); i++ {
		if f <= s[i].at {
			span := s[i].at - s[i-1].at
			k := 0.0
			if span > 0 {
				k = (f - s[i-1].at) / span
			}
			return s[i-1].r + (s[i].r-s[i-1].r)*k, s[i-1].g + (s[i].g-s[i-1].g)*k, s[i-1].b + (s[i].b-s[i-1].b)*k
		}
	}
	last := s[len(s)-1]
	return last.r, last.g, last.b
}

func (g *gradientImage) alphaAt(f float64) float64 {
	a := g.alphas
	if len(a) == 0 {
		return 1
	}
	if f <= a[0].at {
		return a[0].a
	}
	for i := 1; i < len(a); i++ {
		if f <= a[i].at {
			span := a[i].at - a[i-1].at
			k := 0.0
			if span > 0 {
				k = (f - a[i-1].at) / span
			}
			return a[i-1].a + (a[i].a-a[i-1].a)*k
		}
	}
	return a[len(a)-1].a
}
