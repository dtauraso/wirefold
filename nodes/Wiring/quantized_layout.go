package Wiring

import (
	"math"
)

// quantized_layout.go — the quantized FLAT ABSOLUTE SCENE-POLAR layout: every node is
// positioned independently by its own quantized polar offset — three integers
// (iTheta, iPhi, iR) — measured about the ONE scene-sphere center (no reference/parent
// concept; every node is a "root"). Three GLOBAL step constants (same for every node in
// the graph) turn the integers into a world offset.

// Global quantization step constants — same for every node/edge in the graph. Offsets
// are always integer multiples of these: offset = (iTheta*stepTheta, iPhi*stepPhi,
// iR*stepR).
const (
	stepTheta = math.Pi / 12
	stepPhi   = math.Pi / 12
	// stepR must be smaller than the typical node-to-parent spacing, else every node
	// rounds to iR=0 and collapses onto its parent. Node spacing here is ~80 units, so
	// defaultNodeR (200) collapsed the graph; 20 keeps nodes distinct and makes a drag
	// cross an r-cell responsively. Tunable.
	stepR = 20.0
)

// quantizedOffset is a node's quantized polar offset (iTheta,iPhi,iR) about the ONE
// scene-sphere center. iTheta/iPhi/iR default to zero (at the scene center) until
// authored.
type quantizedOffset struct {
	iTheta int
	iPhi   int
	iR     int
}

// measureScalars is the flat-polar INVERSE measurement: given each node's current world
// center (centers), derive the integer scalar triple (iTheta, iPhi, iR) that is the
// node's polar coordinate about the ONE scene center — the model this file implements
// (see the package-level Model doc in node_move.go / CLAUDE.md). Every node is measured
// independently; there is no reference/parent origin.
//
// ids selects which node ids to measure (so callers can measure a subset without
// building a throwaway map); a node missing a center is omitted (nothing to measure).
func measureScalars(centers map[string]vec3, ids map[string]bool, sceneCenter vec3) map[string]quantizedOffset {
	result := make(map[string]quantizedOffset, len(ids))
	for id := range ids {
		pos, ok := centers[id]
		if !ok {
			continue
		}
		p := cart2polar(pos.sub(sceneCenter))
		result[id] = quantizedOffset{
			iTheta: int(math.Round(p.Theta / stepTheta)),
			iPhi:   int(math.Round(p.Phi / stepPhi)),
			iR:     int(math.Round(p.R / stepR)),
		}
	}
	return result
}

// deriveCenters is the flat-polar FORWARD computation: given each node's scalar triple
// (scalars, from measureScalars or loaded meta.json quantI*), compute every node's world
// center directly about the ONE scene center — every node is independent (no reference/
// parent to resolve first):
//
//	derived[id] = sceneCenter + polar2cart({R: iR*stepR, Theta: iTheta*stepTheta, Phi: iPhi*stepPhi})
func deriveCenters(scalars map[string]quantizedOffset, sceneCenter vec3) map[string]vec3 {
	derived := make(map[string]vec3, len(scalars))
	for id, o := range scalars {
		derived[id] = sceneCenter.add(polar2cart(polar{
			R:     float64(o.iR) * stepR,
			Theta: float64(o.iTheta) * stepTheta,
			Phi:   float64(o.iPhi) * stepPhi,
		}))
	}
	return derived
}
