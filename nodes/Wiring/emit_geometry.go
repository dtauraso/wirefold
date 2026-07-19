// emit_geometry.go — the geometry-emission half of builders.go, split out as a pure move (no
// logic changes): partnerCenterFn/buildPartnerCenterFn, emitNodeGeometryLocked/emitNodeGeometryWith,
// buildPortGeoms, effectiveRadius, emitNodeBeads, emitHeldBead, emitInputBeads, emitRefillSlide.
// builders.go keeps the reflection-driven port-manifest/node-construction half.

package Wiring

import (
	"context"

	T "github.com/dtauraso/wirefold/Trace"
)

// partnerCenterFn returns the CURRENT world center of the single partner node connected
// to (port, isInput) via one edge — the aimed-port model's one input. ok is false for an
// edgeless port (no partner), which falls back to ring placement. Built once per node by
// newMoveDispatch (dynamic, atomic-snapshot-backed) or once per node at initial construction
// (static, straight off the loaded geoms) — see buildPartnerCenterFn.
type partnerCenterFn func(port string, isInput bool) (vec3, bool)

// buildPartnerCenterFn returns a partnerCenterFn for nodeID: it scans edgeEndpoints (the
// static edge-label → source/target/handle map) for the one edge touching (port, isInput) on
// nodeID and, if found, resolves the partner's current center via centerOf. This is the ONE
// place the (node,port,isInput) → partner-id lookup lives, shared by the static
// (construction-time) and dynamic (mover, atomic-snapshot) callers so both agree.
func buildPartnerCenterFn(nodeID string, edgeEndpoints map[string]EdgeEndpoints, centerOf func(id string) vec3) partnerCenterFn {
	return func(port string, isInput bool) (vec3, bool) {
		for _, ep := range edgeEndpoints {
			if !isInput && ep.Source == nodeID && ep.SourceHandle == port {
				return centerOf(ep.Target), true
			}
			if isInput && ep.Target == nodeID && ep.TargetHandle == port {
				return centerOf(ep.Source), true
			}
		}
		return vec3{}, false
	}
}

// emitNodeGeometryLocked is the emit entry point used by the move dispatch. A CONNECTED port (partnerCenter reports hasPartner) is AIMED at its
// partner's center (portWorldPosAimed) so port→edge→port stays colinear; an edgeless port falls
// back to its own polar-torus ring offset (portWorldPos). A `port ∈ torus` lock is still
// movement-only and only ever applies to an edgeless (ring-placed) port, so it never overrides
// an aimed port's placement. partnerCenter may be nil (no edges known / test callers), in which
// case every port takes the ring-placement fallback.
func emitNodeGeometryLocked(tr *T.Trace, nodeName string, g nodeGeom, partnerCenter partnerCenterFn) {
	emitNodeGeometryWith(tr, nodeName, g, aimedPortPosDir(g, partnerCenter))
}

// aimedPortPosDir returns the port-position/direction closure used by BOTH the node's own
// live geometry emit (emitNodeGeometryLocked, above) and the load-time row seed
// (newMoveDispatch's md.nodeSeeds, node_move.go) — the ONE place aimed-vs-static port
// placement is computed, so seed and live emit can never drift apart. partnerCenter may
// be nil (no edges known / test callers), in which case every port takes the ring-placement
// fallback.
func aimedPortPosDir(g nodeGeom, partnerCenter partnerCenterFn) func(name string, isInput bool) (vec3, vec3) {
	return func(name string, isInput bool) (vec3, vec3) {
		var pc vec3
		hasPartner := false
		if partnerCenter != nil {
			pc, hasPartner = partnerCenter(name, isInput)
		}
		pos := portWorldPosAimed(g, name, isInput, pc, hasPartner)
		if hasPartner {
			if dirVec := pc.sub(nodeWorldPos(g)); dirVec.length() >= portDegenerateEps {
				return pos, dirVec.normalize()
			}
		}
		dir, _ := portDir(g, name, isInput)
		return pos, dir
	}
}

