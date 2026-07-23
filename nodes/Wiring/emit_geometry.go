// emit_geometry.go — the geometry-emission half of builders.go, split out as a pure move (no
// logic changes): partnerCenterFn/buildPartnerCenterFn, emitNodeGeometryLocked/emitNodeGeometryWith,
// buildPortGeoms, effectiveRadius, emitNodeBeads, emitHeldBead, emitInputBeads, emitRefillSlide.
// builders.go keeps the reflection-driven port-manifest/node-construction half.

package Wiring

import (
	"context"
	"encoding/binary"
	"io"

	T "github.com/dtauraso/wirefold/Trace"
)

// interiorStream bundles ONE node's own dedicated interior fd + injected frame builder +
// a local monotonic tick counter, so emitNodeBeads/emitHeldBead/emitInputBeads can pass
// one small value instead of three loose params. Built once per node (injectClosures);
// nil-safe (a zero-value *interiorStream is fine — write is a no-op when out is nil).
type interiorStream struct {
	out        io.Writer
	buildFrame func(tick uint32, present []uint8, value []int32, ox, oy, oz []float32, events []RowEvent) []byte
	tick       uint32
	// nodeRow is this node's stable buffer NODE-ROW index, resolved once at
	// construction (buildInteriorStream) — carried on every NodeBead/Fire/Recv/Send
	// event this stream records (memory/feedback_no_single_writer_bridge.md).
	nodeRow int32
	// lastPresent/lastValue/lastOx/lastOy/lastOz cache the most recently written 4-slot
	// interior-bead snapshot. BuildInteriorStreamFrame's slot count is FIXED (the decoder
	// reads a constant INTERIOR_SLOTS_PER_NODE, not a length carried by the frame — see
	// buffer-decode.ts), so an events-only flush (writeEvents, for a Fire/Recv/Send
	// occurring BETWEEN bead-state changes) must still ship a full, valid 4-slot
	// snapshot — it reuses this cache rather than inventing/omitting bead state.
	// Populated to an all-absent 4-slot snapshot at construction (buildInteriorStream)
	// and refreshed by every write() call.
	lastPresent            []uint8
	lastValue              []int32
	lastOx, lastOy, lastOz []float32
}

// write packs and writes this node's current interior-slot arrays via
// writeInteriorStreamFrame, advancing its own local tick counter. No-op (including on a
// nil receiver) when out/buildFrame aren't wired — the fallback path. events carries
// this call's own row-resolved NodeBead events, recorded by the caller in the SAME
// function invocation (emitNodeBeads/emitHeldBead/emitInputBeads) that built them.
// Caches the passed bead-slot arrays (see lastPresent's doc comment) so a later
// writeEvents call has a valid snapshot to reuse.
func (s *interiorStream) write(present []uint8, value []int32, ox, oy, oz []float32, events []RowEvent) {
	if s == nil {
		return
	}
	s.lastPresent, s.lastValue = present, value
	s.lastOx, s.lastOy, s.lastOz = ox, oy, oz
	s.tick++
	writeInteriorStreamFrame(s.out, s.buildFrame, s.tick, present, value, ox, oy, oz, events)
}

// writeEvents flushes an events-only interior-stream frame: no bead-slot state has
// changed since the last write, so it reuses the cached last-known 4-slot snapshot
// (lastPresent's doc comment) and carries only the caller's new row-resolved
// RowEvents (Fire/Recv/Send — see owner_events.go). No-op on a nil receiver, same as
// write.
func (s *interiorStream) writeEvents(events []RowEvent) {
	if s == nil {
		return
	}
	s.write(s.lastPresent, s.lastValue, s.lastOx, s.lastOy, s.lastOz, events)
}

