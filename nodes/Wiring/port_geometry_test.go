package Wiring

import (
	"math"
	"testing"
)

// refPortWorldPos is an independent reimplementation of the portWorldPos algorithm,
// used to lock the production code's output. The node center is provided directly.
func refPortWorldPos(kind string, center vec3, ports []portGeom, name string, isInput bool) vec3 {
	if name == "" {
		return center
	}
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
	anchorIdx := 0
	if port.AnchorId != nil {
		anchorIdx = *port.AnchorId
	}
	R := nodeRadius(kind)
	dir := ringAnchorDir(R, anchorIdx)
	return vec3{center.X + dir.X*R, center.Y + dir.Y*R, center.Z + dir.Z*R}
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
	anchorId1 := 1
	center := vec3{X: 46.5425, Y: 93.085, Z: -139.6275}
	g := nodeGeom{
		Kind:   "HoldFlip",
		Center: &center,
		Inputs: []portGeom{
			{Name: "In", AnchorId: &anchorId1},
			{Name: "In2"},
		},
		Outputs: []portGeom{{Name: "Out"}},
	}
	got := portWorldPos(g, "In", true)
	want := refPortWorldPos(g.Kind, center, g.Inputs, "In", true)
	if !almostEqual(got.X, want.X, 1e-9) || !almostEqual(got.Y, want.Y, 1e-9) || !almostEqual(got.Z, want.Z, 1e-9) {
		t.Fatalf("portWorldPos = %+v, want %+v", got, want)
	}
}

