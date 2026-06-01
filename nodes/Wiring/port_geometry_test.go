package Wiring

import (
	"math"
	"testing"
)

// refPortWorldPos is an independent reimplementation of the geometry-helpers.ts
// portWorldPos algorithm, used to lock the production code's output. It mirrors
// nodeWorldPos / nodeRadius / portDir line-for-line so a structural edit to the
// production path that changes the result breaks this test.
func refPortWorldPos(kind string, x, y, z float64, ports []portGeom, name string, isInput bool) vec3 {
	w, h := kindWidthHeight(kind)
	cx := x + w/2
	cy := -(y + h/2)
	cz := z
	center := vec3{cx, cy, cz}
	if name == "" {
		return center
	}
	// find port
	idx := -1
	for i, p := range ports {
		if p.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return center
	}
	port := ports[idx]
	side := port.Side
	if side == "" {
		if isInput {
			side = "left"
		} else {
			side = "right"
		}
	}
	var same []portGeom
	onSide := -1
	for _, p := range ports {
		ps := p.Side
		if ps == "" {
			if isInput {
				ps = "left"
			} else {
				ps = "right"
			}
		}
		if ps == side {
			if p.Name == port.Name {
				onSide = len(same)
			}
			same = append(same, p)
		}
	}
	var pct float64
	if port.Slot != nil {
		pct = []float64{25, 50, 75}[*port.Slot]
	} else {
		pct = float64(onSide+1) * 100 / float64(len(same)+1)
	}
	var bx, by float64
	switch side {
	case "left":
		bx, by = -w/2, h*(0.5-pct/100)
	case "right":
		bx, by = w/2, h*(0.5-pct/100)
	case "top":
		by, bx = h/2, w*(pct/100-0.5)
	default:
		by, bx = -h/2, w*(pct/100-0.5)
	}
	dir := vec3{bx, by, 0}
	l := math.Sqrt(dir.X*dir.X + dir.Y*dir.Y + dir.Z*dir.Z)
	if l == 0 {
		switch side {
		case "left":
			dir = vec3{-1, 0, 0}
		case "right":
			dir = vec3{1, 0, 0}
		case "top":
			dir = vec3{0, 1, 0}
		default:
			dir = vec3{0, -1, 0}
		}
		l = 1
	}
	dir = vec3{dir.X / l, dir.Y / l, dir.Z / l}
	r := nodeRadius(kind)
	return vec3{center.X + dir.X*r, center.Y + dir.Y*r, center.Z + dir.Z*r}
}

// refBezier3DLength is an independent integration of the 3-D quadratic Bezier.
func refBezier3DLength(p0, p2 vec3, bulge float64, n int) float64 {
	mid := vec3{(p0.X + p2.X) / 2, (p0.Y + p2.Y) / 2, (p0.Z + p2.Z) / 2}
	d := vec3{p2.X - p0.X, p2.Y - p0.Y, p2.Z - p0.Z}
	dl := math.Sqrt(d.X*d.X + d.Y*d.Y + d.Z*d.Z)
	edgeDir := vec3{d.X / dl, d.Y / dl, d.Z / dl}
	// lift = (0,0,1) × edgeDir
	lift := vec3{0*edgeDir.Z - 1*edgeDir.Y, 1*edgeDir.X - 0*edgeDir.Z, 0*edgeDir.Y - 0*edgeDir.X}
	ll := math.Sqrt(lift.X*lift.X + lift.Y*lift.Y + lift.Z*lift.Z)
	if ll != 0 {
		lift = vec3{lift.X / ll, lift.Y / ll, lift.Z / ll}
	}
	span := dl
	p1 := vec3{mid.X + lift.X*span*bulge, mid.Y + lift.Y*span*bulge, mid.Z + lift.Z*span*bulge}
	inv := 1.0 / float64(n)
	prev := p0
	total := 0.0
	for i := 1; i <= n; i++ {
		t := float64(i) * inv
		u := 1 - t
		b := vec3{
			u*u*p0.X + 2*u*t*p1.X + t*t*p2.X,
			u*u*p0.Y + 2*u*t*p1.Y + t*t*p2.Y,
			u*u*p0.Z + 2*u*t*p1.Z + t*t*p2.Z,
		}
		dx, dy, dz := b.X-prev.X, b.Y-prev.Y, b.Z-prev.Z
		total += math.Sqrt(dx*dx + dy*dy + dz*dz)
		prev = b
	}
	if total < CurveParamMinArcLength {
		return CurveParamMinArcLength
	}
	return total
}

