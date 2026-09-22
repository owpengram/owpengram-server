// Package giftpackdefault is OwpenGram's own built-in gift pack: a small,
// original set of Star Gifts covering every mechanic the server supports
// (plain purchase, standard collectible upgrade, limited supply + sold-out,
// craft, an initial resale floor, birthday-only, premium-required,
// support-only, and auction), so an operator can exercise the whole feature
// end to end without needing any third-party pack. It is the direct
// successor to the deleted internal/seed/giftdemo (removed 2026-08-07 along
// with the rest of the paid-features purge) -- same technique, more content.
//
// Every animation here is hand-built from geometric primitives (ellipses,
// rounded rects, polygons/stars) with only transform/opacity/scale/rotation
// keyframes -- no images, no expressions, no masks/mattes/merge-paths or
// repeaters. This is deliberate, not just for the Star Gift animation
// validator (internal/app/stargifts/animation.go), but because rlottie --
// what real Telegram-protocol clients actually render .tgs through, not a
// browser Lottie player -- only implements a subset of the Lottie spec; see
// docs/gift-packs.md's rlottie warning for the long version. Keeping to this
// primitive vocabulary is what makes these animations safe to ship sight
// unseen.
//
// The JSON shape produced here is deliberately not "whatever a Lottie file
// technically permits" but a close structural mirror of a real Bodymovin/
// After Effects export, verified field-for-field against a genuine
// Telegram-issued .tgs (the "v":"5.5.2" version pin, every drawable shape
// wrapped in a "gr" group ending with an identity "tr" transform, an
// explicit 4-component RGBA colour, and "hd"/"nm" present on every shape).
// A hand-rolled flat "shapes: [el, fl]" layer (no group, 3-component RGB,
// "v":"5.7.4") is valid enough that a generic Lottie interpreter (including
// this admin panel's own lottie-web preview) renders it -- and still
// rendered incompletely in the actual Telegram-protocol client this project
// targets, silently, no error anywhere. Do not "simplify" this back down
// without re-diffing against a real .tgs; see docs/gift-packs.md for how.
//
// A second, separate defect survived that whole rewrite: any animated
// (keyframed) property whose terminal keyframe still carried "i"/"o" easing
// -- valid enough for a generic interpreter, and for every fully-static part
// in this pack, which is why some gifts looked fine and others didn't --
// made rlottie fail to render that shape at all from the first following
// frame onward. Found by bisecting a real gift's own animated-scale
// keyframe block against this package's generated equivalent field by
// field, down to individual keyframes, using a local rlottie build as an
// oracle. See keyframe's doc comment.
package giftpackdefault

import (
	"encoding/json"
	"math"
)

const (
	canvasSize = 512
	center     = canvasSize / 2
	frameRate  = 60
	// 120 frames @ 60fps = 2s loop, well under the Star Gift 30s ceiling.
	outPoint = 120
	// lottieVersion matches real Telegram-issued .tgs files exactly
	// (verified against a genuine gift's animation); an unpinned/newer
	// version string is accepted by generic Lottie tooling but is one of
	// the divergences from what this project's actual client renders.
	lottieVersion = "5.5.2"
)

// rgb is a 0..1 normalized colour triplet, the form Lottie fills expect.
type rgb [3]float64

func fromHex(v int) rgb {
	return rgb{
		float64((v>>16)&0xff) / 255,
		float64((v>>8)&0xff) / 255,
		float64(v&0xff) / 255,
	}
}

// shade scales a colour toward black (factor<1) or toward white (factor>1),
// used to derive a shadow/accent tone from a part's primary colour without
// hand-picking a second hex constant for every palette variant.
func shade(c rgb, factor float64) rgb {
	out := rgb{}
	for i, v := range c {
		if factor <= 1 {
			out[i] = v * factor
		} else {
			out[i] = v + (1-v)*(factor-1)
		}
		if out[i] < 0 {
			out[i] = 0
		}
		if out[i] > 1 {
			out[i] = 1
		}
	}
	return out
}

// rgba is c with an explicit alpha=1, the 4-component form a real .tgs
// export uses for every fill/stroke colour (confirmed against a genuine
// gift's animation -- a 3-component colour is valid Lottie but not what
// this project's actual client's exports ever contain).
func rgba(c rgb) []float64 { return []float64{c[0], c[1], c[2], 1} }

// --- Lottie JSON model (ordered structs -- see package doc comment) --------

