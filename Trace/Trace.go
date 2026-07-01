// Phase 7 Chunk 3 — runtime trace recorder.
//
// One Trace value is shared across all nodes; each node holds it as
// a *Trace field, injected at build time by Wiring.reflectBuild.
// Nodes call Emit at three points: on a successful channel receive
// (recv), before fanning out an emission (fire), and after each
// successful send. All events serialize through a single
// channel; a drain goroutine assigns the monotonic Step ordinal and
// appends to the slice — the order events arrive at the channel is
// the causal-enough story for replay (per trace-replay-plan.md).
//
// Wire format note: this package emits `send` events keyed by
// (Node, Port), NOT by edge ID. Edge IDs are a Wiring/spec-level
// concept the node doesn't have. The on-disk Go trace is raw form.

package Trace

import (
	"encoding/json"
	"io"
	"sync"
)

// Closed event vocabulary. Mirrors src/sim/trace.ts but keeps Port
// for both recv (input port) and send (output port). Node is always
// the emitting node — the one that received the value (recv) or sent
// it (send/fire).
const (
	KindRecv = "recv"
	KindFire = "fire"
	KindSend = "send"
	KindDone = "done"
	// KindPosition is the per-frame bead-position event (Phase 2). The wire's
	// delivery goroutine emits one every ~16 ms while a bead is in flight,
	// carrying the bead's evaluated 3-D position so the renderer plots it without
	// computing geometry itself.
	KindPosition = "edge-bead"
	// KindGeometry carries an edge's authoritative straight-segment endpoints
	// (Phase 3). Go owns node positions + per-edge segments; it emits one geometry
	// event per edge on load and again whenever a node-move re-derives that edge's
	// segment, so the renderer draws the wire tube from Go's endpoints and
	// computes no geometry of its own. Keyed by edge label (== the TS edge id).
	KindGeometry = "geometry"
	// KindPulseCancelled tells the renderer to remove an in-flight bead's sprite
	// (Phase 3). Go emits it when a wire drops a bead mid-flight (edge deleted while
	// the bead was traversing). Keyed by the bead's SOURCE node+port — the same
	// routing key as send/position — so the renderer drops the sprite by source.
	KindPulseCancelled = "pulse-cancelled"
	// KindNodeGeometry carries one node's authoritative center + per-port world
	// positions/directions (item 1). Each node's goroutine emits this once on
	// startup via its injected EmitGeometry closure — the node owns its own
	// geometry emission (wires still own bead-position emission). Keyed by node id.
	KindNodeGeometry = "node-geometry"
	// KindArrive marks a bead COMPLETING its traversal on a wire — the bead has
	// reached the destination port and is delivered into the slot. The wire emits
	// it from deliverLocked (the single delivery path), keyed by the bead's SOURCE
	// node+port — the same routing key as send/position/pulse-cancelled — so the
	// renderer clears the transit pulse the instant the bead arrives. This is
	// DISTINCT from "done" (the consumer finished USING the held value): the transit
	// pulse represents a bead in flight and must vanish on arrival, not linger at the
	// port until the node's firing rule consumes the held value.
	KindArrive = "arrive"
	// KindNodeBead carries one INTERIOR slot's authoritative grid-slot state
	// (node 1's depleting/refilling buffer). Node 1's Update computes the 2x2 grid
	// slot positions coupled to the working/backup array mutation and emits a 4-slot
	// SNAPSHOT (one node-bead event per slot) via its injected EmitNodeBeads closure
	// on each array change. Keyed by node id + (row,col): row 0 = top/backup, row 1 =
	// bottom/working; col is the index within that row. Payload = present (filled?) +
	// value (0|1) + world position (x,y,z). A popped slot is emitted with present=false
	// so TS clears it (absence can't be rendered, presence can).
	// Discrete positions only this phase — beads snap to their slots (no slide yet).
	KindNodeBead = "node-bead"
	// KindCamera carries the polar camera viewpoint state on camera events. Go emits
	// it whenever the camera is set, orbited, zoomed, or panned, so the renderer can
	// reconstruct the camera pose without computing any geometry itself. Fields:
	// px/py/pz = pivot world position; r = orbit radius; posTheta/posPhi = pivot→camera
	// direction (spherical); upTheta/upPhi = up-hint direction (spherical).
	KindCamera = "camera"
	// KindSceneTori carries the polar-guide tori visibility state. Go emits it when
	// the tori visibility is toggled (op="tori-vis"), so the renderer shows or hides
	// the two polar tori in NavGuides without computing any geometry.
	KindSceneTori = "scene-tori"
	// KindScenePoles carries the scene-center pole frame visibility state. Go emits it
	// when the visibility is toggled (op="scene-poles"), so the renderer shows or hides
	// the scene-center PolarFrame without computing any geometry.
	KindScenePoles = "scene-poles"
	// KindNodePoles carries the per-node pole frame visibility state. Go emits it when
	// the visibility is toggled (op="node-poles"), so the renderer shows or hides
	// PolarFrames drawn at every node sphere.
	KindNodePoles = "node-poles"
	// KindAngleLabels carries the θ/φ angle arc+label visibility state. Go emits it when
	// the visibility is toggled (op="angle-labels"), so the renderer shows or hides the
	// ThetaArc/PhiArc overlays in NavGuides.
	KindAngleLabels = "angle-labels"
	// KindSelSpherePoles carries the selection-sphere pole axis visibility state. Go emits it when
	// the visibility is toggled (op="sel-sphere-poles"), so the renderer shows or hides the
	// selection sphere pole markers in NavGuides.
	KindSelSpherePoles = "sel-sphere-poles"
	// KindHandholds carries the rotation-handhold grab-sphere visibility state. Go emits it
	// when the visibility is toggled (op="handholds-vis"), so the renderer shows or hides
	// the 4 grab spheres per torus in NavGuides without computing any geometry.
	KindHandholds = "handholds"
	// KindLabelsGlobal carries the global node-label visibility state. Go emits it when
	// the visibility is toggled (op="labels-vis"), so the renderer shows or hides
	// all node labels in ThreeView without computing any geometry.
	KindLabelsGlobal = "labels-global"
	// KindBadgesGlobal carries the global occlusion-badge visibility state. Go emits it when
	// the visibility is toggled (op="badges-vis"), so the renderer shows or hides
	// all occlusion +N badges in ThreeView without computing any geometry.
	KindBadgesGlobal = "badges-global"
	// KindOverlaysVis carries the master overlays visibility state. Go emits it when
	// the master toggle is triggered (op="overlays-vis"), so the renderer shows or hides
	// all 8 overlays at once without mutating individual overlay bools.
	KindOverlaysVis = "overlays-vis"
	// KindDoubleLinks carries the double-link overlay visibility state. Go emits it when
	// the toggle is triggered (op="double-links"), so the renderer draws bidirectional
	// arrow overlays on edges and dims the edge tubes.
	KindDoubleLinks = "double-links"
	// KindNodeStatus carries a node's processing-status state: whether its torus is in
	// the RED error state and, when red, the "missed" bead's value plus a world position
	// just OUTSIDE the node so the renderer can show the discarded different-color bead.
	// Go emits torusRed=true (with the missed value+pos) when a different-color bead
	// arrives on an input port mid-processing, and torusRed=false (revert to normal,
	// clearing the missed marker) when the node finishes processing. Keyed by node id.
	// This is Go REPORTING what it does — the renderer plots it later (no rendering yet).
	KindNodeStatus = "node-status"
)

