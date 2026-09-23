// Package giftpacks holds OwpenGram's built-in gift packs: original,
// procedurally drawn Star Gift sets an operator can import from the admin
// panel in one click (see registry.go; add a pack by adding a file that
// registers one).
//
// This file is the Lottie authoring DSL every pack draws with. It emits only
// structures copied field-for-field (including key order) from a genuine
// Telegram-issued gift .tgs: "tgs":1 root, v5.5.2, 60fps/180f, every
// drawable wrapped in a "gr" ending with "tr", 4-component RGBA, "hd"/"nm"
// everywhere, linear+radial gradient fills (gf) and gradient strokes (gs)
// incl. opacity stops, bezier paths (sh), null layers (ty 3) with parenting,
// per-component easing arrays on non-spatial keyframes, scalar easing +
// to/ti on spatial ones, and NO i/o on any terminal keyframe -- rlottie
// silently drops the whole shape otherwise. Generic Lottie players accept
// all of these deviations, so the only real check is rlottie itself; see
// docs/gift-packs.md before loosening anything here.
package giftpacks

import (
	"encoding/json"
	"math"
	"sort"
)

const (
	canvas = 512
	fr     = 60
	op     = 180.0
	cx     = 256.0
	cy     = 256.0
)

// ---------------------------------------------------------------- colours

type col struct{ r, g, b float64 }

func hx(v int) col {
	return col{float64((v>>16)&0xff) / 255, float64((v>>8)&0xff) / 255, float64(v&0xff) / 255}
}

var (
	white = col{1, 1, 1}
	black = col{0, 0, 0}
)

func (c col) mix(d col, t float64) col {
	return col{c.r + (d.r-c.r)*t, c.g + (d.g-c.g)*t, c.b + (d.b-c.b)*t}
}
func (c col) l(t float64) col       { return c.mix(white, t) }
func (c col) d(t float64) col       { return c.mix(black, t) }
func (c col) rgba() []float64       { return []float64{r3(c.r), r3(c.g), r3(c.b), 1} }
func r3(v float64) float64          { return math.Round(v*1000) / 1000 }
func pt(x, y float64) []float64     { return []float64{r3(x), r3(y)} }
func pt3(x, y, z float64) []float64 { return []float64{r3(x), r3(y), r3(z)} }
func frac(x float64) float64        { return x - math.Floor(x) }
func bump(u, ph float64) float64    { return 0.5 - 0.5*math.Cos(2*math.Pi*(u+ph)) }
func wave(u, ph float64) float64    { return math.Sin(2 * math.Pi * (u + ph)) }
func deg(a float64) float64         { return a * math.Pi / 180 }
func clamp01(v float64) float64     { return math.Max(0, math.Min(1, v)) }
func smooth01(e0, e1, x float64) float64 {
	t := clamp01((x - e0) / (e1 - e0))
	return t * t * (3 - 2*t)
}

// ---------------------------------------------------------------- props

type prop struct {
	A int `json:"a"`
	K any `json:"k"`
}

func sv(v any) prop { return prop{0, v} }

type ease struct {
	X []float64 `json:"x"`
	Y []float64 `json:"y"`
}

type kfr struct {
	I *ease     `json:"i,omitempty"`
	O *ease     `json:"o,omitempty"`
	T float64   `json:"t"`
	S []float64 `json:"s"`
}

type sease struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type skfr struct {
	I  *sease    `json:"i,omitempty"`
	O  *sease    `json:"o,omitempty"`
	T  float64   `json:"t"`
	S  []float64 `json:"s"`
	To []float64 `json:"to,omitempty"`
	Ti []float64 `json:"ti,omitempty"`
}

type key struct {
	t float64
	v []float64
}

func k(t float64, v ...float64) key { return key{t, v} }

func rep(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func roundAll(v []float64) []float64 {
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = r3(x)
	}
	return out
}

