package Wiring

import (
	"math"
	"sort"
)

// node's quantizedOffset — the stored quantITheta/quantIPhi/quantIR when ALL THREE are
// present (a scene saved under this model), otherwise the offset MEASURED from the
// node's current (pre-quantized) scenePolar-derived center (an old scene, or a node
// whose scenePolar was hand-authored) — then recomputes every node's world center
// directly about the scene center (every node independent — no reference/parent) and
// overwrites b.nodeGeoms/b.centers with the result. Every later phase (reach radii,
// per-edge arc/segment, the movers seeded in buildMoveDispatch) therefore operates on
// the composed centers, and md.quantizedLayout defaults to true (buildMoveDispatch) so
// the live drag path (RootMove) treats this same offset model as authoritative too.
func (b *buildCtx) computeQuantizedLayout() {
	ids := make(map[string]bool, len(b.spec.Nodes))
	for _, n := range b.spec.Nodes {
		ids[n.ID] = true
	}

	// The scalar triple is the STORED quantI* when a scene was saved under this model
	// (all three present); otherwise it is MEASURED from the node's currently-loaded
	// (pre-quantized, scenePolar-derived) center — the fallback for an un-migrated node.
	// prior carries each node's stored per-node step constants (when present in the
	// spec) so measureScalars preserves them into the fallback-measured offset instead
	// of defaulting to global constants for a node that DOES have its own.
	prior := make(map[string]quantizedOffset, len(b.spec.Nodes))
	for _, n := range b.spec.Nodes {
		o := quantizedOffset{}
		if n.StepTheta != nil {
			o.cTheta = *n.StepTheta
		}
		if n.StepPhi != nil {
			o.cPhi = *n.StepPhi
		}
		if n.StepR != nil {
			o.cR = *n.StepR
		}
		prior[n.ID] = o
	}

	measured := measureScalars(b.centers, ids, b.sphere.Center, prior)
	offsets := make(map[string]quantizedOffset, len(b.spec.Nodes))
	// exact marks nodes whose EXACT position was persisted as scenePolar (r,θ,φ). For
	// those, the loaded center (toNodeGeom placed it at exactly that polar) is the
	// authoritative position — it is NOT overwritten by the quantized reconstruction
	// below, so a dragged node reloads at exactly where it was dropped. The quantized
	// triple for such a node is just measured bookkeeping.
	exact := make(map[string]bool, len(b.spec.Nodes))
	for _, n := range b.spec.Nodes {
		if n.ScenePolarR != nil && n.ScenePolarTheta != nil && n.ScenePolarPhi != nil {
			exact[n.ID] = true
			if off, ok := measured[n.ID]; ok {
				offsets[n.ID] = off
			} else {
				offsets[n.ID] = prior[n.ID]
			}
			continue
		}
		if n.QuantITheta != nil && n.QuantIPhi != nil && n.QuantIR != nil {
			o := quantizedOffset{
				iTheta: *n.QuantITheta,
				iPhi:   *n.QuantIPhi,
				iR:     *n.QuantIR,
			}
			if n.StepTheta != nil {
				o.cTheta = *n.StepTheta
			}
			if n.StepPhi != nil {
				o.cPhi = *n.StepPhi
			}
			if n.StepR != nil {
				o.cR = *n.StepR
			}
			offsets[n.ID] = o
			continue
		}
		if off, ok := measured[n.ID]; ok {
			offsets[n.ID] = off
			continue
		}
		offsets[n.ID] = prior[n.ID] // centerless → default to the scene center, keep any stored constants
	}
	b.quantizedOffsets = offsets

	// Reconstruct world centers from the quantized triple ONLY for nodes without an exact
	// stored scenePolar (legacy / un-migrated). Nodes with an exact scenePolar keep the
	// verbatim loaded center — their drag position round-trips losslessly.
	derived := deriveCenters(offsets, b.sphere.Center)
	for id, pos := range derived {
		if exact[id] {
			continue
		}
		b.centers[id] = pos
		if g, ok := b.nodeGeoms[id]; ok {
			setNodeWorld(&g, pos)
			b.nodeGeoms[id] = g
		}
	}
}