// TraceEventKinds is the single source of truth for the closed kind
// vocabulary. gen-node-defs reads this slice to emit trace-kinds.ts;
// pump.ts exhaustiveness checks are derived from that generated file.
// Adding a kind here forces a tsc error in pump.ts until a branch is added.
var TraceEventKinds = []string{KindRecv, KindFire, KindSend, KindDone, KindPosition, KindGeometry, KindPulseCancelled, KindNodeGeometry, KindArrive, KindNodeBead, KindCamera, KindSceneTori, KindScenePoles, KindNodePoles, KindAngleLabels, KindSelSpherePoles, KindHandholds, KindLabelsGlobal, KindBadgesGlobal, KindOverlaysVis, KindDoubleLinks, KindNodeStatus}

// PortGeom is one port's authoritative world geometry on a node-geometry event:
// its name, whether it is an input, its sphere-surface world position (PX/PY/PZ),
// and the unit direction from node center toward the port (DX/DY/DZ).
type PortGeom struct {
	Name       string
	IsInput    bool
	PX, PY, PZ float64
	DX, DY, DZ float64
}

type Event struct {
	Step  int    `json:"step"`
	Kind  string `json:"kind"`
	Node  string `json:"node"`
	Port  string `json:"port,omitempty"`  // recv: input port; send: output port
	Value int    `json:"value,omitempty"` // recv/send only
	// Bead is the per-wire monotonic bead id (paced_wire.go gen). Set on the four
	// wire-bead events (send, edge-bead/position, arrive, pulse-cancelled) so the
	// renderer keys each in-flight bead independently and a wire can show N beads at
	// once. Bead ids are 1-based (nextGen increments before it is read, so the first
	// bead is gen 1); 0 never occurs, so omitempty safely marks "no bead" — the four
	// wire-bead kinds always carry a real id ≥1, every other kind omits it (TS reads
	// `bead ?? 0`).
	Bead uint64 `json:"bead,omitempty"`
	// Wire geometry fields — populated on send events when the outgoing port
	// is backed by a PacedWire. Zero values are omitted from JSON output.
	// ArriveStep is omitted: Go has no global ms-per-step cadence,
	// so the TS layer derives arrival from emitTime + simLatencyMs instead.
	ArcLength    float64 `json:"arcLength,omitempty"`
	SimLatencyMs float64 `json:"simLatencyMs,omitempty"`
	// Destination slot identity — authoritative from Go. Set on send events backed
	// by a PacedWire so the TS layer never derives targetHandle from edge data.
	Target       string `json:"target,omitempty"`
	TargetHandle string `json:"targetHandle,omitempty"`
	// X/Y/Z carry the bead's evaluated 3-D world position on position events
	// (KindPosition). Go computes the position from the bead's curve + clock; the
	// renderer plots these directly. hasPos distinguishes a real (possibly 0,0,0)
	// position from an unset one so marshalEvent always emits all three.
	X, Y, Z float64
	hasPos  bool
	// F carries the bead's FRACTIONAL progress t along its wire (0..1) on position
	// events. Go owns progress (timing/clock); the editor places the bead in space
	// at lerp(liveStart, liveEnd, F) on its LOCAL (dragged) node port positions, so
	// the bead rides the live wire with no round-trip lag and no t-swing race.
	F float64
	// Edge carries the edge's id on geometry events (KindGeometry).
	// This is the TS edge id — the renderer routes the segment to the right wire.
	// Not set on any other event kind.
	Edge string `json:"edge,omitempty"`
	// SX/SY/SZ and EX/EY/EZ carry an edge's authoritative straight-segment endpoints
	// on geometry events (KindGeometry): Start = source OUT-port world pos,
	// End = dest IN-port world pos. Go owns these; the renderer draws the wire tube
	// from them as a LineCurve3. Set on geometry events only (keyed by Edge).
	SX, SY, SZ float64
	EX, EY, EZ float64
	// NX/NY/NZ carry the node's center world position on node-geometry events
	// (KindNodeGeometry), and Ports carries that node's per-port world geometry.
	// Keyed by Node (the node id). Set on node-geometry events only.
	NX, NY, NZ float64
	Ports      []PortGeom
	// Radius carries the node body/ring sphere radius on node-geometry events
	// (KindNodeGeometry) — Go-owned (min(w,h)/CurveParamNodeRadiusDivisor). The
	// renderer reads it for the body/ring instead of recomputing from node dims.
	Radius float64
	// SphereR carries the node's sphere-chain radius on node-geometry events
	// (KindNodeGeometry) — the radius used for bead-chain orbit and port placement
	// (nodeR in port_geometry.go). Distinct from Radius (the node body/ring sphere).
	SphereR float64 `json:"sphereR,omitempty"`
	// VRX/VRY/VRZ carry the vertical great-circle ring normal on node-geometry events
	// (KindNodeGeometry). Go owns these constants so TS never hardcodes ring orientation.
	VRX, VRY, VRZ float64
	// FRX/FRY/FRZ carry the flat (equatorial) great-circle ring normal on node-geometry
	// events (KindNodeGeometry). Companion to VRX/VRY/VRZ above.
	FRX, FRY, FRZ float64
	// PX/PY/PZ carry the camera pivot world position on camera events (KindCamera).
	// R is the orbit radius; PosTheta/PosPhi are the pivot→camera direction (spherical);
	// UpTheta/UpPhi are the up-hint direction (spherical). Set on camera events only.
	PX, PY, PZ       float64
	R                float64
	PosTheta, PosPhi float64
	UpTheta, UpPhi   float64
	// Row/Col identify an interior bead's grid slot on node-bead events
	// (KindNodeBead): Row 0 = top/backup, Row 1 = bottom/working; Col is the
	// position in that row's slice. Keyed by Node + (Row,Col). X/Y/Z carry the
	// slot's world position and Value the bead value (0|1). Set on node-bead only.
	// Present marks whether the slot is FILLED: a node-bead snapshot emits ALL 4
	// slots on each array change — Present=true (with Value+position) for filled
	// slots, Present=false for empty (popped) slots so TS can clear them.
	Row, Col int
	Present  bool
	// Visible carries the tori visibility state on scene-tori events (KindSceneTori).
	// true = tori shown; false = tori hidden. Set on scene-tori events only.
	Visible bool `json:"visible"`
	// TorusRed carries the processing-error state on node-status events (KindNodeStatus):
	// true = torus RED (a different-color bead was missed mid-processing; Value carries the
	// missed bead's value and X/Y/Z a world position just outside the node); false = revert
	// to normal (missed marker cleared). Set on node-status events only.
	TorusRed bool `json:"torusRed"`
}