// prop is a Lottie animated-value wrapper ({"a":0,"k":value} static, or
// {"a":1,"k":[...keyframes]} animated). K holds whatever shape the property
// needs (a scalar, a []float64, or a []keyframe); encoding/json marshals
// through the concrete value, so only prop's own two fields' order matters.
type prop struct {
	A int `json:"a"`
	K any `json:"k"`
}

func staticProp(v any) prop        { return prop{A: 0, K: v} }
func animProp(kfs []keyframe) prop { return prop{A: 1, K: kfs} }

type easing struct {
	X []float64 `json:"x"`
	Y []float64 `json:"y"`
}

// keyframe field order (i,o,t,s) matches a real export exactly. I/O are
// pointers so the terminal keyframe of an animated property can omit them
// entirely (see kfLast) -- a real export never includes them there, and
// rlottie -- confirmed empirically against a real gift's own keyframe data,
// by bisecting exactly this field -- does not just ignore a stray pair on
// the last keyframe, it fails to render that whole animated shape from the
// first following frame onward. This, not the easing arrays' length (see
// kf's own note), was the actual cause of every motionPulse part (the
// candle/cupcake/badge flame and star icons) rendering as nothing beyond
// frame 0.
type keyframe struct {
	I *easing   `json:"i,omitempty"`
	O *easing   `json:"o,omitempty"`
	T float64   `json:"t"`
	S []float64 `json:"s"`
}

// kf builds a non-terminal keyframe. Easing handles are arrays (never
// strings), so the Star Gift validator's expression check -- which only
// rejects string-valued "x" keys -- never trips on them.
//
// A real export's temporal easing arrays for a non-spatial value (this
// package only animates "r" and "s", never "p", so this is always the case
// here) match the animated value's own component count exactly -- one eased
// value per dimension, the same in/out numbers repeated across every
// dimension of s (confirmed against a genuine gift's scale keyframes, e.g.
// s:[100,100] paired with i.x:[0.266,0.266]). A length-1 easing array next
// to a longer s is also malformed and was fixed here, but turned out not to
// be sufficient on its own -- see keyframe's own doc comment for the actual
// remaining cause and how it was found.
func kf(t float64, s []float64) keyframe {
	n := len(s)
	ix, iy := make([]float64, n), make([]float64, n)
	ox, oy := make([]float64, n), make([]float64, n)
	for i := 0; i < n; i++ {
		ix[i], iy[i] = 0.6, 1
		ox[i], oy[i] = 0.4, 0
	}
	return keyframe{I: &easing{X: ix, Y: iy}, O: &easing{X: ox, Y: oy}, T: t, S: s}
}

// kfLast builds the terminal keyframe of an animated property -- no "i"/"o",
// per keyframe's own doc comment. Every animProp call in this package must
// end its slice with one of these, never a plain kf.
func kfLast(t float64, s []float64) keyframe {
	return keyframe{T: t, S: s}
}

// transform is a layer's "ks". Field order (o,r,p,a,s,sk,sa) mirrors a real
// export's property ordering convention.
type transform struct {
	O  prop `json:"o"`
	R  prop `json:"r"`
	P  prop `json:"p"`
	A  prop `json:"a"`
	S  prop `json:"s"`
	SK prop `json:"sk"`
	SA prop `json:"sa"`
}

