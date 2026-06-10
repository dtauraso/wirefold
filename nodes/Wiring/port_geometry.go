// port_geometry.go — Go mirror of the port-to-port curve geometry in
// tools/topology-vscode/src/webview/three/geometry-helpers.ts.
//
// The substrate must compute a pulse's travel budget from the SAME curve the
// bead is drawn on: a quadratic Bezier from the source OUTPUT port's sphere-
// surface point to the target INPUT port's sphere-surface point, lifted into 3-D.
// This file reproduces nodeWorldPos, nodeRadius, portDir and portWorldPos so
// arcLengthBetweenPorts (loader.go / stdin_reader.go) matches buildPortCurve.
//
// Inputs the geometry needs, per node:
//   - kind        → width/height via kindDims (generated from SPEC.md View)
//   - position    → x, y, z (z defaults to 0)
//   - port lists  → inputs/outputs with optional side + slot (from the spec node;
//                   falls back to registry ports with default sides when absent)
//
// Every magic number is pulled from CurveParam* constants in curve_params.go.

package Wiring

// portGeom is one port's layout descriptor: its name, resolved side, and
// optional snap slot (nil = auto-spaced along the side).
type portGeom struct {
	Name string
	Side string // "left" | "right" | "top" | "bottom"; "" → default by direction
	Slot *int   // 0|1|2, or nil for auto-spacing
}

// nodeGeom carries everything the port-curve math needs for one node.
type nodeGeom struct {
	Kind     string
	Pos      vec3
	Inputs   []portGeom
	Outputs  []portGeom
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
//   min(width, height) / CurveParamNodeRadiusDivisor
func nodeRadius(kind string) float64 {
	w, h := kindWidthHeight(kind)
	m := w
	if h < m {
		m = h
	}
	return m / float64(CurveParamNodeRadiusDivisor)
}

// nodeWorldPos mirrors nodeWorldPos() in geometry-helpers.ts: the RF y-down →
// Three y-up flip, offsetting by half-dimensions to reach the node center.
// z passes through unchanged.
func nodeWorldPos(g nodeGeom) vec3 {
	w, h := kindWidthHeight(g.Kind)
	return vec3{
		X: g.Pos.X + w/2,
		Y: -(g.Pos.Y + h/2),
		Z: g.Pos.Z,
	}
}

// defaultSide returns the resolved side for a port given its direction, matching
// `port.side ?? (isInput ? "left" : "right")` in portDir().
func defaultSide(side string, isInput bool) string {
	if side != "" {
		return side
	}
	if isInput {
		return "left"
	}
	return "right"
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
	side := defaultSide(port.Side, isInput)

	// Ports sharing this resolved side, in list order.
	var sameSide []portGeom
	onSideIdx := -1
	for _, p := range list {
		if defaultSide(p.Side, isInput) == side {
			if p.Name == port.Name {
				onSideIdx = len(sameSide)
			}
			sameSide = append(sameSide, p)
		}
	}

	var pct float64
	if port.Slot != nil {
		pct = slotPct(*port.Slot)
	} else {
		pct = float64(onSideIdx+1) * 100 / float64(len(sameSide)+1)
	}

	w, h := kindWidthHeight(g.Kind)
	// Local border point offset from center (y-up): pct measured from top for
	// left/right, from left for top/bottom.
	var bx, by float64
	switch side {
	case "left":
		bx, by = -w/2, h*(0.5-pct/100)
	case "right":
		bx, by = w/2, h*(0.5-pct/100)
	case "top":
		by, bx = h/2, w*(pct/100-0.5)
	default: // bottom
		by, bx = -h/2, w*(pct/100-0.5)
	}
	dir := vec3{X: bx, Y: by}
	if dir.length() == 0 {
		// Exact center fallback: cardinal by side.
		switch side {
		case "left":
			dir = vec3{X: -1}
		case "right":
			dir = vec3{X: 1}
		case "top":
			dir = vec3{Y: 1}
		default:
			dir = vec3{Y: -1}
		}
	}
	return dir.normalize(), true
}

// portWorldPos mirrors portWorldPos() in geometry-helpers.ts: the sphere-surface
// point in the port direction, or the node center when the port is unnamed/unknown.
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

// arcLengthBetweenPorts computes the port-to-port Bezier arc length between the
// source node's OUTPUT port and the target node's INPUT port, mirroring
// buildPortCurve. This is the travel budget for a pulse on this edge.
func arcLengthBetweenPorts(src nodeGeom, srcHandle string, tgt nodeGeom, tgtHandle string) float64 {
	p0 := portWorldPos(src, srcHandle, false) // source OUTPUT port
	p2 := portWorldPos(tgt, tgtHandle, true)  // target INPUT port
	return PortCurveArcLength(p0, p2, CurveParamBulgeFactor, CurveParamBezierSampleCount)
}

// curveBetweenPorts computes the quadratic-bezier control points (P0, P1, P2) of
// the port-to-port curve between the source OUTPUT port and the target INPUT port,
// mirroring buildPortCurve in geometry-helpers.ts. The bead's position stream
// evaluates this exact curve, so it matches the drawn wire. P1 is the bulge
// control point (bezierControlPoint).
func curveBetweenPorts(src nodeGeom, srcHandle string, tgt nodeGeom, tgtHandle string) edgeCurve {
	p0 := portWorldPos(src, srcHandle, false) // source OUTPUT port
	p2 := portWorldPos(tgt, tgtHandle, true)  // target INPUT port
	p1 := bezierControlPoint(p0, p2, CurveParamBulgeFactor)
	return edgeCurve{P0: p0, P1: p1, P2: p2}
}