// Trace is the shared recorder. Construct with New; injected into
// each node's Trace field by Wiring.reflectBuild. Call Close after
// all nodes have stopped to drain
// the channel and receive the final event slice via Events().
type Trace struct {
	ch      chan Event
	done    chan struct{}
	stopped chan struct{} // closed by Close() to signal senders to stop; ch is NEVER closed
	mu      sync.Mutex
	events  []Event
	closed  bool
	sink    io.Writer // if non-nil, each event is written as JSONL in real time
}

// New allocates a Trace with a buffered emit channel. buf controls
// how much burst the recorder absorbs before Emit blocks. 1024 is
// plenty for the current topology sizes; bump if Emit is observed
// to back-pressure node loops.
func New(buf int) *Trace {
	return NewWithSink(buf, nil)
}

// NewWithSink is like New but writes each event as JSONL to sink in
// real time (inside the drain goroutine) in addition to buffering.
// Pass nil for sink to disable streaming (identical to New).
func NewWithSink(buf int, sink io.Writer) *Trace {
	if buf <= 0 {
		buf = 1024
	}
	t := &Trace{
		ch:      make(chan Event, buf),
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
		sink:    sink,
	}
	go t.drain()
	return t
}

// emit is the single send path. It NEVER sends on a closed channel because
// t.ch is never closed; instead Close() closes t.stopped and a concurrent
// sender that is mid-flight selects the stopped case and drops the event
// silently rather than send on a torn-down trace.
func (t *Trace) emit(e Event) {
	if t == nil {
		return
	}
	select {
	case t.ch <- e:
	case <-t.stopped:
	}
}