// non-spatial animated value; ox/ix are the out/in bezier x handles.
func animWith(ox, oy, ix, iy float64, keys ...key) prop {
	out := make([]kfr, len(keys))
	for i, kk := range keys {
		if i == len(keys)-1 {
			out[i] = kfr{T: kk.t, S: roundAll(kk.v)}
			continue
		}
		n := len(kk.v)
		out[i] = kfr{
			I: &ease{X: rep(ix, n), Y: rep(iy, n)},
			O: &ease{X: rep(ox, n), Y: rep(oy, n)},
			T: kk.t, S: roundAll(kk.v),
		}
	}
	return prop{1, out}
}

func av(keys ...key) prop    { return animWith(0.33, 0, 0.67, 1, keys...) }
func avLin(keys ...key) prop { return animWith(0.167, 0.167, 0.833, 0.833, keys...) }

func spatialWith(ox, oy, ix, iy float64, keys ...key) prop {
	out := make([]skfr, len(keys))
	for i, kk := range keys {
		if i == len(keys)-1 {
			out[i] = skfr{T: kk.t, S: roundAll(kk.v)}
			continue
		}
		z := rep(0, len(kk.v))
		out[i] = skfr{I: &sease{ix, iy}, O: &sease{ox, oy}, T: kk.t, S: roundAll(kk.v), To: z, Ti: z}
	}
	return prop{1, out}
}

func ap(keys ...key) prop    { return spatialWith(0.33, 0, 0.67, 1, keys...) }
func apLin(keys ...key) prop { return spatialWith(0.167, 0.167, 0.833, 0.833, keys...) }

// sampled evaluates f(frac(t/op + phase)) at n+1 evenly spaced frames, plus a
// seam pair (t*-1, t*) where the phase wraps, so a sawtooth f (f(0) != f(1))
// still jumps cleanly at the seam instead of smearing across a whole sample.
func sampled(n int, phase float64, f func(u float64) []float64) []key {
	phase = frac(phase)
	times := []float64{}
	seam := -1.0
	if phase > 0 {
		seam = math.Round((1 - phase) * op)
	}
	for i := 0; i <= n; i++ {
		t := math.Round(float64(i) * op / float64(n))
		if seam > 0 && math.Abs(t-seam) < 2 {
			continue
		}
		times = append(times, t)
	}
	if seam > 1 && seam < op {
		times = append(times, seam-1, seam)
	}
	sort.Float64s(times)
	out := []key{}
	last := -1.0
	for _, t := range times {
		if t <= last {
			continue
		}
		last = t
		u := frac(t/op + phase)
		if t == seam-1 {
			u = frac((t)/op + phase)
		}
		out = append(out, key{t, f(u)})
	}
	return out
}

// ---------------------------------------------------------------- shapes

type P [2]float64

type pathK struct {
	I [][]float64 `json:"i"`
	O [][]float64 `json:"o"`
	V [][]float64 `json:"v"`
	C bool        `json:"c"`
}

type shItem struct {
	Ind int    `json:"ind"`
	Ty  string `json:"ty"`
	Ks  prop   `json:"ks"`
	Nm  string `json:"nm"`
	Hd  bool   `json:"hd"`
}

func pathFrom(closed bool, v, in, out []P) shItem {
	pk := pathK{C: closed}
	for i := range v {
		pk.V = append(pk.V, pt(v[i][0], v[i][1]))
		pk.I = append(pk.I, pt(in[i][0], in[i][1]))
		pk.O = append(pk.O, pt(out[i][0], out[i][1]))
	}
	return shItem{Ind: 0, Ty: "sh", Ks: sv(pk), Nm: "Path 1", Hd: false}
}

func polyPath(closed bool, pts ...P) shItem {
	z := make([]P, len(pts))
	return pathFrom(closed, pts, z, z)
}

