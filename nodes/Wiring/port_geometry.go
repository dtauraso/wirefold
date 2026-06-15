// port_geometry.go — Go mirror of the port-to-port segment geometry in
// tools/topology-vscode/src/webview/three/geometry-helpers.ts.
//
// Go must compute a pulse's travel budget from the SAME segment the
// bead is drawn on: a straight line from the source OUTPUT port's sphere-surface
// point to the target INPUT port's sphere-surface point. This file reproduces
// nodeWorldPos, nodeRadius, portDir and portWorldPos so arcLengthBetweenPorts
// (loader.go / stdin_reader.go) returns the chord length.
//
// Inputs the geometry needs, per node:
//   - kind        → width/height via kindDims (generated from SPEC.md View)
//   - cell        → integer lattice coord (i,j,k); the only node-position model
//                   (nodeWorldPos → latticeToWorld; nil Cell → cell {0,0,0})
//   - port lists  → inputs/outputs with optional side + slot (from the spec node;
//                   falls back to registry ports with default sides when absent)
//
// Every magic number is pulled from CurveParam* constants in curve_params.go.

package Wiring

import "math"

// portGeom is one port's layout descriptor: its name and optional ring-anchor index.
type portGeom struct {
	Name     string
	AnchorId *int // optional index into the flat ring-anchor array; nil → ring slot 0
}

// nodeGeom carries everything the port-curve math needs for one node.
// The lattice Cell is the only node-position model: nodeWorldPos resolves the
// center from Cell (latticeToWorld). A nil Cell defaults to cell {0,0,0} (origin).
type nodeGeom struct {
	Kind    string
	Cell    *[3]int  // integer lattice coord (i,j,k); nil → cell {0,0,0} (origin)
	R       *float64 // optional per-node sphere radius for this node's edges; nil → defaultNodeR (see nodeR)
	Inputs  []portGeom
	Outputs []portGeom
}

// defaultNodeR is the default starting sphere radius (world units) used for a
// node that omits an explicit r. Tunable — chosen as a sensible starting size
// for the sphere-chain layout (B3 will consume this via position computation).
const defaultNodeR = 200.0

// nodeR returns the node's sphere radius: *g.R when set, else defaultNodeR.
func nodeR(g nodeGeom) float64 {
	if g.R != nil {
		return *g.R
	}
	return defaultNodeR
}

// Interior bead render dimensions — mirror scene-content.tsx INTERIOR_BEAD_R +
// torus tube fraction; keep in sync. Each interior bead draws a sphere of radius
// interiorBeadR PLUS a torus ring whose OUTER radius is
// interiorBeadR*(1+interiorTorusTubeFrac). The slot pitch must space by the torus
// outer radius (the larger extent), not the sphere, so adjacent rings don't touch.
const (
	interiorBeadR         = 5.0  // sphere radius (INTERIOR_BEAD_R)
	interiorTorusTubeFrac = 0.12 // torus tube fraction; outer radius = r*(1+frac)
	interiorBeadGap       = 0.2  // small gap BETWEEN adjacent toruses
)

// interiorTorusOuterR is the torus outer radius — the bead's true visual extent.
const interiorTorusOuterR = interiorBeadR * (1 + interiorTorusTubeFrac) // 5.6

// interiorSlot is the 2x2 grid half-pitch, computed TORUS-AWARE from the bead's
// torus outer radius plus half the desired inter-torus gap. Adjacent same-row
// beads are 2*interiorSlot apart, so torus-to-torus gap = 2*interiorSlot -
// 2*rt = interiorBeadGap. Pitch follows bead size (beads are a fixed visual
// size), NOT the node radius — nodeRadius is used only for the wall-fit guarantee.
const interiorSlot = interiorTorusOuterR + interiorBeadGap/2 // 5.9

// interiorSlotOffset returns the NODE-LOCAL OFFSET of the 2x2 interior grid slot
// at (row, col), relative to the node center (NOT a world position): row 0 =
// top/backup, row 1 = bottom/working; col 0 = left, col 1 = right. The grid is
// sized by the bead's TORUS OUTER RADIUS so adjacent rings keep a small gap and
// never overlap:
//
//	slot   = interiorTorusOuterR + interiorBeadGap/2
//	dx = (col - 0.5) * 2*slot
//	dy = (0.5 - row) * 2*slot
//	dz = 0
//
// The grid is centered on the node, so offsets are symmetric about (0,0). TS
// renders the bead as a child of the node group, so its world position =
// node center + offset is composed by the scene graph (no node center added on
// the Go side). Discrete — beads snap to these slot centers. The corner bead's
// torus reach (|offset| + rt) must stay inside the node sphere radius r (see
// TestInteriorBeadsInsideSphere). The Z offset is always 0 (grid is planar).
func interiorSlotOffset(row, col int) vec3 {
	slot := interiorSlot
	pitch := 2 * slot
	return vec3{
		X: (float64(col) - 0.5) * pitch,
		Y: (0.5 - float64(row)) * pitch,
		Z: 0,
	}
}

// kindWidthHeight returns the render width/height for a kind, mirroring the
// TS defaults (width ?? 110, height ?? 60) when the kind is unknown.
func kindWidthHeight(kind string) (float64, float64) {
	if d, ok := kindDims[kind]; ok {
		return d.Width, d.Height
	}
	return 110, 60
}

// nodeRadius mirrors nodeRadius() in geometry-helpers.ts:
//
//	min(width, height) / CurveParamNodeRadiusDivisor
func nodeRadius(kind string) float64 {
	w, h := kindWidthHeight(kind)
	return min(w, h) / float64(CurveParamNodeRadiusDivisor)
}