// Emit sends one event. Called from node Update loops — always check
// t != nil at the call site so untraced runs are zero-cost beyond a
// nil check. Blocks if the buffer is full (per trace-replay-plan §
// "Backpressure: buffered recorder channel; if full, log a warning
// and block briefly rather than drop"). The 1024-deep default keeps
// this rare in practice.
func (t *Trace) Emit(e Event) {
	t.emit(e)
}

// Recv emits a recv event for `(node, port, value)`. Convenience
// wrapper so node code stays one-line.
func (t *Trace) Recv(node, port string, value int) {
	t.emit(Event{Kind: KindRecv, Node: node, Port: port, Value: value})
}

// Fire emits a fire event for `node`. Called once per handler
// activation that produces ≥1 emission, before the first Send.
func (t *Trace) Fire(node string) {
	t.emit(Event{Kind: KindFire, Node: node})
}

// Send emits a send event for `(node, port, value)` after a
// successful S.Send on the corresponding output channel.
func (t *Trace) Send(node, port string, value int) {
	t.emit(Event{Kind: KindSend, Node: node, Port: port, Value: value})
}

// SendWire emits a send event like Send, additionally carrying the wire geometry
// fields (arcLength in world-units, simLatencyMs in milliseconds) and the
// authoritative destination slot identity (target node id, targetHandle port name)
// from the outgoing PacedWire. Pass zero values when the port is not backed by a PacedWire.
func (t *Trace) SendWire(node, port string, value int, arcLength, simLatencyMs float64, target, targetHandle string) {
	t.emit(Event{Kind: KindSend, Node: node, Port: port, Value: value, ArcLength: arcLength, SimLatencyMs: simLatencyMs, Target: target, TargetHandle: targetHandle})
}

// Done emits a done event for `(node, port)` when the receiver has finished
// using a value. The node and port identify the input port that was Done'd,
// matching the edge by target node + targetHandle in the webview.
func (t *Trace) Done(node, port string) {
	t.emit(Event{Kind: KindDone, Node: node, Port: port})
}

// Position emits a per-frame bead-position event (Phase 2). node/port are the
// SOURCE node id + output port — the same identity carried by the send event, so
// the renderer routes the position to the right edge(s) by source+sourceHandle
// (fan-out). value echoes the bead value; x/y/z is the bead's evaluated 3-D world
// position on its own edge curve; f is the bead's FRACTIONAL progress t (0..1)
// along the wire, which the editor uses to place the bead on its LOCAL (dragged)
// node port positions (Go owns progress, the editor owns live placement). The
// wire's delivery goroutine calls this every ~16 ms while the bead is in flight,
// and once more at t==1 just before delivery.
// bead is the per-wire bead id (paced_wire.go gen): a wire may carry N beads at
// once (a clock-paced train), so the renderer keys each in-flight bead by it.
func (t *Trace) Position(node, port string, value int, x, y, z, f float64, bead uint64) {
	t.emit(Event{Kind: KindPosition, Node: node, Port: port, Value: value, X: x, Y: y, Z: z, hasPos: true, F: f, Bead: bead})
}

// Geometry emits an edge's authoritative straight-segment endpoints (Phase 3),
// keyed by edge label (== the TS edge id). (sx,sy,sz) is the source OUT-port world
// pos (Start), (ex,ey,ez) is the dest IN-port world pos (End). Go emits this on load
// and on each node-move; the renderer draws the wire tube as a LineCurve3 from these.
func (t *Trace) Geometry(edge string, sx, sy, sz, ex, ey, ez float64) {
	t.emit(Event{
		Kind: KindGeometry, Edge: edge,
		SX: sx, SY: sy, SZ: sz,
		EX: ex, EY: ey, EZ: ez,
	})
}