// smoothPath is a Catmull-Rom spline through pts.
func smoothPath(closed bool, tension float64, pts ...P) shItem {
	n := len(pts)
	in := make([]P, n)
	out := make([]P, n)
	for i := 0; i < n; i++ {
		var prev, next P
		switch {
		case closed:
			prev, next = pts[(i-1+n)%n], pts[(i+1)%n]
		case i == 0:
			prev, next = pts[0], pts[1]
		case i == n-1:
			prev, next = pts[n-2], pts[n-1]
		default:
			prev, next = pts[i-1], pts[i+1]
		}
		tx := (next[0] - prev[0]) / 6 * tension
		ty := (next[1] - prev[1]) / 6 * tension
		out[i] = P{tx, ty}
		in[i] = P{-tx, -ty}
	}
	return pathFrom(closed, pts, in, out)
}

// roundedPath is a closed polygon with per-corner radius (radii may be one
// value for all corners).
func roundedPath(radii []float64, pts ...P) shItem {
	n := len(pts)
	var v, in, out []P
	for i := 0; i < n; i++ {
		r := radii[0]
		if len(radii) == n {
			r = radii[i]
		}
		p := pts[i]
		prev := pts[(i-1+n)%n]
		next := pts[(i+1)%n]
		dp := unit(P{prev[0] - p[0], prev[1] - p[1]})
		dn := unit(P{next[0] - p[0], next[1] - p[1]})
		if r <= 0 {
			v = append(v, p)
			in = append(in, P{})
			out = append(out, P{})
			continue
		}
		a := P{p[0] + dp[0]*r, p[1] + dp[1]*r}
		b := P{p[0] + dn[0]*r, p[1] + dn[1]*r}
		const kk = 0.5523
		v = append(v, a, b)
		in = append(in, P{}, P{(p[0] - b[0]) * kk, (p[1] - b[1]) * kk})
		out = append(out, P{(p[0] - a[0]) * kk, (p[1] - a[1]) * kk}, P{})
	}
	return pathFrom(true, v, in, out)
}

func unit(p P) P {
	l := math.Hypot(p[0], p[1])
	if l == 0 {
		return P{}
	}
	return P{p[0] / l, p[1] / l}
}

// arcPath is an open elliptical arc from a0 to a1 degrees (0 = +x, 90 = +y).
func arcPath(x, y, rx, ry, a0, a1 float64, segs int) shItem {
	var v, in, out []P
	step := (a1 - a0) / float64(segs)
	h := 4.0 / 3.0 * math.Tan(deg(step)/4)
	for i := 0; i <= segs; i++ {
		a := deg(a0 + step*float64(i))
		px, py := x+rx*math.Cos(a), y+ry*math.Sin(a)
		tx, ty := -rx*math.Sin(a)*h, ry*math.Cos(a)*h
		v = append(v, P{px, py})
		in = append(in, P{-tx, -ty})
		out = append(out, P{tx, ty})
	}
	return pathFrom(false, v, in, out)
}

type elItem struct {
	D  int    `json:"d"`
	Ty string `json:"ty"`
	S  prop   `json:"s"`
	P  prop   `json:"p"`
	Nm string `json:"nm"`
	Hd bool   `json:"hd"`
}

func ell(x, y, w, h float64) elItem {
	return elItem{1, "el", sv(pt(w, h)), sv(pt(x, y)), "Ellipse Path 1", false}
}

type rcItem struct {
	D  int    `json:"d"`
	Ty string `json:"ty"`
	P  prop   `json:"p"`
	S  prop   `json:"s"`
	R  prop   `json:"r"`
	Nm string `json:"nm"`
	Hd bool   `json:"hd"`
}

func rect(x, y, w, h, r float64) rcItem {
	return rcItem{1, "rc", sv(pt(x, y)), sv(pt(w, h)), sv(r3(r)), "Rectangle Path 1", false}
}

type srItem struct {
	Ty string `json:"ty"`
	Sy int    `json:"sy"`
	D  int    `json:"d"`
	Pt prop   `json:"pt"`
	P  prop   `json:"p"`
	R  prop   `json:"r"`
	Ir prop   `json:"ir"`
	Is prop   `json:"is"`
	Or prop   `json:"or"`
	Os prop   `json:"os"`
	Nm string `json:"nm"`
	Hd bool   `json:"hd"`
}