// buildPortGeoms derives the full port-geometry slice (input ports then output ports, same
// order emitNodeGeometryWith streams) from g and a port-position/direction function. Shared
// by emitNodeGeometryWith (live emit) and the load-time row seed so both agree on port order
// and values.
func buildPortGeoms(g nodeGeom, portPosDir func(name string, isInput bool) (pos, dir vec3)) []T.PortGeom {
	ports := make([]T.PortGeom, 0, len(g.Inputs)+len(g.Outputs))
	appendPort := func(name string, isInput bool) {
		pos, dir := portPosDir(name, isInput)
		ports = append(ports, T.PortGeom{
			Name: name, IsInput: isInput,
			PX: pos.X, PY: pos.Y, PZ: pos.Z,
			DX: dir.X, DY: dir.Y, DZ: dir.Z,
		})
	}
	for _, p := range g.Inputs {
		appendPort(p.Name, true)
	}
	for _, p := range g.Outputs {
		appendPort(p.Name, false)
	}
	return ports
}

// effectiveRadius returns the node's REACH radius (max distance to a surface child),
// falling back to nodeR for childless nodes (ReachR == 0) so the value stays sane.
// Used by emitNodeGeometryWith (sphereR).
func effectiveRadius(g nodeGeom) float64 {
	if g.ReachR > 0 {
		return g.ReachR
	}
	return nodeR(g)
}

// emitNodeGeometryWith streams a node-geometry event for g, deriving each port's
// world position + direction from portPosDir. It is called by the one live
// caller, emitNodeGeometryLocked, which supplies the aimed-vs-static port
// direction logic; center, the input-then-output port order, the reach-radius
// fallback, and the ring normals all live here regardless of that logic.
func emitNodeGeometryWith(tr *T.Trace, nodeName string, g nodeGeom, portPosDir func(name string, isInput bool) (pos, dir vec3)) {
	center := nodeWorldPos(g)
	ports := buildPortGeoms(g, portPosDir)
	// sphereR streams the REACH radius (max distance to a surface child) so the TS
	// SphereRing sizes correctly without recomputing geometry. Childless nodes
	// (ReachR == 0) fall back to nodeR so the value stays sane.
	sphereR := effectiveRadius(g)
	// label = the node's human label (g.Label), falling back to the node id so the
	// sidecar always carries a non-empty pill string even for hand-written specs whose
	// geom omits Label.
	label := g.Label
	if label == "" {
		label = nodeName
	}
	tr.NodeGeometry(nodeName, label, g.Kind, center.X, center.Y, center.Z, nodeRadius(g.Kind), sphereR, ports,
		verticalRingNormalX, verticalRingNormalY, verticalRingNormalZ,
		flatRingNormalX, flatRingNormalY, flatRingNormalZ)
}

// emitNodeBeads streams node 1's interior 2x2 buffer as a 4-SLOT SNAPSHOT: one
// node-bead event per fixed slot (rows {0,1} × cols {0,1}). The event's x/y/z
// carry the NODE-LOCAL OFFSET (interiorSlotOffset, relative to the node center —
// NOT a world position); TS renders each bead as a child of the node group, so
// the node center is composed by the scene graph and the beads ride the node on
// move (no re-emit needed). backup is the top row (row 0), working is the bottom
// row (row 1); a slot is PRESENT when its row's slice is at least col+1 long,
// ABSENT (popped) otherwise. Absent slots are emitted with present=false (and
// value 0) so TS can clear them — absence can't be rendered, but an explicit empty
// slot can. Discrete positions only (beads snap to slots; no slide yet). Called from
// the node's injected EmitNodeBeads closure whenever the arrays change. Offsets are
// node-local, so no node geometry is needed.
func emitNodeBeads(tr *T.Trace, nodeName string, working, backup []int) {
	const cols = 2
	emitRow := func(row int, slice []int) {
		for col := 0; col < cols; col++ {
			p := interiorSlotOffset(row, col)
			if col < len(slice) {
				tr.NodeBead(nodeName, row, col, true, slice[col], p.X, p.Y, p.Z)
			} else {
				tr.NodeBead(nodeName, row, col, false, 0, p.X, p.Y, p.Z)
			}
		}
	}
	emitRow(0, backup)  // top row = backup
	emitRow(1, working) // bottom row = working
}