// NodeGeometry emits one node's authoritative center + per-port world geometry,
// plus the two great-circle ring normals (vertical + flat) owned by Go.
// (item 1), keyed by node id. cx/cy/cz is the node center world position; ports
// carries each port's world position + direction. Each node's goroutine calls this
// once on startup via its injected EmitGeometry closure (the node owns its geometry
// emission; wires still own bead-position emission).
func (t *Trace) NodeGeometry(nodeID string, cx, cy, cz, radius, sphereR float64, ports []PortGeom, vrx, vry, vrz, frx, fry, frz float64) {
	t.emit(Event{Kind: KindNodeGeometry, Node: nodeID, NX: cx, NY: cy, NZ: cz, Radius: radius, SphereR: sphereR, Ports: ports,
		VRX: vrx, VRY: vry, VRZ: vrz, FRX: frx, FRY: fry, FRZ: frz})
}

// Arrive marks a bead completing its traversal — delivered into the destination
// slot. Keyed by the bead's SOURCE node+port (the same routing key as send/
// position/pulse-cancelled), so the renderer clears the transit pulse on arrival.
// The wire's deliverLocked is the single caller; it fires exactly once per bead.
func (t *Trace) Arrive(node, port string, value int, bead uint64) {
	t.emit(Event{Kind: KindArrive, Node: node, Port: port, Value: value, Bead: bead})
}

// NodeBead emits one interior grid SLOT's authoritative state (node 1's 2x2
// buffer), keyed by node id + (row,col): row 0 = top/backup, row 1 =
// bottom/working; col is the index within that row. present marks whether the slot
// is filled; value is the bead value (0|1) and x/y/z the slot's world position
// (meaningful when present). Node 1's Update calls this for ALL 4 slots whenever the
// working/backup arrays change (the seed pop, each feedback pop, each refill) — a
// 4-slot snapshot, with empty slots carrying present=false so TS clears them.
// Discrete positions only — beads snap to their slots; no slide interpolation yet.
func (t *Trace) NodeBead(nodeID string, row, col int, present bool, value int, x, y, z float64) {
	t.emit(Event{Kind: KindNodeBead, Node: nodeID, Row: row, Col: col, Present: present, Value: value, X: x, Y: y, Z: z, hasPos: true})
}

// Camera emits the polar camera viewpoint state: pivot world position (px,py,pz),
// orbit radius r, pivot→camera direction (posTheta,posPhi), and up-hint direction
// (upTheta,upPhi). Go emits this whenever the camera is set, orbited, zoomed, or panned.
func (t *Trace) Camera(px, py, pz, r, posTheta, posPhi, upTheta, upPhi float64) {
	t.emit(Event{Kind: KindCamera, PX: px, PY: py, PZ: pz, R: r, PosTheta: posTheta, PosPhi: posPhi, UpTheta: upTheta, UpPhi: upPhi})
}

// SceneTori emits the polar-guide tori visibility state. visible=true = tori shown;
// visible=false = tori hidden. Go emits this on op="tori-vis" so the renderer
// shows/hides the two polar tori in NavGuides without computing any geometry.
func (t *Trace) SceneTori(visible bool) {
	t.emit(Event{Kind: KindSceneTori, Visible: visible})
}

// ScenePoles emits the scene-center pole frame visibility state. visible=true = shown;
// visible=false = hidden. Go emits this on op="scene-poles".
func (t *Trace) ScenePoles(visible bool) {
	t.emit(Event{Kind: KindScenePoles, Visible: visible})
}

// NodePoles emits the per-node pole frame visibility state. visible=true = shown;
// visible=false = hidden. Go emits this on op="node-poles".
func (t *Trace) NodePoles(visible bool) {
	t.emit(Event{Kind: KindNodePoles, Visible: visible})
}

// AngleLabels emits the θ/φ angle arc+label visibility state. visible=true = shown;
// visible=false = arcs hidden. Go emits this on op="angle-labels".
func (t *Trace) AngleLabels(visible bool) {
	t.emit(Event{Kind: KindAngleLabels, Visible: visible})
}

// SelSpherePoles emits the selection-sphere pole axis visibility state. visible=true = shown;
// visible=false = hidden. Go emits this on op="sel-sphere-poles".
func (t *Trace) SelSpherePoles(visible bool) {
	t.emit(Event{Kind: KindSelSpherePoles, Visible: visible})
}

// Handholds emits the rotation-handhold grab-sphere visibility state. visible=true = shown;
// visible=false = hidden. Go emits this on op="handholds-vis".
func (t *Trace) Handholds(visible bool) {
	t.emit(Event{Kind: KindHandholds, Visible: visible})
}

// LabelsGlobal emits the global node-label visibility state. visible=true = labels shown;
// visible=false = labels hidden. Go emits this on op="labels-vis" so the renderer
// shows/hides all node labels in ThreeView without computing any geometry.
func (t *Trace) LabelsGlobal(visible bool) {
	t.emit(Event{Kind: KindLabelsGlobal, Visible: visible})
}

// BadgesGlobal emits the global occlusion-badge visibility state. visible=true = badges shown;
// visible=false = badges hidden. Go emits this on op="badges-vis" so the renderer
// shows/hides all +N badges in ThreeView without computing any geometry.
func (t *Trace) BadgesGlobal(visible bool) {
	t.emit(Event{Kind: KindBadgesGlobal, Visible: visible})
}