func star(x, y float64, points int, outer, inner, rot float64) srItem {
	return srItem{"sr", 1, 1, sv(float64(points)), sv(pt(x, y)), sv(rot), sv(inner), sv(0.0), sv(outer), sv(0.0), "Polystar Path 1", false}
}

// ---------------------------------------------------------------- paints

type flItem struct {
	Ty string `json:"ty"`
	C  prop   `json:"c"`
	O  prop   `json:"o"`
	R  int    `json:"r"`
	Bm int    `json:"bm"`
	Nm string `json:"nm"`
	Hd bool   `json:"hd"`
}

func fill(c col) flItem { return fillA(c, 1) }
func fillA(c col, a float64) flItem {
	return flItem{"fl", sv(c.rgba()), sv(r3(a * 100)), 1, 0, "Fill 1", false}
}

type stItem struct {
	Ty string `json:"ty"`
	C  prop   `json:"c"`
	O  prop   `json:"o"`
	W  prop   `json:"w"`
	Lc int    `json:"lc"`
	Lj int    `json:"lj"`
	Ml int    `json:"ml"`
	Bm int    `json:"bm"`
	Nm string `json:"nm"`
	Hd bool   `json:"hd"`
}

func stroke(c col, w float64) stItem { return strokeA(c, w, 1) }
func strokeA(c col, w, a float64) stItem {
	return stItem{"st", sv(c.rgba()), sv(r3(a * 100)), sv(r3(w)), 2, 2, 4, 0, "Stroke 1", false}
}

type stop struct {
	p, a float64
	c    col
}

func S(p float64, c col) stop             { return stop{p, 1, c} }
func SA(p float64, c col, a float64) stop { return stop{p, a, c} }

type gradK struct {
	P int  `json:"p"`
	K prop `json:"k"`
}

func grad(stops []stop) gradK {
	var ks []float64
	alpha := false
	for _, s := range stops {
		ks = append(ks, r3(s.p), r3(s.c.r), r3(s.c.g), r3(s.c.b))
		if s.a != 1 {
			alpha = true
		}
	}
	if alpha {
		for _, s := range stops {
			ks = append(ks, r3(s.p), r3(s.a))
		}
	}
	return gradK{len(stops), sv(ks)}
}

type gfItem struct {
	Ty string `json:"ty"`
	O  prop   `json:"o"`
	R  int    `json:"r"`
	Bm int    `json:"bm"`
	G  gradK  `json:"g"`
	S  prop   `json:"s"`
	E  prop   `json:"e"`
	T  int    `json:"t"`
	H  *prop  `json:"h,omitempty"`
	A  *prop  `json:"a,omitempty"`
	Nm string `json:"nm"`
	Hd bool   `json:"hd"`
}

func lin(sx, sy, ex, ey float64, stops ...stop) gfItem {
	return gfItem{Ty: "gf", O: sv(100.0), R: 1, G: grad(stops), S: sv(pt(sx, sy)), E: sv(pt(ex, ey)), T: 1, Nm: "Gradient Fill 1"}
}

func rad(x, y, radius float64, stops ...stop) gfItem {
	h, a := sv(0.0), sv(0.0)
	return gfItem{Ty: "gf", O: sv(100.0), R: 1, G: grad(stops), S: sv(pt(x, y)), E: sv(pt(x+radius, y)), T: 2, H: &h, A: &a, Nm: "Gradient Fill 1"}
}

type gsItem struct {
	Ty string `json:"ty"`
	O  prop   `json:"o"`
	W  prop   `json:"w"`
	G  gradK  `json:"g"`
	S  prop   `json:"s"`
	E  prop   `json:"e"`
	T  int    `json:"t"`
	Lc int    `json:"lc"`
	Lj int    `json:"lj"`
	Bm int    `json:"bm"`
	Nm string `json:"nm"`
	Hd bool   `json:"hd"`
}

