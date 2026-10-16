// Package lottieraster draws one frame of a Lottie animation into a small
// raster image.
//
// Telegram clients show a .tgs sticker frozen (a pinned gift, a settled
// reaction, an emoji pattern) from the document's raster "m" thumb, which
// every real Telegram document carries. Documents this server mints itself
// -- star gifts and their collectible models/patterns -- come with only the
// Lottie, so this renders the thumb from it.
//
// It covers what sticker-style Lottie files use: shape and null layers with
// parenting, groups, ellipse / rectangle / path / polystar geometry, solid and
// gradient fills and strokes, and transform/opacity at the chosen frame.
// Masks, mattes, precomps, images, text, trim paths and repeaters are
// skipped, so anything relying on them comes out partial rather than failing.
package lottieraster

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
)

// RenderPNG draws the first visible frame of lottieJSON into a size x size
// PNG: the animation's first frame, or -- when that one is (nearly) blank, as
// in an animation that fades in -- the most covered of a few later frames.
func RenderPNG(lottieJSON []byte, size int) ([]byte, error) {
	img, err := Render(lottieJSON, size)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Render is RenderPNG without the encoding.
func Render(lottieJSON []byte, size int) (*image.NRGBA, error) {
	if size <= 0 {
		return nil, errors.New("lottieraster: size must be positive")
	}
	var a animation
	if err := json.Unmarshal(lottieJSON, &a); err != nil {
		return nil, fmt.Errorf("lottieraster: parse: %w", err)
	}
	if a.W <= 0 || a.H <= 0 {
		return nil, errors.New("lottieraster: animation has no size")
	}
	first := renderFrame(&a, a.IP, size)
	if coverage(first) > size*size/50 || a.OP <= a.IP {
		return first, nil
	}
	best, bestCover := first, coverage(first)
	for k := 1; k < 8; k++ {
		img := renderFrame(&a, a.IP+(a.OP-a.IP)*float64(k)/8, size)
		if c := coverage(img); c > bestCover {
			best, bestCover = img, c
		}
	}
	return best, nil
}

func coverage(img *image.NRGBA) int {
	n := 0
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0 {
			n++
		}
	}
	return n
}

// ---- model ----

type animation struct {
	W      float64 `json:"w"`
	H      float64 `json:"h"`
	IP     float64 `json:"ip"`
	OP     float64 `json:"op"`
	Layers []layer `json:"layers"`
}

type layer struct {
	Ty     int             `json:"ty"`
	Ind    *int            `json:"ind"`
	Parent *int            `json:"parent"`
	IP     float64         `json:"ip"`
	OP     float64         `json:"op"`
	ST     float64         `json:"st"`
	SR     float64         `json:"sr"`
	Hidden bool            `json:"hd"`
	KS     transform       `json:"ks"`
	Shapes []shape         `json:"shapes"`
}

type transform struct {
	A  prop `json:"a"`
	P  prop `json:"p"`
	S  prop `json:"s"`
	R  prop `json:"r"`
	RZ prop `json:"rz"`
	O  prop `json:"o"`
	SK prop `json:"sk"`
	SA prop `json:"sa"`
}

type shape struct {
	Ty     string    `json:"ty"`
	Hidden bool      `json:"hd"`
	Items  []shape   `json:"it"`
	D      int       `json:"d"`
	P      prop      `json:"p"`
	S      prop      `json:"s"`
	E      prop      `json:"e"`
	R      prop      `json:"r"`
	KS     prop      `json:"ks"`
	C      prop      `json:"c"`
	O      prop      `json:"o"`
	W      prop      `json:"w"`
	T      int       `json:"t"`
	G      gradient  `json:"g"`
	Rule   int       `json:"-"`
	SY     int       `json:"sy"`
	PT     prop      `json:"pt"`
	IR     prop      `json:"ir"`
	OR     prop      `json:"or"`
	A      prop      `json:"a"`
	SK     prop      `json:"sk"`
	SA     prop      `json:"sa"`
}

type gradient struct {
	P int  `json:"p"`
	K prop `json:"k"`
}