// DoubleLinks emits the double-link overlay visibility state. visible=true = overlay shown;
// visible=false = overlay hidden. Written by pump to useCameraStore.doubleLinksVisible.
func (t *Trace) DoubleLinks(visible bool) {
	t.emit(Event{Kind: KindDoubleLinks, Visible: visible})
}

// OverlaysVis emits the master overlays visibility state. visible=true = all overlays shown;
// visible=false = all overlays hidden. Go emits this on op="overlays-vis" so the renderer
// shows/hides all 8 overlays at once without mutating individual overlay bools.
func (t *Trace) OverlaysVis(visible bool) {
	t.emit(Event{Kind: KindOverlaysVis, Visible: visible})
}

// NodeStatus emits a node's processing-status state (KindNodeStatus), keyed by node id.
// torusRed=true marks the node's torus RED for an error: a different-color bead arrived on
// an input port while the node was processing; missedValue is that discarded bead's value
// and (x,y,z) a world position just OUTSIDE the node so the renderer can show it.
// torusRed=false reverts the torus to normal and clears the missed marker (emitted when the
// node finishes processing). Go REPORTS this; the renderer plots it later.
func (t *Trace) NodeStatus(node string, torusRed bool, missedValue int, x, y, z float64) {
	t.emit(Event{Kind: KindNodeStatus, Node: node, TorusRed: torusRed, Value: missedValue, X: x, Y: y, Z: z, hasPos: true})
}

// PulseCancelled tells the renderer to drop an in-flight bead's sprite (Phase 3),
// keyed by the bead's SOURCE node+port (the same routing key as send/position). Go
// emits it when a wire drops a bead mid-flight (edge deleted during traversal).
func (t *Trace) PulseCancelled(node, port string, value int, bead uint64) {
	t.emit(Event{Kind: KindPulseCancelled, Node: node, Port: port, Value: value, Bead: bead})
}

// Breadcrumb writes a free-form diagnostic line directly to the sink
// (if any) in real time. It is logging-only: breadcrumbs are NOT added
// to the buffered event slice, do NOT receive a Step ordinal, and are
// outside the closed trace vocabulary used for replay/parity. The line
// shape is {"src":"go","kind":"breadcrumb","label":...,"node":...,
// "port":...,"value":...} with empty/zero fields omitted, so the TS
// relay can route it to go.jsonl alongside real trace events.
//
// Used to trace one-off control events (e.g. edge delete / wire reset)
// that have no place in the recv/fire/send/done lifecycle.
func (t *Trace) Breadcrumb(label, node, port, value string) {
	if t == nil || t.sink == nil {
		return
	}
	b, err := marshalBreadcrumb(label, node, port, value)
	if err != nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = t.sink.Write(b)
	_, _ = t.sink.Write([]byte{'\n'})
}

func marshalBreadcrumb(label, node, port, value string) ([]byte, error) {
	type breadcrumb struct {
		Kind  string `json:"kind"`
		Label string `json:"label"`
		Node  string `json:"node,omitempty"`
		Port  string `json:"port,omitempty"`
		Value string `json:"value,omitempty"`
	}
	return json.Marshal(breadcrumb{Kind: "breadcrumb", Label: label, Node: node, Port: port, Value: value})
}

// Close stops the drain goroutine. Call after every node's Update
// has returned (sync.WaitGroup.Wait in main). Idempotent.
func (t *Trace) Close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	// Signal senders to stop. t.ch is NEVER closed, so an in-flight emit
	// selects the stopped case and drops rather than panicking.
	close(t.stopped)
	t.mu.Unlock()
	// drain() observes t.stopped, flushes any buffered events, then closes done.
	<-t.done
}

// Events returns a snapshot of the recorded sequence. Safe to call
// after Close; calling before Close races with the drain goroutine.
func (t *Trace) Events() []Event {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Event, len(t.events))
	copy(out, t.events)
	return out
}

// WriteJSONL serializes all recorded events as JSON-lines (one
// object per line, trailing newline) onto w. Emits raw form: send
// events carry node+port. Call after Close.
func (t *Trace) WriteJSONL(w io.Writer) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return writeAll(t.events, w, marshalEvent)
}