// boolU8 converts a bool to the buffer's canonical 0/1 byte encoding — a local copy of
// Buffer.boolU8 (unexported there), avoided rather than importing Buffer into this
// Buffer-independent package.
func boolU8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// writeInteriorStreamFrame packs and writes ONE node's current fixed 4-slot interior
// state to its OWN dedicated fd (out) via buildFrame (Buffer.BuildInteriorStreamFrame,
// injected so this package needs no Buffer import) — the SECOND emitting goroutine per
// node (memory/feedback_no_single_writer_bridge.md): this node's own Update loop, called
// from the SAME goroutine as the tr.NodeBead calls beside each call site below. No-op
// when out is nil (no dedicated interior fd for this node — the fallback path) or
// buildFrame is nil (no WIREFOLD_STREAM_FDS "interior" entry). tick is a local
// monotonically-increasing counter (informational only — freshness, not correctness; the
// Interior columns themselves carry the authoritative state).
func writeInteriorStreamFrame(out io.Writer, buildFrame func(tick uint32, present []uint8, value []int32, ox, oy, oz []float32, events []RowEvent) []byte, tick uint32, present []uint8, value []int32, ox, oy, oz []float32, events []RowEvent) {
	if out == nil || buildFrame == nil {
		return
	}
	frame := buildFrame(tick, present, value, ox, oy, oz, events)
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(frame)))
	// Fire-and-forget, same reasoning as SnapshotState.writeFrame: no delivery
	// guarantee on this channel, errors ignored.
	_, _ = out.Write(hdr[:])
	_, _ = out.Write(frame)
}

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
func emitNodeBeads(tr *T.Trace, nodeName string, working, backup []int, stream *interiorStream) {
	const cols = 2
	present := make([]uint8, 0, 4)
	value := make([]int32, 0, 4)
	ox, oy, oz := make([]float32, 0, 4), make([]float32, 0, 4), make([]float32, 0, 4)
	nodeRow := int32(-1)
	if stream != nil {
		nodeRow = stream.nodeRow
	}
	var events []RowEvent
	emitRow := func(row int, slice []int) {
		for col := 0; col < cols; col++ {
			p := interiorSlotOffset(row, col)
			has := col < len(slice)
			v := 0
			if has {
				v = slice[col]
			}
			tr.NodeBead(nodeName, row, col, has, v, p.X, p.Y, p.Z)
			events = append(events, RowEvent{
				Kind: T.KindNodeBead, NodeRow: nodeRow, Slot: int32(row*2 + col), Value: int32(v),
				PortRow: -1, TargetRow: -1, TargetPortRow: -1, EdgeRow: -1,
				X: p.X, Y: p.Y, Z: p.Z,
			})
			present = append(present, boolU8(has))
			value = append(value, int32(v))
			ox, oy, oz = append(ox, float32(p.X)), append(oy, float32(p.Y)), append(oz, float32(p.Z))
		}
	}
	emitRow(0, backup)  // top row = backup
	emitRow(1, working) // bottom row = working
	stream.write(present, value, ox, oy, oz, events)
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
func emitHeldBead(tr *T.Trace, nodeName string, held int, stream *interiorStream) {
	has := held != NoValue
	tr.NodeBead(nodeName, 0, 0, has, held, 0, 0, 0)
	// Only slot (0,0) is meaningful for a HoldNewSendOld node; the remaining 3 fixed
	// slots stay absent, matching the fd-3 Interior block's convention for this kind
	// (writeInteriorBlock reads n.interior[slot], and only slot 0 was ever set here).
	v := 0
	if has {
		v = held
	}
	nodeRow := int32(-1)
	if stream != nil {
		nodeRow = stream.nodeRow
	}
	events := []RowEvent{{
		Kind: T.KindNodeBead, NodeRow: nodeRow, Slot: 0, Value: int32(v),
		PortRow: -1, TargetRow: -1, TargetPortRow: -1, EdgeRow: -1,
	}}
	stream.write(
		[]uint8{boolU8(has), 0, 0, 0},
		[]int32{int32(v), 0, 0, 0},
		[]float32{0, 0, 0, 0}, []float32{0, 0, 0, 0}, []float32{0, 0, 0, 0},
		events,
	)
}

// emitInputBeads streams a gate's two held inputs as interior beads: the LEFT
// input on the left of the node (negative x), the RIGHT input on the right
// (positive x), vertically centered. NoValue = not held → present=false. Slot
// keys (0,0)=left, (0,1)=right. Offsets use interiorSlot so they sit inside the
// sphere.
func emitInputBeads(tr *T.Trace, nodeName string, left, right int, stream *interiorStream) {
	s := interiorSlot
	hasL, hasR := left != NoValue, right != NoValue
	tr.NodeBead(nodeName, 0, 0, hasL, left, -s, 0, 0)
	tr.NodeBead(nodeName, 0, 1, hasR, right, s, 0, 0)
	vL, vR := 0, 0
	if hasL {
		vL = left
	}
	if hasR {
		vR = right
	}
	nodeRow := int32(-1)
	if stream != nil {
		nodeRow = stream.nodeRow
	}
	events := []RowEvent{
		{Kind: T.KindNodeBead, NodeRow: nodeRow, Slot: 0, Value: int32(vL), PortRow: -1, TargetRow: -1, TargetPortRow: -1, EdgeRow: -1, X: -s},
		{Kind: T.KindNodeBead, NodeRow: nodeRow, Slot: 1, Value: int32(vR), PortRow: -1, TargetRow: -1, TargetPortRow: -1, EdgeRow: -1, X: s},
	}
	stream.write(
		[]uint8{boolU8(hasL), boolU8(hasR), 0, 0},
		[]int32{int32(vL), int32(vR), 0, 0},
		[]float32{float32(-s), float32(s), 0, 0}, []float32{0, 0, 0, 0}, []float32{0, 0, 0, 0},
		events,
	)
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
//
// speedCh is the SAME per-goroutine speed channel the caller's own paced loop
// polls (per-goroutine-clock.md "Delivery"). This loop is a SEPARATE blocking
// loop from the caller's (it is not just one iteration of the caller's flat
// loop — it runs its own SleepCycle cycles until the slide lands), so it must
// poll ApplySpeedNonBlocking itself each cycle; without this a speed change
// sent mid-slide sits unapplied in the channel until the slide finishes and
// the caller's own loop resumes and drains it one cycle later — the in-node
// animation would run at the OLD speed for its entire duration regardless of
// the slider (the bug this fixes).
func emitRefillSlide(ctx context.Context, tr *T.Trace, nodeName string, clk Clock, speedCh <-chan float64, beads []int) {
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
		ApplySpeedNonBlocking(clk, speedCh)
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