func glin(w, sx, sy, ex, ey float64, stops ...stop) gsItem {
	return gsItem{"gs", sv(100.0), sv(r3(w)), grad(stops), sv(pt(sx, sy)), sv(pt(ex, ey)), 1, 2, 2, 0, "Gradient Stroke 1", false}
}

// ---------------------------------------------------------------- groups

type trItem struct {
	Ty string `json:"ty"`
	P  prop   `json:"p"`
	A  prop   `json:"a"`
	S  prop   `json:"s"`
	R  prop   `json:"r"`
	O  prop   `json:"o"`
	SK prop   `json:"sk"`
	SA prop   `json:"sa"`
	Nm string `json:"nm"`
}

func tr(px, py, ax, ay, s, r, o float64) trItem {
	return trItem{"tr", sv(pt(px, py)), sv(pt(ax, ay)), sv(pt(s, s)), sv(r3(r)), sv(r3(o)), sv(0.0), sv(0.0), "Transform"}
}

type grItem struct {
	Ty string `json:"ty"`
	It []any  `json:"it"`
	Nm string `json:"nm"`
	Bm int    `json:"bm"`
	Hd bool   `json:"hd"`
}

func G(items ...any) grItem { return GT(tr(0, 0, 0, 0, 100, 0, 100), items...) }

// GT wraps items with a group transform. Lottie draws the FIRST item of a
// shapes/it list on top, so when the items are sub-groups they're written
// here in painter's order (back to front) and reversed on the way out.
func GT(t trItem, items ...any) grItem {
	allGroups := len(items) > 0
	for _, x := range items {
		if _, ok := x.(grItem); !ok {
			allGroups = false
		}
	}
	if allGroups {
		items = reversed(items)
	}
	it := append(append([]any{}, items...), t)
	return grItem{"gr", it, "Group 1", 0, false}
}

func reversed(items []any) []any {
	out := make([]any, len(items))
	for i, x := range items {
		out[len(items)-1-i] = x
	}
	return out
}

// GRot rotates contents (drawn in absolute coords) about (x,y).
func GRot(x, y, r float64, items ...any) grItem { return GT(tr(x, y, x, y, 100, r, 100), items...) }

// GAt translates contents (drawn around 0,0) to (x,y), rotated by r.
func GAt(x, y, r float64, items ...any) grItem { return GT(tr(x, y, 0, 0, 100, r, 100), items...) }

// GOp sets a group's opacity (0..1).
func GOp(o float64, items ...any) grItem { return GT(tr(0, 0, 0, 0, 100, 0, o*100), items...) }

// ---------------------------------------------------------------- layers

type ksT struct {
	O prop `json:"o"`
	R prop `json:"r"`
	P prop `json:"p"`
	A prop `json:"a"`
	S prop `json:"s"`
}

type layer struct {
	Ddd    int     `json:"ddd"`
	Ind    int     `json:"ind"`
	Ty     int     `json:"ty"`
	Nm     string  `json:"nm"`
	Parent *int    `json:"parent,omitempty"`
	Sr     int     `json:"sr"`
	Ks     ksT     `json:"ks"`
	Ao     int     `json:"ao"`
	Shapes []any   `json:"shapes,omitempty"`
	Ip     float64 `json:"ip"`
	Op     float64 `json:"op"`
	St     float64 `json:"st"`
	Bm     int     `json:"bm"`
}

// pivot: local (x,y) sits at parent's (x,y); rotation/scale pivot there.
func pivot(x, y float64) ksT {
	return ksT{sv(100.0), sv(0.0), sv(pt3(x, y, 0)), sv(pt3(x, y, 0)), sv(pt3(100, 100, 100))}
}

type scene struct {
	name   string
	layers []*layer // back to front
	ind    int
}