func writeAll(events []Event, w io.Writer, marshal func(Event) ([]byte, error)) error {
	for _, e := range events {
		b, err := marshal(e)
		if err != nil {
			return err
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
		if _, err := w.Write([]byte{'\n'}); err != nil {
			return err
		}
	}
	return nil
}

func (t *Trace) drain() {
	record := func(ev Event) {
		// Hold the lock ACROSS the sink write. Breadcrumb() and Close() also write to
		// the sink under t.mu; if we unlocked before writing (as before), an event's
		// two writes (body, '\n') could interleave with a breadcrumb's writes, fusing
		// two JSON objects onto one line. That garbled the stdout stream — the extension's
		// line parser then rejected the fused line and dumped it to the "topology run"
		// Output channel, and the pump could misparse events. Serialized writes keep every
		// line a single newline-delimited JSON object.
		t.mu.Lock()
		ev.Step = len(t.events)
		t.events = append(t.events, ev)
		if t.sink != nil {
			if b, err := marshalEvent(ev); err == nil {
				_, _ = t.sink.Write(b)
				_, _ = t.sink.Write([]byte{'\n'})
			}
		}
		t.mu.Unlock()
	}
	for {
		select {
		case ev := <-t.ch:
			record(ev)
		case <-t.stopped:
			// Close() signalled. Drain any events already buffered in t.ch
			// (senders may have enqueued before observing stopped), then finish.
			for {
				select {
				case ev := <-t.ch:
					record(ev)
				default:
					close(t.done)
					return
				}
			}
		}
	}
}

// marshalEvent emits the closed-vocabulary shape:
//
//	{"step":N,"kind":"recv","node":"X","port":"Y","value":V}
//	{"step":N,"kind":"fire","node":"X"}
//	{"step":N,"kind":"send","node":"X","port":"Y","value":V}
//
// json.Marshal with `omitempty` would drop value=0 and port="";
// neither is correct (value 0 is a valid signal in this codebase, and
// a missing port on recv/send is a bug worth surfacing). Hand-roll
// to keep the shape stable.
//
// Perf note: this allocates a fresh []byte per event (incl. the high-volume
// KindPosition stream). A reusable bytes.Buffer / json.Encoder could avoid the
// per-call alloc, but Encoder differs from json.Marshal in HTML-escaping and
// trailing-newline handling, so swapping it risks changing the emitted bytes —
// not worth it at current trace volumes on this off-hot-path serializer. Left as-is.
func marshalEvent(e Event) ([]byte, error) {
	type recvOrSend struct {
		Step  int    `json:"step"`
		Kind  string `json:"kind"`
		Node  string `json:"node"`
		Port  string `json:"port"`
		Value int    `json:"value"`
	}
	type sendWire struct {
		Step         int     `json:"step"`
		Kind         string  `json:"kind"`
		Node         string  `json:"node"`
		Port         string  `json:"port"`
		Value        int     `json:"value"`
		ArcLength    float64 `json:"arcLength,omitempty"`
		SimLatencyMs float64 `json:"simLatencyMs,omitempty"`
		Target       string  `json:"target,omitempty"`
		TargetHandle string  `json:"targetHandle,omitempty"`
	}
	type fire struct {
		Step int    `json:"step"`
		Kind string `json:"kind"`
		Node string `json:"node"`
	}
	type doneEvent struct {
		Step int    `json:"step"`
		Kind string `json:"kind"`
		Node string `json:"node"`
		Port string `json:"port"`
	}
	type position struct {
		Step  int     `json:"step"`
		Kind  string  `json:"kind"`
		Node  string  `json:"node"`
		Port  string  `json:"port"`
		Value int     `json:"value"`
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
		Z     float64 `json:"z"`
		F     float64 `json:"f"`
		Bead  uint64  `json:"bead,omitempty"`
	}
	type geometry struct {
		Step int     `json:"step"`
		Kind string  `json:"kind"`
		Edge string  `json:"edge"`
		SX   float64 `json:"sx"`
		SY   float64 `json:"sy"`
		SZ   float64 `json:"sz"`
		EX   float64 `json:"ex"`
		EY   float64 `json:"ey"`
		EZ   float64 `json:"ez"`
	}
	type pulseCancelled struct {
		Step  int    `json:"step"`
		Kind  string `json:"kind"`
		Node  string `json:"node"`
		Port  string `json:"port"`
		Value int    `json:"value"`
		Bead  uint64 `json:"bead,omitempty"`
	}
	type portGeomJSON struct {
		Name    string  `json:"name"`
		IsInput bool    `json:"isInput"`
		PX      float64 `json:"px"`
		PY      float64 `json:"py"`
		PZ      float64 `json:"pz"`
		DX      float64 `json:"dx"`
		DY      float64 `json:"dy"`
		DZ      float64 `json:"dz"`
	}
	type nodeGeometry struct {
		Step    int            `json:"step"`
		Kind    string         `json:"kind"`
		Node    string         `json:"node"`
		NX      float64        `json:"nx"`
		NY      float64        `json:"ny"`
		NZ      float64        `json:"nz"`
		Radius  float64        `json:"radius"`
		SphereR float64        `json:"sphereR,omitempty"`
		VRX     float64        `json:"vrx"`
		VRY     float64        `json:"vry"`
		VRZ     float64        `json:"vrz"`
		FRX     float64        `json:"frx"`
		FRY     float64        `json:"fry"`
		FRZ     float64        `json:"frz"`
		Ports   []portGeomJSON `json:"ports"`
	}
	type nodeBead struct {
		Step    int     `json:"step"`
		Kind    string  `json:"kind"`
		Node    string  `json:"node"`
		Row     int     `json:"row"`
		Col     int     `json:"col"`
		Present bool    `json:"present"`
		Value   int     `json:"value"`
		X       float64 `json:"x"`
		Y       float64 `json:"y"`
		Z       float64 `json:"z"`
	}
	switch e.Kind {
	case KindFire:
		return json.Marshal(fire{Step: e.Step, Kind: e.Kind, Node: e.Node})
	case KindSend:
		if e.ArcLength != 0 || e.SimLatencyMs != 0 {
			return json.Marshal(sendWire{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port, Value: e.Value, ArcLength: e.ArcLength, SimLatencyMs: e.SimLatencyMs, Target: e.Target, TargetHandle: e.TargetHandle})
		}
		return json.Marshal(recvOrSend{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port, Value: e.Value})
	case KindDone:
		return json.Marshal(doneEvent{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port})
	case KindPosition:
		// All three coordinates always emitted (0,0,0 is a valid position).
		return json.Marshal(position{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port, Value: e.Value, X: e.X, Y: e.Y, Z: e.Z, F: e.F, Bead: e.Bead})
	case KindGeometry:
		// All six segment-endpoint coordinates always emitted (0 is valid).
		return json.Marshal(geometry{Step: e.Step, Kind: e.Kind, Edge: e.Edge,
			SX: e.SX, SY: e.SY, SZ: e.SZ,
			EX: e.EX, EY: e.EY, EZ: e.EZ})
	case KindPulseCancelled:
		return json.Marshal(pulseCancelled{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port, Value: e.Value, Bead: e.Bead})
	case KindArrive:
		// Same wire shape as pulse-cancelled: source node+port+value+bead routing key.
		return json.Marshal(pulseCancelled{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port, Value: e.Value, Bead: e.Bead})
	case KindNodeGeometry:
		ports := make([]portGeomJSON, len(e.Ports))
		for i, p := range e.Ports {
			ports[i] = portGeomJSON{Name: p.Name, IsInput: p.IsInput, PX: p.PX, PY: p.PY, PZ: p.PZ, DX: p.DX, DY: p.DY, DZ: p.DZ}
		}
		return json.Marshal(nodeGeometry{Step: e.Step, Kind: e.Kind, Node: e.Node, NX: e.NX, NY: e.NY, NZ: e.NZ, Radius: e.Radius, SphereR: e.SphereR,
			VRX: e.VRX, VRY: e.VRY, VRZ: e.VRZ, FRX: e.FRX, FRY: e.FRY, FRZ: e.FRZ, Ports: ports})
	case KindNodeBead:
		// row/col/present/value/position always emitted (0/false is valid for each).
		return json.Marshal(nodeBead{Step: e.Step, Kind: e.Kind, Node: e.Node, Row: e.Row, Col: e.Col, Present: e.Present, Value: e.Value, X: e.X, Y: e.Y, Z: e.Z})
	case KindCamera:
		// All camera fields always emitted (0 is valid for any angle or position).
		type camera struct {
			Step     int     `json:"step"`
			Kind     string  `json:"kind"`
			PX       float64 `json:"px"`
			PY       float64 `json:"py"`
			PZ       float64 `json:"pz"`
			R        float64 `json:"r"`
			PosTheta float64 `json:"posTheta"`
			PosPhi   float64 `json:"posPhi"`
			UpTheta  float64 `json:"upTheta"`
			UpPhi    float64 `json:"upPhi"`
		}
		return json.Marshal(camera{Step: e.Step, Kind: e.Kind, PX: e.PX, PY: e.PY, PZ: e.PZ, R: e.R, PosTheta: e.PosTheta, PosPhi: e.PosPhi, UpTheta: e.UpTheta, UpPhi: e.UpPhi})
	case KindNodeStatus:
		// torusRed + missed bead value + outside world position always emitted.
		type nodeStatus struct {
			Step        int     `json:"step"`
			Kind        string  `json:"kind"`
			Node        string  `json:"node"`
			TorusRed    bool    `json:"torusRed"`
			MissedValue int     `json:"missedValue"`
			X           float64 `json:"x"`
			Y           float64 `json:"y"`
			Z           float64 `json:"z"`
		}
		return json.Marshal(nodeStatus{Step: e.Step, Kind: e.Kind, Node: e.Node, TorusRed: e.TorusRed, MissedValue: e.Value, X: e.X, Y: e.Y, Z: e.Z})
	case KindSceneTori, KindScenePoles, KindNodePoles, KindAngleLabels, KindSelSpherePoles, KindHandholds, KindLabelsGlobal, KindBadgesGlobal, KindOverlaysVis, KindDoubleLinks:
		// Visibility toggles: all carry just the Visible flag.
		type visToggle struct {
			Step    int    `json:"step"`
			Kind    string `json:"kind"`
			Visible bool   `json:"visible"`
		}
		return json.Marshal(visToggle{Step: e.Step, Kind: e.Kind, Visible: e.Visible})
	default:
		return json.Marshal(recvOrSend{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port, Value: e.Value})
	}
}
