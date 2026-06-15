package Wiring

import (
	"math"
	"testing"
)

// refPortWorldPos is an independent reimplementation of the portWorldPos algorithm,
// used to lock the production code's output. The node center resolves from the
// lattice cell (the only node-position model); a nil cell defaults to {0,0,0}.
func refPortWorldPos(kind string, cell *[3]int, ports []portGeom, name string, isInput bool) vec3 {
	ci, cj, ck := 0, 0, 0
	if cell != nil {
		ci, cj, ck = cell[0], cell[1], cell[2]
	}
	cx, cy, cz := latticeToWorld(ci, cj, ck)
	center := vec3{cx, cy, cz}
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
	g := nodeGeom{
		Kind: "HoldFlip",
		Cell: &[3]int{1, 2, -3},
		Inputs: []portGeom{
			{Name: "In", AnchorId: &anchorId1},
			{Name: "In2"},
		},
		Outputs: []portGeom{{Name: "Out"}},
	}
	got := portWorldPos(g, "In", true)
	want := refPortWorldPos(g.Kind, g.Cell, g.Inputs, "In", true)
	if !almostEqual(got.X, want.X, 1e-9) || !almostEqual(got.Y, want.Y, 1e-9) || !almostEqual(got.Z, want.Z, 1e-9) {
		t.Fatalf("portWorldPos = %+v, want %+v", got, want)
	}
}

func TestArcLengthBetweenPortsCases(t *testing.T) {
	anchorId0, anchorId1, anchorId2 := 0, 1, 2
	cases := []struct {
		name string
		src  nodeGeom
		srcH string
		tgt  nodeGeom
		tgtH string
	}{
		{
			name: "input-to-holdflip-2d",
			src: nodeGeom{Kind: "Input", Cell: &[3]int{0, 1, 0},
				Outputs: []portGeom{{Name: "ToHoldFlip", AnchorId: &anchorId1}}},
			srcH: "ToHoldFlip",
			tgt: nodeGeom{Kind: "HoldFlip", Cell: &[3]int{1, 1, 0},
				Inputs: []portGeom{{Name: "In", AnchorId: &anchorId1}}},
			tgtH: "In",
		},
		{
			name: "nonzero-z-both",
			src: nodeGeom{Kind: "ChainInhibitor", Cell: &[3]int{1, 1, 1},
				Outputs: []portGeom{{Name: "ToNext0", AnchorId: &anchorId1}}},
			srcH: "ToNext0",
			tgt: nodeGeom{Kind: "ChainInhibitor", Cell: &[3]int{1, 1, -1},
				Inputs: []portGeom{{Name: "FromPrevChainInhibitorNode", AnchorId: &anchorId1}}},
			tgtH: "FromPrevChainInhibitorNode",
		},
		{
			name: "anchorid0-and-anchorid2-with-z",
			src: nodeGeom{Kind: "WindowAndGate", Cell: &[3]int{1, 1, 0},
				Outputs: []portGeom{{Name: "ToPassed", AnchorId: &anchorId0}}},
			srcH: "ToPassed",
			tgt: nodeGeom{Kind: "WindowAndGate", Cell: &[3]int{2, 0, 1},
				Inputs: []portGeom{{Name: "FromRight", AnchorId: &anchorId2}}},
			tgtH: "FromRight",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := arcLengthBetweenPorts(c.src, c.srcH, c.tgt, c.tgtH)
			// Straight-segment model: arc length is the chord distance.
			p0 := refPortWorldPos(c.src.Kind, c.src.Cell, c.src.Outputs, c.srcH, false)
			p2 := refPortWorldPos(c.tgt.Kind, c.tgt.Cell, c.tgt.Inputs, c.tgtH, true)
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
		Cell:    &[3]int{0, 0, 0},
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
