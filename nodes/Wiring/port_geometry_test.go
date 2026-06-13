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

// refChordLength is the reference chord-distance formula (straight-segment model):
// Euclidean distance between two 3-D points, floored at CurveParamMinArcLength.
func refChordLength(p0, p2 vec3) float64 {
	dx := p2.X - p0.X
	dy := p2.Y - p0.Y
	dz := p2.Z - p0.Z
	l := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if l < CurveParamMinArcLength {
		return CurveParamMinArcLength
	}
	return l
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
		name string
		src  nodeGeom
		srcH string
		tgt  nodeGeom
		tgtH string
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
			src: nodeGeom{Kind: "AndGate", Pos: vec3{X: 225, Y: 333, Z: 12},
				Outputs: []portGeom{{Name: "ToPassed", Side: "right", Slot: &slot0}}},
			srcH: "ToPassed",
			tgt: nodeGeom{Kind: "AndGate", Pos: vec3{X: 500, Y: 100, Z: 90},
				Inputs: []portGeom{{Name: "FromRight", Side: "right", Slot: &slot2}}},
			tgtH: "FromRight",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := arcLengthBetweenPorts(c.src, c.srcH, c.tgt, c.tgtH)
			// Straight-segment model: arc length is the chord distance.
			p0 := refPortWorldPos(c.src.Kind, c.src.Pos.X, c.src.Pos.Y, c.src.Pos.Z, c.src.Outputs, c.srcH, false)
			p2 := refPortWorldPos(c.tgt.Kind, c.tgt.Pos.X, c.tgt.Pos.Y, c.tgt.Pos.Z, c.tgt.Inputs, c.tgtH, true)
			want := refChordLength(p0, p2)
			if !almostEqual(got, want, 1e-9) {
				t.Fatalf("arcLengthBetweenPorts = %v, want chord %v", got, want)
			}
			if got < CurveParamMinArcLength {
				t.Fatalf("arc length below floor: %v", got)
			}
		})
	}
}

// TestChordLength verifies chordLength returns the Euclidean distance floored at
// CurveParamMinArcLength.
func TestChordLength(t *testing.T) {
	// 3-4-5 right triangle.
	got := chordLength(vec3{0, 0, 0}, vec3{3, 4, 0})
	if !almostEqual(got, 5, 1e-9) {
		t.Fatalf("chordLength 3-4-5 = %v, want 5", got)
	}
	// 3-D diagonal: sqrt(1+4+4) = 3.
	got = chordLength(vec3{0, 0, 0}, vec3{1, 2, 2})
	if !almostEqual(got, 3, 1e-9) {
		t.Fatalf("chordLength 3D = %v, want 3", got)
	}
	// Floor for co-located points.
	if g := chordLength(vec3{5, 5, 5}, vec3{5, 5, 5}); g != CurveParamMinArcLength {
		t.Fatalf("chordLength co-located = %v, want floor %v", g, CurveParamMinArcLength)
	}
}

// TestPortAnchorOverridesSideSlot verifies that a non-nil Anchor makes portDir
// return normalize(anchor) (bypassing side+slot) and portWorldPos places the port
// at center + normalize(anchor)*nodeRadius. Without an anchor the side+slot path is
// unchanged. Uses a kind from the registry so dims/radius are well-defined.
func TestPortAnchorOverridesSideSlot(t *testing.T) {
	// Pick any registered kind for stable dims; geometry math is kind-agnostic.
	var kind string
	for k := range kindDims {
		kind = k
		break
	}
	if kind == "" {
		t.Skip("no kinds in kindDims")
	}

	anchor := vec3{X: 0, Y: 1, Z: 0} // straight up — top of the ring
	g := nodeGeom{
		Kind:   kind,
		Pos:    vec3{X: 10, Y: 20, Z: 0},
		Inputs: []portGeom{{Name: "In", Anchor: &anchor}},
		Outputs: []portGeom{
			{Name: "Out"}, // no anchor → side+slot path
		},
	}

	// Anchored input: direction == normalize(anchor).
	dir, ok := portDir(g, "In", true)
	if !ok {
		t.Fatal("portDir(In) not found")
	}
	want := anchor.normalize()
	if math.Abs(dir.X-want.X) > 1e-9 || math.Abs(dir.Y-want.Y) > 1e-9 || math.Abs(dir.Z-want.Z) > 1e-9 {
		t.Fatalf("anchored portDir = %v, want normalize(anchor) %v", dir, want)
	}

	// Anchored input world pos == center + dir*nodeRadius.
	center := nodeWorldPos(g)
	wantPos := center.add(want.scale(nodeRadius(kind)))
	gotPos := portWorldPos(g, "In", true)
	if math.Abs(gotPos.X-wantPos.X) > 1e-9 || math.Abs(gotPos.Y-wantPos.Y) > 1e-9 || math.Abs(gotPos.Z-wantPos.Z) > 1e-9 {
		t.Fatalf("anchored portWorldPos = %v, want %v", gotPos, wantPos)
	}

	// Un-anchored output: matches the reference side+slot reimplementation.
	refDir, _ := portDir(g, "Out", false)
	refPos := refPortWorldPos(kind, g.Pos.X, g.Pos.Y, g.Pos.Z, g.Outputs, "Out", false)
	gotOut := portWorldPos(g, "Out", false)
	if math.Abs(gotOut.X-refPos.X) > 1e-9 || math.Abs(gotOut.Y-refPos.Y) > 1e-9 || math.Abs(gotOut.Z-refPos.Z) > 1e-9 {
		t.Fatalf("un-anchored Out portWorldPos = %v, want side+slot ref %v", gotOut, refPos)
	}
	_ = refDir
}