// emitHeldBead streams the HoldNewSendOld node's interior as a SINGLE centered
// bead (row 0, col 0) at the node center (offset 0,0,0). The bead is PRESENT when
// NoValue is the sentinel meaning "no value yet" / "no real bead". Real values
// are non-negative indices so NoValue (-1) never collides with a legitimate
// value. Lives here (not gatecommon) because gatecommon imports Wiring —
// gatecommon.NoValue aliases THIS constant, not the reverse, so every package
// that needs the sentinel (including this one, which cannot import gatecommon)
// shares one definition.
const NoValue = -1

// held != NoValue and colored by the held value (0 = white, 1 = black per the
// existing node-bead convention); held == NoValue (no value seen yet) →
// present=false so the interior renders empty. Called from the node's injected
// EmitHeldBead closure only when the held value changes.
func emitHeldBead(tr *T.Trace, nodeName string, held int) {
	tr.NodeBead(nodeName, 0, 0, held != NoValue, held, 0, 0, 0)
}

// emitInputBeads streams a gate's two held inputs as interior beads: the LEFT
// input on the left of the node (negative x), the RIGHT input on the right
// (positive x), vertically centered. NoValue = not held → present=false. Slot
// keys (0,0)=left, (0,1)=right. Offsets use interiorSlot so they sit inside the
// sphere.
func emitInputBeads(tr *T.Trace, nodeName string, left, right int) {
	s := interiorSlot
	tr.NodeBead(nodeName, 0, 0, left != NoValue, left, -s, 0, 0)
	tr.NodeBead(nodeName, 0, 1, right != NoValue, right, s, 0, 0)
}

// emitRefillSlide runs the clock-paced animated refill for the Input node's
// interior buffer: the OLD backup row (row 0, top) slides DOWN into the working
// row (row 1, bottom) at human speed (the same wire-bead pulse speed), so a paused
// clock freezes the slide just like every wire. beads is the OLD backup contents
// that are becoming the new working row.
//
// Geometry: each bead animates from its row-0 slot offset to its row-1 slot offset
// — a downward translation of rowPitch = row0.y − row1.y in local y. Duration at
// human speed = rowPitch / PulseSpeedWuPerTick ticks. The loop steps t=0 to t=1
// one cycle per SleepCycle (pause-aware — Tick() freezes under Halt). Each frame:
//   - row 1, every col: present, value = beads[col], offset = lerp(row0,row1,t)
//     (keyed to the DESTINATION bottom slot, sliding down from the top position).
//   - row 0, every col: present=false (the top row is empty during the slide).
//
// At t=1 the bottom beads sit exactly at their row-1 offset.
func emitRefillSlide(ctx context.Context, tr *T.Trace, nodeName string, clk Clock, beads []int) {
	if clk == nil || len(beads) == 0 {
		return
	}
	row0Y := interiorSlotOffset(0, 0).Y
	row1Y := interiorSlotOffset(1, 0).Y
	rowPitch := row0Y - row1Y // downward translation distance (local y, positive)
	// Slide runs at the base pulse speed — the same constant speed as the wire
	// beads; the clock is still pause-aware. Duration is a tick count.
	durationTicks := rowPitch / PulseSpeedWuPerTick

	start := clk.Tick()
	emitFrame := func(t float64) {
		for col := 0; col < len(beads); col++ {
			a := interiorSlotOffset(0, col)
			b := interiorSlotOffset(1, col)
			tr.NodeBead(nodeName, 1, col, true, beads[col],
				a.X+(b.X-a.X)*t, a.Y+(b.Y-a.Y)*t, a.Z+(b.Z-a.Z)*t)
		}
		for col := 0; col < len(beads); col++ {
			p := interiorSlotOffset(0, col)
			tr.NodeBead(nodeName, 0, col, false, 0, p.X, p.Y, p.Z)
		}
	}

	emitFrame(0) // initial frame: beads at the top, top row cleared
	for {
		if err := clk.SleepCycle(ctx); err != nil {
			return
		}
		t := float64(clk.Tick()-start) / durationTicks
		if t >= 1 {
			emitFrame(1) // land exactly on the bottom row
			return
		}
		emitFrame(t)
	}
}