// computeLocalPolars resolves each node's LocalPolars list (layout_holder.go
// LocalPolar) — additive, double-link local-polar DATA layered on top of the
// authoritative absolute quantized layout computed just above.
//
// A node's neighbors are every node it shares a domain edge with (either
// direction), deduplicated — 2To5 and 5To2 give node 2 a single neighbor 5, and
// node 5 a single neighbor 2. Each is resolved from the STORED spec value when
// present (a migrated node's meta.json localPolars entry for that neighbor),
// otherwise MEASURED fresh from the composed world centers (b.centers, already
// overwritten by computeQuantizedLayout) using this node's own effective step
// constants: iTheta=round(theta/stepTheta), iPhi=round(phi/stepPhi),
// iR=round(R/stepR) — the same snap contract as measureScalars, but with the
// NEIGHBOR (not the scene center) as the polar origin, and THIS node's steps
// (not the neighbor's).
func (b *buildCtx) computeLocalPolars() {
	neighbors := map[string]map[string]bool{}
	for _, e := range b.spec.Edges {
		if neighbors[e.Source] == nil {
			neighbors[e.Source] = map[string]bool{}
		}
		if neighbors[e.Target] == nil {
			neighbors[e.Target] = map[string]bool{}
		}
		neighbors[e.Source][e.Target] = true
		neighbors[e.Target][e.Source] = true
	}

	stored := map[string]map[string]specLocalPolar{}
	storedPole := map[string]dir{}
	for _, n := range b.spec.Nodes {
		if len(n.LocalPolars) != 0 {
			m := make(map[string]specLocalPolar, len(n.LocalPolars))
			for _, lp := range n.LocalPolars {
				m[lp.To] = lp
			}
			stored[n.ID] = m
		}
		if n.LocalPoleTheta != nil && n.LocalPolePhi != nil {
			storedPole[n.ID] = dir{Theta: *n.LocalPoleTheta, Phi: *n.LocalPolePhi}
		}
	}

	result := map[string][]LocalPolar{}
	poles := map[string]dir{}
	for _, n := range b.spec.Nodes {
		nbrs := neighbors[n.ID]
		if len(nbrs) == 0 {
			continue
		}
		ids := make([]string, 0, len(nbrs))
		for id := range nbrs {
			ids = append(ids, id)
		}
		sort.Strings(ids) // deterministic order

		ownCenter, hasOwn := b.centers[n.ID]
		// Local-polar quantization uses its OWN small, uniform cells (layout_holder.go
		// localStepTheta/localStepPhi/localStepR) — NOT the scene-center triple's
		// coarser stepTheta/stepPhi/stepR (the point of the double-link model: every
		// distance lands on a whole tick of a small grid).
		t, p, r := LocalPolar{}.effectiveSteps()

		// The measurement pole is a pure function of live geometry (rotating_pole.go
		// localPole) — resolved from EVERY neighbor's CURRENT world offset. A STORED pole
		// (persisted by a prior runtime requantize, WriteLocalPolars) is honored verbatim
		// when present, so a reload reconstructs the exact same pole a live drag last
		// resolved rather than depending on recomputing an identical result from geometry;
		// with no stored value (first load / migrated spec) it falls back to the same
		// live-geometry computation as before.
		var finalPole dir
		if sp, ok := storedPole[n.ID]; ok {
			finalPole = sp
		} else {
			offsetVecs := make([]vec3, 0, len(ids))
			if hasOwn {
				for _, mid := range ids {
					if mCenter, ok := b.centers[mid]; ok {
						offsetVecs = append(offsetVecs, mCenter.sub(ownCenter))
					}
				}
			}
			finalPole = localPole(offsetVecs)
		}
		poles[n.ID] = finalPole

		list := make([]LocalPolar, 0, len(ids))
		for _, mid := range ids {
			if sm, ok := stored[n.ID]; ok {
				if lp, ok2 := sm[mid]; ok2 {
					entry := LocalPolar{
						To: mid, QuantITheta: lp.QuantITheta, QuantIPhi: lp.QuantIPhi, QuantIR: lp.QuantIR,
						StepTheta: lp.StepTheta, StepPhi: lp.StepPhi, StepR: lp.StepR,
					}
					// The stored bearing may have been quantized about a DIFFERENT pole
					// (pre-feature data quantized about world +y, or a kick since moved
					// the pole) — re-quantize the bearing about finalPole. Per
					// quantized_move.go's requantizePoleTraced doc contract, an unchanged
					// neighbor's direction is RECONSTRUCTED from its own stored indices
					// about the OLD pole via fromAxisFrame, never re-measured against a
					// live cartesian center (that boundary crossing is what the drag path
					// deliberately avoids for an unchanged neighbor, and what made a load
					// diverge from a drag here). oldPole is the pole THIS node's stored
					// indices were quantized about last save — storedPole[n.ID], defaulting
					// to the zero value dir{} (home, world +y) when absent, which is
					// exactly the same default LayoutHolder.Pole() returns for a node
					// that's never called SetPole (e.g. a pre-pole-persistence save).
					// QuantIR/step constants are preserved exactly (QuantIR carries the
					// equal-radii shared-c contract and must not be recomputed).
					oldPole := storedPole[n.ID]
					et, ep, _ := entry.effectiveSteps()
					d := fromAxisFrame(oldPole, float64(lp.QuantITheta)*et, float64(lp.QuantIPhi)*ep)
					c, psi := azimuthFrom(finalPole, d)
					entry.QuantITheta = int(math.Round(c / et))
					entry.QuantIPhi = int(math.Round(psi / ep))
					list = append(list, entry)
					continue
				}
			}
			mCenter, ok := b.centers[mid]
			if !hasOwn || !ok {
				list = append(list, LocalPolar{To: mid}) // centerless → zero offset, nothing to measure
				continue
			}
			d, radius := dirFromOffset(mCenter.sub(ownCenter))
			c, psi := azimuthFrom(finalPole, d)
			list = append(list, LocalPolar{
				To:          mid,
				QuantITheta: int(math.Round(c / t)),
				QuantIPhi:   int(math.Round(psi / p)),
				QuantIR:     int(math.Round(radius / r)),
			})
		}
		result[n.ID] = list
	}
	b.localPolars = result
	b.localPoles = poles
}

// computeReachRadii computes each node's REACH radius (max distance from its
// center to any node it outputs to) under the loaded centers — non-rooted layout
// — streamed in NodeGeometry's sphereR field so the TS SphereRing reaches every
// surface node. Computed before newMoveDispatch so each node/edge mover captures
// it in its held geom.
func (b *buildCtx) computeReachRadii() {
	edges := make([]sphereEdge, 0, len(b.spec.Edges))
	for _, e := range b.spec.Edges {
		edges = append(edges, sphereEdge{Source: e.Source, Target: e.Target})
	}
	polars := map[string]polar{}
	for id, g := range b.nodeGeoms {
		if g.HasPos {
			polars[id] = g.ScenePolar
		}
	}
	for id, r := range reachRFromPolar(polars, edges) {
		g := b.nodeGeoms[id]
		g.ReachR = r
		b.nodeGeoms[id] = g
	}
}
