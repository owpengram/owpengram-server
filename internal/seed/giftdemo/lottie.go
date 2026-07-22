package giftdemo

import "encoding/json"

// This file hand-builds small, fully original 512x512 Lottie animations from
// geometric primitives (polystars, ellipses, rings). They are deliberately
// simple placeholders — the point is a legally-clean demo asset set the admin
// can later replace with real artwork, not studio-grade graphics. Everything
// here is emitted as plain shape/transform JSON, with no expressions and no
// external assets, so it passes the Star Gift animation validator unchanged.

const (
	canvasSize = 512
	center     = canvasSize / 2
	frameRate  = 60
	// 120 frames @ 60fps = 2s loop, well under the 30s ceiling.
	outPoint = 120
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

type motion int

const (
	motionSpin motion = iota
	motionPulse
	motionSpinPulse
)

// shapeKind picks which primitive the layer draws.
type shapeKind int

const (
	shapeStar shapeKind = iota // pointed star (polystar type 1)
	shapePolygon
	shapeRing // ellipse outline + inner disc, used for "coin"
	shapeBurst
)

type lottieSpec struct {
	kind    shapeKind
	points  int // star / polygon point count
	fill    rgb
	stroke  rgb
	strokeW float64 // 0 = no stroke
	radius  float64
	motion  motion
}

// prop builds a static Lottie animated-value wrapper {a:0,k:value}.
func prop(value any) map[string]any { return map[string]any{"a": 0, "k": value} }

// easing handles are arrays (never strings), so the validator's expression
// check — which only rejects string-valued "x" keys — never trips on them.
func keyframe(t float64, value []float64) map[string]any {
	return map[string]any{
		"t": t,
		"s": value,
		"i": map[string]any{"x": []float64{0.6}, "y": []float64{1}},
		"o": map[string]any{"x": []float64{0.4}, "y": []float64{0}},
	}
}

func spinRotation() map[string]any {
	return map[string]any{"a": 1, "k": []any{
		keyframe(0, []float64{0}),
		keyframe(outPoint, []float64{360}),
	}}
}

func pulseScale() map[string]any {
	return map[string]any{"a": 1, "k": []any{
		keyframe(0, []float64{100, 100, 100}),
		keyframe(outPoint/2, []float64{114, 114, 114}),
		keyframe(outPoint, []float64{100, 100, 100}),
	}}
}

func transform(m motion) map[string]any {
	rotation := prop(0.0)
	scale := prop([]float64{100, 100, 100})
	switch m {
	case motionSpin:
		rotation = spinRotation()
	case motionPulse:
		scale = pulseScale()
	case motionSpinPulse:
		rotation = spinRotation()
		scale = pulseScale()
	}
	return map[string]any{
		"o":  prop(100.0),
		"r":  rotation,
		"p":  prop([]float64{center, center, 0}),
		"a":  prop([]float64{0, 0, 0}),
		"s":  scale,
		"sk": prop(0.0),
		"sa": prop(0.0),
	}
}

func fill(c rgb) map[string]any {
	return map[string]any{
		"ty": "fl", "nm": "Fill", "r": 1,
		"o": prop(100.0),
		"c": prop([]float64{c[0], c[1], c[2]}),
	}
}

func stroke(c rgb, width float64) map[string]any {
	return map[string]any{
		"ty": "st", "nm": "Stroke", "lc": 2, "lj": 2, "ml": 4,
		"o": prop(100.0),
		"w": prop(width),
		"c": prop([]float64{c[0], c[1], c[2]}),
	}
}

func polystar(points int, starType int, outer, innerRatio float64) map[string]any {
	return map[string]any{
		"ty": "sr", "nm": "Polystar", "sy": starType,
		"d":  1,
		"pt": prop(float64(points)),
		"p":  prop([]float64{0, 0}),
		"r":  prop(0.0),
		"ir": prop(outer * innerRatio),
		"is": prop(0.0),
		"or": prop(outer),
		"os": prop(0.0),
	}
}

func ellipse(radius float64) map[string]any {
	return map[string]any{
		"ty": "el", "nm": "Ellipse", "d": 1,
		"p": prop([]float64{0, 0}),
		"s": prop([]float64{radius * 2, radius * 2}),
	}
}

// renderLottie serializes one spec to Lottie JSON bytes.
func renderLottie(spec lottieSpec) ([]byte, error) {
	var shapes []any
	switch spec.kind {
	case shapeStar:
		shapes = append(shapes, polystar(spec.points, 1, spec.radius, 0.5))
	case shapePolygon:
		shapes = append(shapes, polystar(spec.points, 2, spec.radius, 0.5))
	case shapeBurst:
		shapes = append(shapes, polystar(spec.points, 1, spec.radius, 0.32))
	case shapeRing:
		shapes = append(shapes, ellipse(spec.radius))
	}
	shapes = append(shapes, fill(spec.fill))
	if spec.strokeW > 0 {
		shapes = append(shapes, stroke(spec.stroke, spec.strokeW))
	}
	// A coin gets a smaller contrasting inner disc for a bit of depth.
	if spec.kind == shapeRing {
		shapes = append(shapes, ellipse(spec.radius*0.55), fill(spec.stroke))
	}

	layer := map[string]any{
		"ddd": 0, "ind": 1, "ty": 4, "nm": "gift", "sr": 1,
		"ks":     transform(spec.motion),
		"ao":     0,
		"shapes": shapes,
		"ip":     0, "op": outPoint, "st": 0, "bm": 0,
	}
	root := map[string]any{
		"v": "5.7.4", "fr": frameRate, "ip": 0, "op": outPoint,
		"w": canvasSize, "h": canvasSize, "nm": "owpengram-demo-gift",
		"ddd": 0, "assets": []any{}, "layers": []any{layer},
	}
	return json.Marshal(root)
}