// UnmarshalJSON keeps a fill's "r" (fill rule, a bare number) apart from a
// transform's/rectangle's "r" (an animatable property object).
func (s *shape) UnmarshalJSON(data []byte) error {
	type plain shape
	var raw struct {
		plain
		R json.RawMessage `json:"r"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*s = shape(raw.plain)
	if len(raw.R) == 0 {
		return nil
	}
	var rule float64
	if err := json.Unmarshal(raw.R, &rule); err == nil {
		s.Rule = int(rule)
		return nil
	}
	return json.Unmarshal(raw.R, &s.R)
}

// prop is a Lottie animatable value: {"a":0,"k":v}, {"a":1,"k":[keyframes]},
// or a split position {"s":true,"x":{...},"y":{...}}.
type prop struct {
	set       bool
	animated  bool
	static    json.RawMessage
	keyframes []keyframe
	split     bool
	x, y      *prop
}

type keyframe struct {
	T    float64         `json:"t"`
	S    json.RawMessage `json:"s"`
	E    json.RawMessage `json:"e"`
	Hold int             `json:"h"`
}

func (p *prop) UnmarshalJSON(data []byte) error {
	var raw struct {
		A json.RawMessage `json:"a"`
		K json.RawMessage `json:"k"`
		S json.RawMessage `json:"s"`
		X *prop           `json:"x"`
		Y *prop           `json:"y"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		// Not an object: a bare value (some exporters inline constants).
		p.set, p.static = true, append(json.RawMessage(nil), data...)
		return nil
	}
	p.set = true
	if raw.X != nil && raw.Y != nil && len(raw.K) == 0 {
		p.split, p.x, p.y = true, raw.X, raw.Y
		return nil
	}
	if len(raw.K) == 0 {
		p.set = false
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw.A), []byte("1")) {
		var kfs []keyframe
		if err := json.Unmarshal(raw.K, &kfs); err == nil && len(kfs) > 0 {
			p.animated, p.keyframes = true, kfs
			return nil
		}
	}
	p.static = raw.K
	return nil
}

// numbers returns the property's value at frame t as a number slice
// (a scalar becomes a one-element slice), linearly interpolating between
// keyframes. ok is false when the property is absent or unreadable.
func (p *prop) numbers(t float64) ([]float64, bool) {
	if p == nil || !p.set {
		return nil, false
	}
	if p.split {
		x, okx := p.x.numbers(t)
		y, oky := p.y.numbers(t)
		if !okx || !oky || len(x) == 0 || len(y) == 0 {
			return nil, false
		}
		return []float64{x[0], y[0]}, true
	}
	if !p.animated {
		return decodeNumbers(p.static)
	}
	kfs := p.keyframes
	if t <= kfs[0].T {
		return decodeNumbers(kfs[0].S)
	}
	for i := 0; i < len(kfs); i++ {
		if i+1 < len(kfs) && t >= kfs[i+1].T {
			continue
		}
		from, ok := decodeNumbers(kfs[i].S)
		if !ok {
			return nil, false
		}
		if i+1 >= len(kfs) || kfs[i].Hold == 1 {
			return from, true
		}
		end := kfs[i].E
		if len(end) == 0 {
			end = kfs[i+1].S
		}
		to, ok := decodeNumbers(end)
		if !ok || len(to) != len(from) || kfs[i+1].T <= kfs[i].T {
			return from, true
		}
		f := (t - kfs[i].T) / (kfs[i+1].T - kfs[i].T)
		out := make([]float64, len(from))
		for j := range from {
			out[j] = from[j] + (to[j]-from[j])*f
		}
		return out, true
	}
	return decodeNumbers(kfs[len(kfs)-1].S)
}

func (p *prop) number(t, fallback float64) float64 {
	v, ok := p.numbers(t)
	if !ok || len(v) == 0 {
		return fallback
	}
	return v[0]
}

func (p *prop) vec2(t float64, fx, fy float64) (float64, float64) {
	v, ok := p.numbers(t)
	if !ok || len(v) == 0 {
		return fx, fy
	}
	if len(v) == 1 {
		return v[0], v[0]
	}
	return v[0], v[1]
}