func TestArcLengthBetweenPortsCases(t *testing.T) {
	anchorId0, anchorId1, anchorId2 := 0, 1, 2
	c01 := vec3{X: 0, Y: 46.5425, Z: 0}
	c11 := vec3{X: 46.5425, Y: 46.5425, Z: 0}
	c111 := vec3{X: 46.5425, Y: 46.5425, Z: 46.5425}
	c11n1 := vec3{X: 46.5425, Y: 46.5425, Z: -46.5425}
	c201 := vec3{X: 93.085, Y: 0, Z: 46.5425}
	cases := []struct {
		name string
		src  nodeGeom
		srcH string
		tgt  nodeGeom
		tgtH string
	}{
		{
			name: "input-to-holdflip-2d",
			src: nodeGeom{Kind: "Input", Center: &c01,
				Outputs: []portGeom{{Name: "ToHoldFlip", AnchorId: &anchorId1}}},
			srcH: "ToHoldFlip",
			tgt: nodeGeom{Kind: "HoldFlip", Center: &c11,
				Inputs: []portGeom{{Name: "In", AnchorId: &anchorId1}}},
			tgtH: "In",
		},
		{
			name: "nonzero-z-both",
			src: nodeGeom{Kind: "HoldNewSendOld", Center: &c111,
				Outputs: []portGeom{{Name: "ToNext0", AnchorId: &anchorId1}}},
			srcH: "ToNext0",
			tgt: nodeGeom{Kind: "HoldNewSendOld", Center: &c11n1,
				Inputs: []portGeom{{Name: "FromPrevHoldNewSendOldNode", AnchorId: &anchorId1}}},
			tgtH: "FromPrevHoldNewSendOldNode",
		},
		{
			name: "anchorid0-and-anchorid2-with-z",
			src: nodeGeom{Kind: "WindowAndInhibitRightGate", Center: &c11,
				Outputs: []portGeom{{Name: "ToPassed", AnchorId: &anchorId0}}},
			srcH: "ToPassed",
			tgt: nodeGeom{Kind: "WindowAndInhibitRightGate", Center: &c201,
				Inputs: []portGeom{{Name: "FromRight", AnchorId: &anchorId2}}},
			tgtH: "FromRight",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := arcLengthBetweenPorts(c.src, c.srcH, c.tgt, c.tgtH)
			// Straight-segment model: arc length is the chord distance.
			srcCenter := vec3{}
			if c.src.Center != nil {
				srcCenter = *c.src.Center
			}
			tgtCenter := vec3{}
			if c.tgt.Center != nil {
				tgtCenter = *c.tgt.Center
			}
			p0 := refPortWorldPos(c.src.Kind, srcCenter, c.src.Outputs, c.srcH, false)
			p2 := refPortWorldPos(c.tgt.Kind, tgtCenter, c.tgt.Inputs, c.tgtH, true)
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

// TestPortRadiusPerPort verifies that two ports on the same node with DIFFERENT
// PortR values place each port (and its edge endpoint / arc length) at its own
// radius, not the shared nodeRadius(kind) value.
func TestPortRadiusPerPort(t *testing.T) {
	anchorId0 := 0
	r1, r2 := 15.0, 40.0
	g := nodeGeom{
		Kind: "HoldFlip",
		Inputs: []portGeom{
			{Name: "InSmall", AnchorId: &anchorId0, PortR: &r1},
			{Name: "InBig", AnchorId: &anchorId0, PortR: &r2},
		},
	}
	center := nodeWorldPos(g)
	dir := ringAnchorDir(nodeRadius(g.Kind), 0)

	gotSmall := portWorldPos(g, "InSmall", true)
	wantSmall := center.add(dir.scale(r1))
	if math.Abs(gotSmall.X-wantSmall.X) > 1e-9 || math.Abs(gotSmall.Y-wantSmall.Y) > 1e-9 || math.Abs(gotSmall.Z-wantSmall.Z) > 1e-9 {
		t.Fatalf("portWorldPos(InSmall) = %v, want %v (r=%v)", gotSmall, wantSmall, r1)
	}

	gotBig := portWorldPos(g, "InBig", true)
	wantBig := center.add(dir.scale(r2))
	if math.Abs(gotBig.X-wantBig.X) > 1e-9 || math.Abs(gotBig.Y-wantBig.Y) > 1e-9 || math.Abs(gotBig.Z-wantBig.Z) > 1e-9 {
		t.Fatalf("portWorldPos(InBig) = %v, want %v (r=%v)", gotBig, wantBig, r2)
	}

	if got := portRadiusByName(g, "InSmall", true); got != r1 {
		t.Fatalf("portRadiusByName(InSmall) = %v, want %v", got, r1)
	}
	if got := portRadiusByName(g, "InBig", true); got != r2 {
		t.Fatalf("portRadiusByName(InBig) = %v, want %v", got, r2)
	}
	// A port with no PortR falls back to nodeRadius(kind).
	if got := portRadiusByName(g, "NoSuchPort", true); got != nodeRadius(g.Kind) {
		t.Fatalf("portRadiusByName(unknown) = %v, want fallback %v", got, nodeRadius(g.Kind))
	}
}

// TestArcLengthBetweenPortsUsesEachEndsOwnRadius verifies that when the source
// OUTPUT port and destination INPUT port carry different PortR values, the
// chord distance (edge arc length) is computed from each end's own radius, not
// a shared nodeRadius(kind).
func TestArcLengthBetweenPortsUsesEachEndsOwnRadius(t *testing.T) {
	anchorId0 := 0
	srcR, tgtR := 10.0, 60.0
	srcCenter := vec3{X: 0, Y: 0, Z: 0}
	tgtCenter := vec3{X: 200, Y: 0, Z: 0}
	src := nodeGeom{
		Kind:    "HoldFlip",
		Center:  &srcCenter,
		Outputs: []portGeom{{Name: "Out", AnchorId: &anchorId0, PortR: &srcR}},
	}
	tgt := nodeGeom{
		Kind:   "HoldFlip",
		Center: &tgtCenter,
		Inputs: []portGeom{{Name: "In", AnchorId: &anchorId0, PortR: &tgtR}},
	}
	got := arcLengthBetweenPorts(src, "Out", tgt, "In")

	srcDir := ringAnchorDir(nodeRadius(src.Kind), 0)
	tgtDir := ringAnchorDir(nodeRadius(tgt.Kind), 0)
	p0 := srcCenter.add(srcDir.scale(srcR))
	p1 := tgtCenter.add(tgtDir.scale(tgtR))
	want := chordLength(p0, p1)
	if !almostEqual(got, want, 1e-9) {
		t.Fatalf("arcLengthBetweenPorts = %v, want %v (per-end radii src=%v tgt=%v)", got, want, srcR, tgtR)
	}
}

// TestPortAnchorIdRingPath verifies that AnchorId selects the correct ring slot and
// that a nil AnchorId falls back to ring slot 0.
func TestPortAnchorIdRingPath(t *testing.T) {
	var kind string
	for k := range kindDims {
		kind = k
		break
	}
	if kind == "" {
		t.Skip("no kinds in kindDims")
	}

	anchorId1 := 1
	g := nodeGeom{
		Kind:    kind,
		Inputs:  []portGeom{{Name: "In", AnchorId: &anchorId1}},
		Outputs: []portGeom{{Name: "Out"}}, // nil AnchorId → ring slot 0
	}

	// Anchored input: direction == ringAnchorDir(R, 1)
	R := nodeRadius(kind)
	dir, ok := portDir(g, "In", true)
	if !ok {
		t.Fatal("portDir(In) not found")
	}
	want := ringAnchorDir(R, 1)
	if math.Abs(dir.X-want.X) > 1e-9 || math.Abs(dir.Y-want.Y) > 1e-9 || math.Abs(dir.Z-want.Z) > 1e-9 {
		t.Fatalf("portDir(In, anchorId=1) = %v, want %v", dir, want)
	}

	// Output with nil AnchorId → ring slot 0
	dir0, ok := portDir(g, "Out", false)
	if !ok {
		t.Fatal("portDir(Out) not found")
	}
	want0 := ringAnchorDir(R, 0)
	if math.Abs(dir0.X-want0.X) > 1e-9 || math.Abs(dir0.Y-want0.Y) > 1e-9 || math.Abs(dir0.Z-want0.Z) > 1e-9 {
		t.Fatalf("portDir(Out, nil anchorId) = %v, want ring[0] %v", dir0, want0)
	}

	// World pos == center + dir*nodeRadius
	center := nodeWorldPos(g)
	wantPos := center.add(want.scale(R))
	gotPos := portWorldPos(g, "In", true)
	if math.Abs(gotPos.X-wantPos.X) > 1e-9 || math.Abs(gotPos.Y-wantPos.Y) > 1e-9 || math.Abs(gotPos.Z-wantPos.Z) > 1e-9 {
		t.Fatalf("portWorldPos(In) = %v, want %v", gotPos, wantPos)
	}
}