// groupTransform is the mandatory "tr" item every shape group's "it" array
// ends with (distinct from a layer's "ks" -- this is the group's own local
// transform). An identity one (as used here; every part's actual placement
// happens on the layer's own "ks") still has to be present and complete --
// a real export never omits it, and it is what a flat, group-less "shapes"
// list is missing entirely.
type groupTransform struct {
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

func identityGroupTransform() groupTransform {
	return groupTransform{
		Ty: "tr",
		P:  staticProp([]float64{0, 0}), A: staticProp([]float64{0, 0}), S: staticProp([]float64{100, 100}),
		R: staticProp(0.0), O: staticProp(100.0), SK: staticProp(0.0), SA: staticProp(0.0),
		Nm: "Transform",
	}
}

// groupShapeItem is a Lottie shape group ("gr"): a real export never places
// a drawable shape (el/rc/sr) or a paint (fl/st) directly in a layer's
// "shapes" array -- everything is wrapped in a group, ending with its own
// "tr". Field order (ty,it,nm,bm,hd) matches a real export.
type groupShapeItem struct {
	Ty string `json:"ty"`
	It []any  `json:"it"`
	Nm string `json:"nm"`
	Bm int    `json:"bm"`
	Hd bool   `json:"hd"`
}

// newGroup wraps items (one drawable shape plus one paint, in that order)
// together with the mandatory trailing identity transform.
func newGroup(nm string, items ...any) groupShapeItem {
	it := append(append([]any{}, items...), identityGroupTransform())
	return groupShapeItem{Ty: "gr", It: it, Nm: nm, Bm: 0, Hd: false}
}

// ellipseShapeItem field order (d,ty,s,p,nm,hd) matches a real export
// exactly, including size ("s") preceding position ("p").
type ellipseShapeItem struct {
	D  int    `json:"d"`
	Ty string `json:"ty"`
	S  prop   `json:"s"`
	P  prop   `json:"p"`
	Nm string `json:"nm"`
	Hd bool   `json:"hd"`
}

func newEllipse(w, h float64) ellipseShapeItem {
	return ellipseShapeItem{D: 1, Ty: "el", S: staticProp([]float64{w, h}), P: staticProp([]float64{0, 0}), Nm: "Ellipse Path 1", Hd: false}
}

type rectShapeItem struct {
	D  int    `json:"d"`
	Ty string `json:"ty"`
	P  prop   `json:"p"`
	S  prop   `json:"s"`
	R  prop   `json:"r"`
	Nm string `json:"nm"`
	Hd bool   `json:"hd"`
}

func newRect(w, h, rounded float64) rectShapeItem {
	return rectShapeItem{D: 1, Ty: "rc", P: staticProp([]float64{0, 0}), S: staticProp([]float64{w, h}), R: staticProp(rounded), Nm: "Rectangle Path 1", Hd: false}
}

// polystarShapeItem is a Lottie polystar: sy=1 is a pointed star (needs
// innerRatio<1), sy=2 is a regular polygon (innerRatio ignored).
type polystarShapeItem struct {
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

func newPolystar(points int, starType int, outer, innerRatio float64) polystarShapeItem {
	return polystarShapeItem{
		Ty: "sr", Sy: starType, D: 1,
		Pt: staticProp(float64(points)), P: staticProp([]float64{0, 0}), R: staticProp(0.0),
		Ir: staticProp(outer * innerRatio), Is: staticProp(0.0),
		Or: staticProp(outer), Os: staticProp(0.0),
		Nm: "Polystar Path 1", Hd: false,
	}
}

// fillShapeItem field order (ty,c,o,r,bm,nm,hd) and 4-component colour match
// a real export exactly.
type fillShapeItem struct {
	Ty string `json:"ty"`
	C  prop   `json:"c"`
	O  prop   `json:"o"`
	R  int    `json:"r"`
	Bm int    `json:"bm"`
	Nm string `json:"nm"`
	Hd bool   `json:"hd"`
}

func newFill(c rgb) fillShapeItem {
	return fillShapeItem{Ty: "fl", C: staticProp(rgba(c)), O: staticProp(100.0), R: 1, Bm: 0, Nm: "Fill 1", Hd: false}
}

type strokeShapeItem struct {
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

func newStroke(c rgb, width float64) strokeShapeItem {
	return strokeShapeItem{Ty: "st", C: staticProp(rgba(c)), O: staticProp(100.0), W: staticProp(width), Lc: 2, Lj: 2, Ml: 4, Bm: 0, Nm: "Stroke 1", Hd: false}
}

type lottieLayer struct {
	Ddd    int       `json:"ddd"`
	Ind    int       `json:"ind"`
	Ty     int       `json:"ty"`
	Nm     string    `json:"nm"`
	Sr     int       `json:"sr"`
	Ks     transform `json:"ks"`
	Ao     int       `json:"ao"`
	Shapes []any     `json:"shapes"`
	Ip     float64   `json:"ip"`
	Op     float64   `json:"op"`
	St     float64   `json:"st"`
	Bm     int       `json:"bm"`
}

type animationDoc struct {
	V      string        `json:"v"`
	Fr     float64       `json:"fr"`
	Ip     float64       `json:"ip"`
	Op     float64       `json:"op"`
	W      int           `json:"w"`
	H      int           `json:"h"`
	Nm     string        `json:"nm"`
	Ddd    int           `json:"ddd"`
	Assets []any         `json:"assets"`
	Layers []lottieLayer `json:"layers"`
}

// --- part composer -----------------------------------------------------

// motion is the one animated behavior a part may have, layered on top of its
// static placement.
type motion int

const (
	motionNone motion = iota
	motionSpin
	motionPulse
)

// shapeKind picks which primitive a part draws.
type shapeKind int

const (
	shapeEllipse shapeKind = iota
	shapeRect
	shapePolygon // regular polygon (sides = points)
	shapeStar    // pointed star (sides = points)
)

// part is one layer of a composite icon: a single primitive shape, filled,
// placed at a static offset from canvas center with a static rotation, and
// optionally animated (spin continues from that static rotation; pulse
// scales around the part's own local origin).
type part struct {
	shape       shapeKind
	points      int     // polygon/star side count
	innerRatio  float64 // star inner/outer radius ratio (ignored for polygon)
	w, h        float64 // ellipse: diameter x/y; rect: width/height; polygon/star: outer "radius" x2 (w only)
	rounded     float64 // rect corner radius
	fill        rgb
	noFill      bool // true for a stroke-only outline (e.g. a trophy handle "ring")
	strokeColor rgb
	strokeWidth float64
	offsetX     float64
	offsetY     float64
	rotationDeg float64
	motion      motion
}

func rotationProp(base float64, m motion) prop {
	if m != motionSpin {
		return staticProp(base)
	}
	return animProp([]keyframe{kf(0, []float64{base}), kfLast(outPoint, []float64{base + 360})})
}

func scaleProp(m motion) prop {
	if m != motionPulse {
		return staticProp([]float64{100, 100, 100})
	}
	return animProp([]keyframe{
		kf(0, []float64{100, 100, 100}),
		kf(outPoint/2, []float64{114, 114, 114}),
		kfLast(outPoint, []float64{100, 100, 100}),
	})
}

func transformFor(p part) transform {
	return transform{
		O:  staticProp(100.0),
		R:  rotationProp(p.rotationDeg, p.motion),
		P:  staticProp([]float64{center + p.offsetX, center + p.offsetY, 0}),
		A:  staticProp([]float64{0, 0, 0}),
		S:  scaleProp(p.motion),
		SK: staticProp(0.0),
		SA: staticProp(0.0),
	}
}

func partShape(p part) any {
	switch p.shape {
	case shapeEllipse:
		return newEllipse(p.w, p.h)
	case shapeRect:
		return newRect(p.w, p.h, p.rounded)
	case shapeStar:
		return newPolystar(p.points, 1, p.w/2, p.innerRatio)
	default: // shapePolygon
		return newPolystar(p.points, 2, p.w/2, 1)
	}
}

func partLayer(index int, p part) lottieLayer {
	var paint any
	if p.noFill {
		paint = newStroke(p.strokeColor, p.strokeWidth)
	} else {
		paint = newFill(p.fill)
	}
	group := newGroup("Group 1", partShape(p), paint)
	return lottieLayer{
		Ddd: 0, Ind: index, Ty: 4, Nm: "part", Sr: 1,
		Ks:     transformFor(p),
		Ao:     0,
		Shapes: []any{group},
		Ip:     0, Op: outPoint, St: 0, Bm: 0,
	}
}

// renderIcon composites parts (back to front, part[0] drawn first) into one
// 512x512 Lottie animation and marshals it to JSON. Named after nm purely
// for readability when debugging a dumped file; it has no effect on
// validation or rendering.
func renderIcon(nm string, parts []part) ([]byte, error) {
	layers := make([]lottieLayer, len(parts))
	for i, p := range parts {
		// Lottie stacks layers with the FIRST layer on top, so parts must be
		// emitted in reverse (back-to-front) draw order.
		layers[len(parts)-1-i] = partLayer(i+1, p)
	}
	doc := animationDoc{
		V: lottieVersion, Fr: frameRate, Ip: 0, Op: outPoint,
		W: canvasSize, H: canvasSize, Nm: nm,
		Ddd: 0, Assets: []any{}, Layers: layers,
	}
	return json.Marshal(doc)
}

// petalRing places count identical parts evenly around the canvas center at
// the given radius, each rotated to face outward -- the clover/rose petal
// layout technique.
func petalRing(count int, radius float64, template part) []part {
	out := make([]part, count)
	for i := 0; i < count; i++ {
		angle := float64(i) * 360 / float64(count)
		rad := angle * math.Pi / 180
		p := template
		p.offsetX = radius * math.Cos(rad)
		p.offsetY = radius * math.Sin(rad)
		p.rotationDeg = angle
		out[i] = p
	}
	return out
}