func almostEqual(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func TestPortWorldPosMirrorsReference(t *testing.T) {
	slot1 := 1
	g := nodeGeom{
		Kind: "ReadGate",
		Pos:  vec3{X: 100, Y: 200, Z: 30},
		Inputs: []portGeom{
			{Name: "FromInput", Side: "left", Slot: &slot1},
			{Name: "FromChainInhibitor", Side: "bottom"},
		},
		Outputs: []portGeom{{Name: "ToChainInhibitor", Side: "top"}},
	}
	got := portWorldPos(g, "FromInput", true)
	want := refPortWorldPos(g.Kind, g.Pos.X, g.Pos.Y, g.Pos.Z, g.Inputs, "FromInput", true)
	if !almostEqual(got.X, want.X, 1e-9) || !almostEqual(got.Y, want.Y, 1e-9) || !almostEqual(got.Z, want.Z, 1e-9) {
		t.Fatalf("portWorldPos = %+v, want %+v", got, want)
	}
}

func TestArcLengthBetweenPortsCases(t *testing.T) {
	slot0, slot1, slot2 := 0, 1, 2
	cases := []struct {
		name     string
		src      nodeGeom
		srcH     string
		tgt      nodeGeom
		tgtH     string
	}{
		{
			name: "input-to-readgate-2d",
			src: nodeGeom{Kind: "Input", Pos: vec3{X: 25, Y: 318},
				Outputs: []portGeom{{Name: "ToReadGate", Side: "right", Slot: &slot1}}},
			srcH: "ToReadGate",
			tgt: nodeGeom{Kind: "ReadGate", Pos: vec3{X: 169, Y: 335},
				Inputs: []portGeom{{Name: "FromInput", Side: "left", Slot: &slot1}}},
			tgtH: "FromInput",
		},
		{
			name: "nonzero-z-both",
			src: nodeGeom{Kind: "ChainInhibitor", Pos: vec3{X: 340, Y: 302, Z: 40},
				Outputs: []portGeom{{Name: "ToNext0", Side: "bottom", Slot: &slot1}}},
			srcH: "ToNext0",
			tgt: nodeGeom{Kind: "ChainInhibitor", Pos: vec3{X: 327, Y: 356, Z: -25},
				Inputs: []portGeom{{Name: "FromPrevChainInhibitorNode", Side: "top", Slot: &slot1}}},
			tgtH: "FromPrevChainInhibitorNode",
		},
		{
			name: "slot0-and-slot2-with-z",
			src: nodeGeom{Kind: "InhibitRightGate", Pos: vec3{X: 225, Y: 333, Z: 12},
				Outputs: []portGeom{{Name: "ToPassed", Side: "right", Slot: &slot0}}},
			srcH: "ToPassed",
			tgt: nodeGeom{Kind: "InhibitRightGate", Pos: vec3{X: 500, Y: 100, Z: 90},
				Inputs: []portGeom{{Name: "FromRight", Side: "right", Slot: &slot2}}},
			tgtH: "FromRight",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := arcLengthBetweenPorts(c.src, c.srcH, c.tgt, c.tgtH)
			p0 := refPortWorldPos(c.src.Kind, c.src.Pos.X, c.src.Pos.Y, c.src.Pos.Z, c.src.Outputs, c.srcH, false)
			p2 := refPortWorldPos(c.tgt.Kind, c.tgt.Pos.X, c.tgt.Pos.Y, c.tgt.Pos.Z, c.tgt.Inputs, c.tgtH, true)
			want := refBezier3DLength(p0, p2, CurveParamBulgeFactor, CurveParamBezierSampleCount)
			if !almostEqual(got, want, 1e-7) {
				t.Fatalf("arcLengthBetweenPorts = %v, want %v", got, want)
			}
			if got < CurveParamMinArcLength {
				t.Fatalf("arc length below floor: %v", got)
			}
		})
	}
}

// TestBezierArcLength keeps the legacy 2-D integrator under test: a straight
// chord (bulge 0) has length equal to the Euclidean distance.
func TestBezierArcLength(t *testing.T) {
	got := BezierArcLength(0, 0, 30, 40, 0, CurveParamBezierSampleCount)
	if !almostEqual(got, 50, 1e-6) {
		t.Fatalf("BezierArcLength straight chord = %v, want 50", got)
	}
	// Floor applies for co-located points.
	if g := BezierArcLength(5, 5, 5, 5, CurveParamBulgeFactor, CurveParamBezierSampleCount); g != CurveParamMinArcLength {
		t.Fatalf("BezierArcLength co-located = %v, want floor %v", g, CurveParamMinArcLength)
	}
}

// TestPortCurveArcLength3D sanity-checks the 3-D integrator against a known
// straight diagonal (bulge contributes nothing only when span is 0; here we use
// bulge 0 to get the exact chord length including dz).
func TestPortCurveArcLength3D(t *testing.T) {
	got := PortCurveArcLength(vec3{0, 0, 0}, vec3{1, 2, 2}, 0, CurveParamBezierSampleCount)
	want := math.Sqrt(1 + 4 + 4) // = 3
	if !almostEqual(got, want, 1e-6) {
		t.Fatalf("PortCurveArcLength 3D chord = %v, want %v", got, want)
	}
}