func decodeNumbers(raw json.RawMessage) ([]float64, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return []float64{n}, true
	}
	var ns []float64
	if err := json.Unmarshal(raw, &ns); err == nil {
		return ns, true
	}
	return nil, false
}

// bezier is one Lottie path value.
type bezier struct {
	I [][]float64 `json:"i"`
	O [][]float64 `json:"o"`
	V [][]float64 `json:"v"`
	C bool        `json:"c"`
}

// path returns a path ("sh") value at frame t; path keyframes are held,
// not interpolated.
func (p *prop) path(t float64) (bezier, bool) {
	if p == nil || !p.set {
		return bezier{}, false
	}
	raw := p.static
	if p.animated {
		raw = p.keyframes[0].S
		for _, kf := range p.keyframes {
			if kf.T <= t && len(kf.S) > 0 {
				raw = kf.S
			}
		}
	}
	var b bezier
	if err := json.Unmarshal(raw, &b); err == nil && len(b.V) > 0 {
		return b, true
	}
	var list []bezier
	if err := json.Unmarshal(raw, &list); err == nil && len(list) > 0 {
		return list[0], true
	}
	return bezier{}, false
}

// ---- geometry ----

// affine maps (x, y) to (a*x + c*y + e, b*x + d*y + f).
type affine struct{ a, b, c, d, e, f float64 }

var identity = affine{a: 1, d: 1}

// mul returns m applied after n (m * n).
func (m affine) mul(n affine) affine {
	return affine{
		a: m.a*n.a + m.c*n.b,
		b: m.b*n.a + m.d*n.b,
		c: m.a*n.c + m.c*n.d,
		d: m.b*n.c + m.d*n.d,
		e: m.a*n.e + m.c*n.f + m.e,
		f: m.b*n.e + m.d*n.f + m.f,
	}
}

func (m affine) apply(x, y float64) (float64, float64) {
	return m.a*x + m.c*y + m.e, m.b*x + m.d*y + m.f
}

func (m affine) invert() (affine, bool) {
	det := m.a*m.d - m.b*m.c
	if math.Abs(det) < 1e-12 {
		return affine{}, false
	}
	return affine{
		a: m.d / det, b: -m.b / det, c: -m.c / det, d: m.a / det,
		e: (m.c*m.f - m.d*m.e) / det, f: (m.b*m.e - m.a*m.f) / det,
	}, true
}

func (m affine) scale() float64 {
	return math.Sqrt(math.Abs(m.a*m.d - m.b*m.c))
}

// matrix is the Lottie transform order: anchor, scale, skew, rotate, position.
func matrix(t float64, a, p, s, r, sk, sa *prop) affine {
	ax, ay := a.vec2(t, 0, 0)
	px, py := p.vec2(t, 0, 0)
	sx, sy := s.vec2(t, 100, 100)
	rot := r.number(t, 0) * math.Pi / 180
	m := affine{a: 1, d: 1, e: -ax, f: -ay}
	m = affine{a: sx / 100, d: sy / 100}.mul(m)
	if skew := sk.number(t, 0); skew != 0 {
		axis := sa.number(t, 0) * math.Pi / 180
		k := math.Tan(-skew * math.Pi / 180)
		cos, sin := math.Cos(axis), math.Sin(axis)
		m = affine{a: cos, b: sin, c: -sin, d: cos}.mul(m)
		m = affine{a: 1, c: k, d: 1}.mul(m)
		m = affine{a: cos, b: -sin, c: sin, d: cos}.mul(m)
	}
	cos, sin := math.Cos(rot), math.Sin(rot)
	m = affine{a: cos, b: sin, c: -sin, d: cos}.mul(m)
	return affine{a: 1, d: 1, e: px, f: py}.mul(m)
}

type point struct{ x, y float64 }

// polyline is one flattened subpath in output pixel space.
type polyline struct {
	pts    []point
	closed bool
}

const flattenSteps = 12

