package Wiring

import (
	"math"
)

// quantized_layout.go — the quantized FLAT ABSOLUTE SCENE-POLAR layout: every node is
// positioned independently by its own quantized polar offset — three integers
// (iTheta, iPhi, iR) — measured about the ONE scene-sphere center (no reference/parent
// concept; every node is a "root"). Three GLOBAL step constants (same for every node in
// the graph) turn the integers into a world offset.

// Default quantization step constants — used for any node that has no stored per-node
// step constants (quantizedOffset.cTheta/cPhi/cR == 0). Offsets are always integer
// multiples of a node's EFFECTIVE constants (its own, or these defaults):
// offset = (iTheta*cTheta, iPhi*cPhi, iR*cR).
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
// scene-sphere center, PLUS that node's own step constants (cTheta,cPhi,cR). iTheta/
// iPhi/iR default to zero (at the scene center) until authored. cTheta/cPhi/cR default
// to zero, meaning "unset" — effectiveSteps falls back to the global defaults
// (stepTheta/stepPhi/stepR) for any unset component, so an all-zero quantizedOffset
// reproduces today's global-constant behavior exactly.
type quantizedOffset struct {
	iTheta int
	iPhi   int
	iR     int

	cTheta float64
	cPhi   float64
	cR     float64
}

// effectiveSteps returns this node's own step constants, falling back to the global
// defaults for any component that is unset (zero). Every site that turns a scalar
// triple into (or out of) a world offset MUST go through this — it is the one place
// "per-node step, default fallback" is implemented.
func (o quantizedOffset) effectiveSteps() (t, p, r float64) {
	t, p, r = o.cTheta, o.cPhi, o.cR
	if t == 0 {
		t = stepTheta
	}
	if p == 0 {
		p = stepPhi
	}
	if r == 0 {
		r = stepR
	}
	return
}

// measureScalars is the flat-polar INVERSE measurement: given each node's current world
// center (centers), derive the integer scalar triple (iTheta, iPhi, iR) that is the
// node's polar coordinate about the ONE scene center — the model this file implements
// (see the package-level Model doc in node_move.go / CLAUDE.md). Every node is measured
// independently; there is no reference/parent origin.
//
// ids selects which node ids to measure (so callers can measure a subset without
// building a throwaway map); a node missing a center is omitted (nothing to measure).
//
// prior carries each node's PRIOR quantizedOffset (e.g. md.quantizedOffsets before a
// drag, or the loaded/measured offsets so far) so its stored step constants
// (cTheta/cPhi/cR) can be PRESERVED into the result — a node's constants never change
// on drag/remeasure, only its integer scalars do. prior may be nil (constants default
// to unset → global defaults).
func measureScalars(centers map[string]vec3, ids map[string]bool, sceneCenter vec3, prior map[string]quantizedOffset) map[string]quantizedOffset {
	result := make(map[string]quantizedOffset, len(ids))
	for id := range ids {
		pos, ok := centers[id]
		if !ok {
			continue
		}
		carried := prior[id] // zero value if absent — constants default to unset
		t, p_, r := carried.effectiveSteps()
		p := cart2polar(pos.sub(sceneCenter))
		result[id] = quantizedOffset{
			iTheta: int(math.Round(p.Theta / t)),
			iPhi:   int(math.Round(p.Phi / p_)),
			iR:     int(math.Round(p.R / r)),
			cTheta: carried.cTheta,
			cPhi:   carried.cPhi,
			cR:     carried.cR,
		}
	}
	return result
}

// measureScalar is the single-node variant of measureScalars: given ONE node's current
// world center, derive its integer scalar triple (iTheta, iPhi, iR) about sceneCenter,
// preserving prior's stored step constants (cTheta/cPhi/cR) exactly as measureScalars
// does. Used by the per-node commit path (commitNodeMoveLocal) so
// each node's quantized offset lives on that node's OWN mover (nodeMover.quantOffset)
// rather than a shared map read/written from multiple mover goroutines — see
// node6-drag-decentralized.md / the quantizedOffsets data-race fix.
func measureScalar(pos, sceneCenter vec3, prior quantizedOffset) quantizedOffset {
	t, p_, r := prior.effectiveSteps()
	p := cart2polar(pos.sub(sceneCenter))
	return quantizedOffset{
		iTheta: int(math.Round(p.Theta / t)),
		iPhi:   int(math.Round(p.Phi / p_)),
		iR:     int(math.Round(p.R / r)),
		cTheta: prior.cTheta,
		cPhi:   prior.cPhi,
		cR:     prior.cR,
	}
}

// deriveCenters is the flat-polar FORWARD computation: given each node's scalar triple
// (scalars, from measureScalars or loaded meta.json quantI*), compute every node's world
// center directly about the ONE scene center — every node is independent (no reference/
// parent to resolve first):
//
//	derived[id] = sceneCenter + polar2cart({R: iR*cR, Theta: iTheta*cTheta, Phi: iPhi*cPhi})
//
// using each node's OWN effective step constants (o.effectiveSteps()), falling back to
// the global defaults for any unset component.
func deriveCenters(scalars map[string]quantizedOffset, sceneCenter vec3) map[string]vec3 {
	derived := make(map[string]vec3, len(scalars))
	for id, o := range scalars {
		t, p, r := o.effectiveSteps()
		derived[id] = sceneCenter.add(polar2cart(polar{
			R:     float64(o.iR) * r,
			Theta: float64(o.iTheta) * t,
			Phi:   float64(o.iPhi) * p,
		}))
	}
	return derived
}