// nodeWorldPos resolves a node's world center from its lattice Cell — the only
// node-position model. Cell{i,j,k} → latticeToWorld. A nil Cell is a fallback
// default to cell {0,0,0} (the world origin); every live node carries a Cell, so
// the nil branch only guards hand-written/partial specs.
func nodeWorldPos(g nodeGeom) vec3 {
	i, j, k := 0, 0, 0 // fallback default: cell {0,0,0} = origin
	if g.Cell != nil {
		i, j, k = g.Cell[0], g.Cell[1], g.Cell[2]
	}
	x, y, z := latticeToWorld(i, j, k)
	return vec3{X: x, Y: y, Z: z}
}

// Ring-anchor geometry constants. These are Go-local until the TS side adopts them.
// d = anchor diameter, p = padding between anchors along the circumference.
const (
	ringAnchorDiameter = 8.0 // port anchor circle diameter (pixels)
	ringAnchorPadding  = 2.0 // gap between adjacent anchors along the ring
)

// ringAnchorCount returns the number of evenly-spaced anchors that fit around a
// node's ring given radius R: N = floor(2*pi*R / (d+p)), minimum 1.
func ringAnchorCount(R float64) int {
	pitch := ringAnchorDiameter + ringAnchorPadding
	n := int(2 * math.Pi * R / pitch)
	return max(n, 1)
}

// ringAnchorDir returns the unit direction (in the y-up, z-forward plane — the
// same XY plane the side/slot directions live in) for anchor index i in a ring
// of N evenly-spaced slots around a node of radius R. The angle for slot i is:
//
//	theta_i = i * 2*pi / N
//
// and maps to direction (cos theta_i, sin theta_i, 0) in the node-local XY plane.
// i is taken mod N so out-of-range indices wrap safely.
func ringAnchorDir(R float64, i int) vec3 {
	N := ringAnchorCount(R)
	i = ((i % N) + N) % N // safe mod
	theta := float64(i) * 2 * math.Pi / float64(N)
	return vec3{X: math.Cos(theta), Y: math.Sin(theta), Z: 0}
}

// snapToRingAnchorIndex returns the ring-anchor index (0..N-1) whose direction
// best matches the given direction vector for a node of the given kind. The
// winning index i maximises dot(normalize(dir), ringAnchorDir(R, i)). If dir is
// the zero vector, 0 is returned as a safe default.
func snapToRingAnchorIndex(kind string, dir vec3) int {
	R := nodeRadius(kind)
	N := ringAnchorCount(R)
	nd := dir.normalize()
	if nd.length() == 0 {
		return 0
	}
	best := -1
	bestDot := -2.0
	for i := range N {
		d := ringAnchorDir(R, i)
		dot := nd.X*d.X + nd.Y*d.Y + nd.Z*d.Z
		if dot > bestDot {
			bestDot = dot
			best = i
		}
	}
	if best < 0 {
		return 0
	}
	return best
}

// portDir mirrors portDir() in geometry-helpers.ts: the unit direction (in the
// y-up frame, z=0) from node center toward the named port, derived from
// side + slot/auto-spacing. Returns (zeroVec, false) if the port is not found.
func portDir(g nodeGeom, portName string, isInput bool) (vec3, bool) {
	list := g.Outputs
	if isInput {
		list = g.Inputs
	}
	idx := -1
	for i, p := range list {
		if p.Name == portName {
			idx = i
			break
		}
	}
	if idx < 0 {
		return vec3{}, false
	}
	port := list[idx]

	// AnchorId: index into the flat ring-anchor array. nil → ring slot 0 as default.
	anchorIdx := 0
	if port.AnchorId != nil {
		anchorIdx = *port.AnchorId
	}
	R := nodeRadius(g.Kind)
	return ringAnchorDir(R, anchorIdx), true
}

// portWorldPos returns the sphere-surface point in the port direction, or the
// node center when the port is unnamed/unknown. This is the authoritative port
// placement (Go owns geometry); the TS renderer plots from Go's streamed segments.
func portWorldPos(g nodeGeom, portName string, isInput bool) vec3 {
	center := nodeWorldPos(g)
	if portName == "" {
		return center
	}
	dir, ok := portDir(g, portName, isInput)
	if !ok {
		return center
	}
	return center.add(dir.scale(nodeRadius(g.Kind)))
}

// arcLengthBetweenPorts computes the straight chord distance between the
// source node's OUTPUT port and the target node's INPUT port. This is the travel
// budget for a pulse on this edge (wires are straight segments).
func arcLengthBetweenPorts(src nodeGeom, srcHandle string, tgt nodeGeom, tgtHandle string) float64 {
	start := portWorldPos(src, srcHandle, false) // source OUTPUT port
	end := portWorldPos(tgt, tgtHandle, true)    // target INPUT port
	return chordLength(start, end)
}

// segmentBetweenPorts returns the straight-line wireSegment from the source
// OUTPUT port to the target INPUT port. The bead's position stream evaluates
// P(t) = Start + t*(End-Start) on this segment.
func segmentBetweenPorts(src nodeGeom, srcHandle string, tgt nodeGeom, tgtHandle string) wireSegment {
	start := portWorldPos(src, srcHandle, false) // source OUTPUT port
	end := portWorldPos(tgt, tgtHandle, true)    // target INPUT port
	return wireSegment{Start: start, End: end}
}