func flattenBezier(b bezier, m affine) polyline {
	n := len(b.V)
	pl := polyline{closed: b.C}
	at := func(list [][]float64, i int) (float64, float64) {
		if i < len(list) && len(list[i]) >= 2 {
			return list[i][0], list[i][1]
		}
		return 0, 0
	}
	if n == 0 {
		return pl
	}
	x0, y0 := at(b.V, 0)
	px, py := m.apply(x0, y0)
	pl.pts = append(pl.pts, point{px, py})
	segments := n - 1
	if b.C {
		segments = n
	}
	for i := 0; i < segments; i++ {
		j := (i + 1) % n
		vx, vy := at(b.V, i)
		ox, oy := at(b.O, i)
		wx, wy := at(b.V, j)
		ix, iy := at(b.I, j)
		c1x, c1y := vx+ox, vy+oy
		c2x, c2y := wx+ix, wy+iy
		for s := 1; s <= flattenSteps; s++ {
			t := float64(s) / flattenSteps
			u := 1 - t
			x := u*u*u*vx + 3*u*u*t*c1x + 3*u*t*t*c2x + t*t*t*wx
			y := u*u*u*vy + 3*u*u*t*c1y + 3*u*t*t*c2y + t*t*t*wy
			px, py := m.apply(x, y)
			pl.pts = append(pl.pts, point{px, py})
		}
	}
	return pl
}

const kappa = 0.5522847498

func ellipseBezier(cx, cy, w, h float64) bezier {
	rx, ry := w/2, h/2
	return bezier{
		V: [][]float64{{cx, cy - ry}, {cx + rx, cy}, {cx, cy + ry}, {cx - rx, cy}},
		I: [][]float64{{-rx * kappa, 0}, {0, -ry * kappa}, {rx * kappa, 0}, {0, ry * kappa}},
		O: [][]float64{{rx * kappa, 0}, {0, ry * kappa}, {-rx * kappa, 0}, {0, -ry * kappa}},
		C: true,
	}
}

func rectBezier(cx, cy, w, h, r float64) bezier {
	hw, hh := w/2, h/2
	r = math.Max(0, math.Min(r, math.Min(hw, hh)))
	if r == 0 {
		zero := [][]float64{{0, 0}, {0, 0}, {0, 0}, {0, 0}}
		return bezier{
			V: [][]float64{{cx + hw, cy - hh}, {cx + hw, cy + hh}, {cx - hw, cy + hh}, {cx - hw, cy - hh}},
			I: zero, O: zero, C: true,
		}
	}
	k := r * kappa
	return bezier{
		V: [][]float64{
			{cx + hw, cy - hh + r}, {cx + hw, cy + hh - r},
			{cx + hw - r, cy + hh}, {cx - hw + r, cy + hh},
			{cx - hw, cy + hh - r}, {cx - hw, cy - hh + r},
			{cx - hw + r, cy - hh}, {cx + hw - r, cy - hh},
		},
		I: [][]float64{{0, -k}, {0, 0}, {k, 0}, {0, 0}, {0, k}, {0, 0}, {-k, 0}, {0, 0}},
		O: [][]float64{{0, 0}, {0, k}, {0, 0}, {-k, 0}, {0, 0}, {0, -k}, {0, 0}, {k, 0}},
		C: true,
	}
}

func starBezier(sh *shape, t float64) bezier {
	cx, cy := sh.P.vec2(t, 0, 0)
	points := int(math.Round(sh.PT.number(t, 5)))
	if points < 3 {
		points = 3
	}
	outer := sh.OR.number(t, 0)
	inner := sh.IR.number(t, 0)
	rot := sh.R.number(t, 0)*math.Pi/180 - math.Pi/2
	var b bezier
	add := func(r, angle float64) {
		b.V = append(b.V, []float64{cx + r*math.Cos(angle), cy + r*math.Sin(angle)})
		b.I = append(b.I, []float64{0, 0})
		b.O = append(b.O, []float64{0, 0})
	}
	step := 2 * math.Pi / float64(points)
	for i := 0; i < points; i++ {
		angle := rot + step*float64(i)
		add(outer, angle)
		if sh.SY == 1 {
			add(inner, angle+step/2)
		}
	}
	b.C = true
	return b
}