func (s *scene) add(name string, ty int, parent int, k ksT, shapes []any) int {
	s.ind++
	l := &layer{Ddd: 0, Ind: s.ind, Ty: ty, Nm: name, Sr: 1, Ks: k, Ao: 0, Shapes: shapes, Ip: 0, Op: op, St: 0, Bm: 0}
	if parent > 0 {
		p := parent
		l.Parent = &p
	}
	s.layers = append(s.layers, l)
	return s.ind
}

func (s *scene) null(name string, parent int, k ksT) int {
	k.O = sv(0.0)
	return s.add(name, 3, parent, k, nil)
}

// shape adds a shape layer; items are sub-groups in painter's order.
func (s *scene) shape(name string, parent int, k ksT, items ...any) int {
	return s.add(name, 4, parent, k, reversed(items))
}

// stepKeys turns a piecewise-constant f(t) into 1-frame-transition keys
// (blinking LEDs, cursors).
func stepKeys(f func(t float64) float64) []key {
	prev := f(0)
	out := []key{k(0, prev)}
	for t := 1.0; t <= op; t++ {
		v := f(t)
		if v != prev {
			if out[len(out)-1].t < t-1 {
				out = append(out, k(t-1, prev))
			}
			out = append(out, k(t, v))
			prev = v
		}
	}
	if out[len(out)-1].t < op {
		out = append(out, k(op, prev))
	}
	return out
}

// stage adds the shared soft floor shadow + a floating root null, returns
// the root's ind. Children of root draw in canvas-centre-relative coords.
func (s *scene) stage(shadowY, shadowW, amp float64) int {
	sh := pivot(0, 0)
	sh.P = sv(pt3(cx, cy+shadowY, 0))
	sh.S = avLin(sampled(18, 0, func(u float64) []float64 {
		v := 100 - 14*bump(u, 0)
		return []float64{v, v, 100}
	})...)
	sh.O = avLin(sampled(18, 0, func(u float64) []float64 { return []float64{100 - 30*bump(u, 0)} })...)
	s.shape("shadow", 0, sh, G(ell(0, 0, shadowW, shadowW*0.16),
		rad(0, 0, shadowW/2, SA(0, black, 0.32), SA(0.6, black, 0.14), SA(1, black, 0))))

	root := pivot(0, 0)
	root.P = apLin(sampled(18, 0, func(u float64) []float64 {
		return []float64{cx, cy - amp*bump(u, 0), 0}
	})...)
	return s.null("root", 0, root)
}

// sparkle: a twinkling 4-point star layer.
func (s *scene) sparkle(parent int, x, y, size float64, c col, phase float64) {
	k := pivot(x, y)
	k.S = avLin(sampled(18, phase, func(u float64) []float64 {
		v := 100 * math.Pow(bump(u, 0), 2.2)
		return []float64{v, v, 100}
	})...)
	k.R = avLin(sampled(18, phase, func(u float64) []float64 { return []float64{u * 90} })...)
	s.shape("sparkle", parent, k,
		G(ell(x, y, size*1.2, size*1.2), rad(x, y, size*0.6, SA(0, c, 0.55), SA(1, c, 0))),
		G(star(x, y, 4, size, size*0.26, 0), fill(c)),
		G(ell(x, y, size*0.3, size*0.3), fill(white)),
	)
}

type doc struct {
	Tgs    int      `json:"tgs"`
	V      string   `json:"v"`
	Fr     float64  `json:"fr"`
	Ip     float64  `json:"ip"`
	Op     float64  `json:"op"`
	W      int      `json:"w"`
	H      int      `json:"h"`
	Nm     string   `json:"nm"`
	Ddd    int      `json:"ddd"`
	Assets []any    `json:"assets"`
	Layers []*layer `json:"layers"`
}

func (s *scene) json() []byte {
	ls := make([]*layer, len(s.layers))
	for i, l := range s.layers {
		ls[len(ls)-1-i] = l
	}
	b, err := json.Marshal(doc{1, "5.5.2", fr, 0, op, canvas, canvas, s.name, 0, []any{}, ls})
	if err != nil {
		panic(err)
	}
	return b
}
