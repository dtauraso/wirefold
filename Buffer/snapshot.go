// Buffer/snapshot.go — full-state column-block snapshot builder (Phase 2).
//
// SnapshotState accumulates world render state from trace events and produces
// framed binary snapshots on the position-emit cadence (~16 ms).
//
// Output channel: binary frames are written to a dedicated file descriptor
// (default fd 3, overridable via WIREFOLD_BUF_OUT_FD env var; set to "0" to
// disable). This is the SOLE framed channel: the JSON trace on stdout was
// already removed end-to-end (see main.go, Trace/Trace.go); there is no
// separate JSON stream and no pending migration.
//
// Frame format: [len:u32-LE][blockTag:u8][block bytes]  (len counts the tag byte PLUS
// the block bytes). blockTag is the discriminator defined in frame_tags.go. Four tags
// are emitted, every emitSnapshot call, as four SEPARATE frames:
//
//   - BufBlockTagScene: the combined snapshot below (layout-link/camera/overlay/scene/
//     event blocks — everything except beads, the node-owner-group blocks, and the
//     Edge block).
//   - BufBlockTagBead: the Bead block alone, in its own self-contained frame (built by
//     buildBeadFrame, Buffer/pack.go) — beads churn every tick independent of the rest
//     of scene state, so they no longer ride the scene frame. See frame_tags.go for its
//     byte layout.
//   - BufBlockTagNode: the Node/Interior/Port blocks + Label/PortName bytes, in their
//     own self-contained frame (built by buildNodeFrame, Buffer/pack.go) — these three
//     blocks share one owner group (the node movers), so they travel together, split
//     out of the scene frame the same way the Bead block was. See frame_tags.go for its
//     byte layout.
//   - BufBlockTagEdge: the Edge block + edge-label bytes, in its own self-contained
//     frame (built by buildEdgeFrame, Buffer/pack.go). The Edge block carries NO
//     endpoint coordinates — it references its two port rows (SrcPortRow/DstPortRow),
//     which resolve against the NODE frame's Port block — see bufLayoutEdge's doc
//     comment in layout.go for why (the endpoint-tear fix). See frame_tags.go for its
//     byte layout.
//
// This split is a step of eventually breaking the rest of this single buffer
// into N per-block buffers, each tagged with its own owner.
//
// Scene snapshot layout (little-endian, packed):
//
//	Header   12 bytes: [tick][eventCount][layoutLinkCount] (u32 each)
//	LayoutLink layoutLinkCount × BufLayoutLinkStride bytes (the LAYOUT double-link overlay pairs,
//	           from LocalPolars — NOT the Edge block; see bufLayoutLayoutLink in layout.go —
//	           SrcNodeRow/DstNodeRow resolve against the NODE frame's Node block, see
//	           frame_tags.go's BufBlockTagNode comment)
//	Camera   BufCameraStride bytes             (always 1 row)
//	Overlay  BufOverlayStride bytes            (always 1 row)
//	Scene    BufSceneStride bytes              (always 1 row; persisted scene-sphere center+radius)
//	Event    eventCount × BufEventStride bytes (per-tick causal trace events; .probe log only)
//
// This buffer is the sole framed channel Go emits (no pending rollout flip):
// the JSON trace was already removed, not deferred to a later phase.
//
// All SnapshotState methods must be called from a single goroutine (the Trace
// drain goroutine). No internal synchronisation.
//
// This file owns INGEST: Update, the per-kind on* handlers, selection/hover mutation, row-table
// rebuilds, emitSnapshot, eventReady. The PACK half — buildSnapshot and its block-writer helpers
// that turn this state into the framed bytes above — lives in Buffer/pack.go (same package, same
// receiver), split out because it sees very different commit traffic (buffer-layout/column
// changes vs. new trace-event-kind changes).

package Buffer

import (
	"encoding/binary"
	"io"
	"sync/atomic"

	T "github.com/dtauraso/wirefold/Trace"
)

// SnapshotState accumulates full-world render state from trace events.
type SnapshotState struct {
	// Node rows: stable-ordered by first KindNodeGeometry event.
	nodeIDs   []string
	nodeIndex map[string]int
	nodes     []nodeSnapState

	// Edge rows: stable-ordered by first KindGeometry event.
	edgeLabels []string
	edgeIndex  map[string]int
	edges      []edgeSnapState

	// LayoutLink rows: stable-ordered by first KindLayoutLink event. Distinct from edges —
	// sourced from the LAYOUT model (LocalPolars), not the bead-edge graph. Keyed by
	// "src\x00dst" so a re-emit of the same pair (e.g. a respawn re-running load) is idempotent
	// rather than appending a duplicate row.
	layoutLinkIndex map[string]int
	layoutLinks     []layoutLinkSnapState

	// Live in-flight beads, keyed by (sourceNode, sourcePort, gen).
	beads map[beadSnapKey]beadSnapState

	// abcDragged is the CURRENT-DRAG-SCOPED set of node ids that have received at least
	// one time.abc-drag message during the drag in progress (KindAbcDrag's Event.Node).
	// Cleared to empty on KindAbcDragReset (emitted once per drag, at the gesture FSM's
	// pending→dragging transition, before that drag's neighborSetC fan) so a new drag's
	// recipients don't accumulate on top of a prior drag's. Mirrored into each row's
	// nodeSnapState.gotDragMsg on write so the
	// AbcDragLabel overlay can list every recipient by name straight from the Node block.
	abcDragged map[string]bool

	// Camera, overlay, and scene-sphere singletons (always one row each in the snapshot).
	camera cameraSnapState
	// overlay IS the generated OverlayRow (buffer_layout_gen.go) — the same struct type
	// that writeOverlayBlock passes to SetOverlayRow BY VALUE. There is no separate
	// hand-authored mirror struct to keep in sync with the Overlay block's columns: the
	// row we mutate on incoming events is exactly the row we write, named field by
	// named field, with no positional arg list anywhere in between.
	overlay OverlayRow
	scene   sceneSnapState

	// tick is the monotonic snapshot sequence counter.
	tick uint32

	// out receives framed binary snapshots. Nil = silent discard.
	out io.Writer

	// viewOut, when non-nil, is the VIEW stream's OWN dedicated fd (see
	// Buffer/stream_fds.go / main.go's SetViewOut call) — camera+overlay+scene are
	// written there as their own frame (buildViewFrame) instead of embedded in the
	// fd-3 scene frame. Nil (the default — no WIREFOLD_STREAM_FDS entry for "view",
	// e.g. headless tests) is the REQUIRED fallback: camera/overlay/scene stay embedded
	// in buildSnapshot's scene frame exactly as before this migration. Exactly one
	// goroutine (the Trace-drain goroutine, same as `out`) ever writes to this — no lock
	// needed, mirroring `out`'s own single-writer contract.
	viewOut io.Writer

	// edgeStreamActive is true once the dedicated per-edge stream is active (see
	// SetEdgeStreamActive's doc comment) — the required either/or with fd 3's
	// Bead/Edge blocks, mirroring viewOut's role for the VIEW stream. False (the
	// default) is the REQUIRED fallback: fd 3 keeps emitting the Bead and Edge
	// blocks exactly as before this migration (headless tests, non-extension
	// launches). Ingestion (Update/onEdgeGeometry/the beads map) is UNCHANGED
	// either way — selection/the EVENT block's row resolution still depend on it;
	// only the fd-3 WRITE of the Bead/Edge frames is gated by this flag.
	//
	// ATOMIC (unlike viewOut, a plain field): SetViewOut is called BEFORE the Trace
	// drain goroutine exists (main.go constructs SnapshotState and calls SetViewOut
	// ahead of T.NewWithSinkHook's `go t.drain()`), so there is no concurrent reader
	// yet. SetEdgeStreamActive is called AFTER the drain goroutine has already
	// started (it needs md, which needs tr, which starts the drain goroutine at
	// construction) — a plain bool here would be a genuine data race between this
	// goroutine's write and emitSnapshot's read on the drain goroutine.
	edgeStreamActive atomic.Bool

	// nodeStreamActive is true once the dedicated per-node stream (StreamKindNode) is
	// active — the required either/or with fd 3's Node/Interior/Port/Label/PortName
	// frame, mirroring edgeStreamActive's role for the per-edge stream. False (the
	// default) is the REQUIRED fallback: fd 3 keeps emitting that combined frame exactly
	// as before this migration. Ingestion (Update/onNodeGeometry/onNodeBead, the node/
	// port tables) is UNCHANGED either way; only the fd-3 WRITE is gated. ATOMIC for the
	// same reason edgeStreamActive is (set after the Trace drain goroutine already
	// exists).
	nodeStreamActive atomic.Bool

	// nodeUIStates publishes the CURRENT selection/hover/drag/kind UI state for every
	// node, keyed by node id, as an immutable map — the node analogue of selectedEdges,
	// letting a dedicated nodeMover goroutine (a goroutine other than the Trace drain)
	// read this node's current selected/hovered/latchedSel/gotDragMsg/dragDelta*/kindID
	// via NodeUIStateFor with no lock. Rebuilt once per emitSnapshot call (rebuildNodeUIStates)
	// — a nodeMover's own per-cycle poll (writeStreamFrame) reads whatever is currently
	// published, matching edgeSelected's same at-most-one-snapshot-old freshness.
	nodeUIStates atomic.Pointer[map[string]nodeUIStateSnap]

	// portHovered publishes the CURRENT per-port hover state, keyed by "node\x00port\x00
	// isInput" ("0"/"1"), as an immutable map — the per-PORT analogue of nodeUIStates
	// (a node's own hover lives in nodeUIStateSnap; a PORT's hover is separate UI state
	// on portSnapState). Rebuilt alongside nodeUIStates (rebuildNodeUIStates) so a
	// dedicated nodeMover stream's own poll can read this node's own ports' hover with no
	// lock.
	portHovered atomic.Pointer[map[string]uint8]

	// selectedEdges publishes the CURRENT edge-selection state (label -> selected) as an
	// immutable map, atomically, so a dedicated edgeMover goroutine (a stream-owning
	// goroutine other than the Trace-drain goroutine) can read the current selection via
	// IsEdgeSelected with no lock — same lock-free cross-goroutine read shape as
	// nodeUIStates/portHovered above. Republished on every selection-state change
	// (setSelectedEdge/setSelected's clearSelectedEdges).
	selectedEdges atomic.Pointer[map[string]bool]

	// pendingEvents accumulates the per-tick causal trace events since the last snapshot
	// emit (the same accumulate-then-flush lifecycle as the transient node event flags).
	// buildSnapshot resolves each to numeric rows + string-section slices and writes the
	// EVENT block; clearTransients drops them. Consumed ONLY by the ext-host buffer-decoded
	// .probe logger — the render path ignores the EVENT block.
	pendingEvents []eventRec

	// kindID maps a trace-event kind string to its index in T.TraceEventKinds (the shared
	// Go/TS vocabulary), which is the EVENT block's Kind column. Built once in NewSnapshotState.
	kindID map[string]uint8

	// overlayFlagFields maps a bare-visibility-toggle trace Kind to the *uint8 field on
	// s.overlay it sets. Collapses the eight identical
	// "s.overlay.X = boolU8(ev.Visible); s.emitSnapshot()" Update cases into one lookup+set.
	// Built once in NewSnapshotState via the GENERATED overlayFlagFieldsOf (buffer_layout_gen.go,
	// mechanically derived from the Overlay block's u8 columns in Buffer/layout.go — pointers
	// into s.overlay, valid for the state's lifetime since overlay is a fixed-offset struct
	// field, not reallocated).
	overlayFlagFields map[string]*uint8

	// tickSource, when non-nil, is the network's one human-speed clock (Wiring.Clock.Tick),
	// injected via SetTickSource. It coalesces the high-volume KindPosition stream (one event
	// PER BEAD per tick, so beads_in_flight events per tick) down to at most one emit per tick,
	// matching "the tick IS the animation clock" (clock.go) instead of publishing once per bead.
	// Buffer must not import nodes/Wiring (one-way dependency), so this is injected as a plain
	// func, not a Wiring.Clock. Nil (the default, e.g. bare NewSnapshotState in tests) preserves
	// the original per-event emit behavior exactly.
	tickSource func() int64

	// lastPosEmitTick is the tick at which a KindPosition event last actually triggered an
	// emitSnapshot; -1 = none yet (so the first position event on a fresh tick always emits).
	lastPosEmitTick int64

	// breadcrumb, if set, receives the DEBUG BREADCRUMB channel (wired to tr.Breadcrumb in
	// main.go). SnapshotState runs on the single Trace-drain goroutine, so calling it from a
	// build method is goroutine-safe. Nil in headless tests (a no-op).
	breadcrumb func(label, node, port, value string)
	// lastDroppedLayoutLinks is the count of layout links dropped by resolvableLayoutLinks
	// last build, so the drop is breadcrumbed only when it CHANGES — never per-snapshot (the
	// build path runs hundreds of times/sec; a per-build breadcrumb would flood the channel).
	lastDroppedLayoutLinks int

	// positionDirty is true when bead state has changed since the last emitSnapshot call for
	// any reason. Set on every KindPosition update; cleared inside emitSnapshot (which publishes
	// the current bead state regardless of what triggered it). FinalFlush checks this so a
	// coalesced-away final tick's bead positions are never dropped at shutdown.
	positionDirty bool
}

// SetTickSource injects the network's tick function so the KindPosition stream coalesces to
// at most one emit per tick. Call once at startup (main.go, after both the clock and
// SnapshotState exist); leave unset (nil) to keep tests' original per-event emit semantics.
func (s *SnapshotState) SetTickSource(f func() int64) {
	s.tickSource = f
}

// SetViewOut installs the VIEW stream's dedicated fd (see viewOut's doc comment and
// Buffer/stream_fds.go). Call once at startup, after both this SnapshotState and the
// fd exist (main.go). Leave uncalled (viewOut stays nil) to keep the fallback: camera/
// overlay/scene embedded in the fd-3 scene frame, exactly as before this migration —
// this is what every existing test and headless launch already gets, unchanged.
func (s *SnapshotState) SetViewOut(w io.Writer) {
	s.viewOut = w
}

// SetEdgeStreamActive installs the either/or for the fd-3 Bead/Edge blocks (see
// edgeStreamActive's doc comment): pass true once every edgeMover has been wired to its
// own dedicated fd (nodes/Wiring's MoveDispatch.SetEdgeStreams, called from main.go when
// WIREFOLD_STREAM_FDS carries an "edge" entry). Leave uncalled (edgeStreamActive stays
// false) to keep the fallback: fd 3 keeps emitting the Bead and Edge blocks exactly as
// before this migration — required for headless tests and non-extension launches.
func (s *SnapshotState) SetEdgeStreamActive(active bool) {
	s.edgeStreamActive.Store(active)
}

// SetNodeStreamActive installs the either/or for the fd-3 Node/Interior/Port/Label/
// PortName frame (see nodeStreamActive's doc comment): pass true once every nodeMover has
// been wired to its own dedicated fd (nodes/Wiring's MoveDispatch.SetNodeStreams, called
// from main.go when WIREFOLD_STREAM_FDS carries a "node" entry). Leave uncalled
// (nodeStreamActive stays false) to keep the fallback — required for headless tests and
// non-extension launches.
func (s *SnapshotState) SetNodeStreamActive(active bool) {
	s.nodeStreamActive.Store(active)
}

// nodeUIStateSnap is one node's published UI-state snapshot (see nodeUIStates' doc
// comment) — everything a dedicated nodeMover stream needs to emit its own Node row that
// this SnapshotState (not the nodeMover) is the sole owner/writer of.
type nodeUIStateSnap struct {
	selected, hovered, latchedSel, gotDragMsg, kindID uint8
	dragDeltaA, dragDeltaB, dragDeltaC                int32
}

// rebuildNodeUIStates republishes nodeUIStates from the current s.nodes/s.nodeIndex
// state. Called once per emitSnapshot (Trace-drain goroutine) regardless of whether the
// dedicated node stream is active — cheap, and keeps the published map from ever going
// stale by more than one snapshot cycle.
func (s *SnapshotState) rebuildNodeUIStates() {
	m := make(map[string]nodeUIStateSnap, len(s.nodeIndex))
	ph := map[string]uint8{}
	for id, i := range s.nodeIndex {
		n := s.nodes[i]
		m[id] = nodeUIStateSnap{
			selected: n.selected, hovered: n.hovered, latchedSel: n.latchedSel,
			gotDragMsg: n.gotDragMsg, kindID: n.kindID,
			dragDeltaA: n.dragDeltaA, dragDeltaB: n.dragDeltaB, dragDeltaC: n.dragDeltaC,
		}
		for _, p := range n.ports {
			if p.hovered != 0 {
				ph[portHoverKey(id, p.name, p.isInput)] = 1
			}
		}
	}
	s.nodeUIStates.Store(&m)
	s.portHovered.Store(&ph)
}

// portHoverKey builds the portHovered map key for (node, port, isInput).
func portHoverKey(node, port string, isInput bool) string {
	iv := "0"
	if isInput {
		iv = "1"
	}
	return node + "\x00" + port + "\x00" + iv
}

// PortHoveredFor reports whether (node, port, isInput) is CURRENTLY hovered, via the
// published portHovered map. Safe to call from a goroutine other than the Trace drain,
// same lock-free shape as NodeUIStateFor/IsEdgeSelected.
func (s *SnapshotState) PortHoveredFor(node, port string, isInput bool) uint8 {
	m := s.portHovered.Load()
	if m == nil {
		return 0
	}
	return (*m)[portHoverKey(node, port, isInput)]
}

// NodeUIStateFor resolves id's current published UI-state snapshot (see nodeUIStates' doc
// comment). Safe to call from a goroutine other than the Trace drain (reads an immutable
// atomically-published map, same shape as selectedEdges/portHovered).
// ok=false when id has never registered a node-geometry event yet.
func (s *SnapshotState) NodeUIStateFor(id string) (selected, hovered, latchedSel, gotDragMsg, kindID uint8, dragDeltaA, dragDeltaB, dragDeltaC int32, ok bool) {
	m := s.nodeUIStates.Load()
	if m == nil {
		return 0, 0, 0, 0, 0, 0, 0, 0, false
	}
	st, found := (*m)[id]
	if !found {
		return 0, 0, 0, 0, 0, 0, 0, 0, false
	}
	return st.selected, st.hovered, st.latchedSel, st.gotDragMsg, st.kindID,
		st.dragDeltaA, st.dragDeltaB, st.dragDeltaC, true
}

// IsEdgeSelected reports whether label is the CURRENT click-selected edge, via the
// published selectedEdges map. Safe to call from a goroutine other than the Trace drain
// (reads an immutable atomically-published map, same shape as selectedEdges). false when
// never registered or not currently selected.
func (s *SnapshotState) IsEdgeSelected(label string) bool {
	m := s.selectedEdges.Load()
	if m == nil {
		return false
	}
	return (*m)[label]
}

// rebuildSelectedEdges republishes the selectedEdges map from the current s.edges/
// s.edgeLabels state. Called from the Trace-drain goroutine whenever edge selection
// changes (setSelectedEdge, clearSelectedEdges).
func (s *SnapshotState) rebuildSelectedEdges() {
	m := make(map[string]bool, len(s.edges))
	for i, e := range s.edges {
		if e.selected == 1 {
			m[s.edgeLabels[i]] = true
		}
	}
	s.selectedEdges.Store(&m)
}

// eventRec is one buffered causal event, holding string identities that buildSnapshot
// resolves to numeric rows / string-section slices when it writes the EVENT block.
type eventRec struct {
	kind         string
	node, port   string
	portIsInput  bool
	target       string
	targetHandle string
	edge         string
	slot         int
	value        int
	bead         uint64
	arc, lat     float64
	x, y, z, f   float64
	flag         bool // visible (overlay toggles)
}

type beadSnapKey struct {
	node string
	port string
	gen  uint64
}

// nodeSnapState holds persistent geometry + status for one node. (Recv/Fire/Send/
// Arrive/Done events are carried by the EVENT block only, via recordEvent — not
// stored per-node here.)
type nodeSnapState struct {
	cx, cy, cz      float64
	radius, sphereR float64
	// vr*/fr* are the two great-circle ring-plane normals (vertical vr, flat fr) from the
	// node-geometry event; SphereRing orients its two tori by these.
	vrx, vry, vrz float64
	frx, fry, frz float64
	// label is the node's human label (from the node-geometry event's Label; data.label
	// else the id). Streamed as UTF-8 bytes in the snapshot's trailing label section, keyed
	// by this node's LabelOff/LabelLen columns — no sidecar.
	label string
	// selected is PERSISTENT (not a transient event flag): 1 marks this node as the
	// current click-selected node. Set/cleared by KindSelect; NOT reset in clearTransients.
	selected uint8
	// hovered is PERSISTENT (not a transient event flag): 1 marks this node as the one under
	// the pointer. Set/cleared by KindHover; NOT reset in clearTransients.
	hovered uint8
	// latchedSel is PERSISTENT: 1 marks this node as the LAST node that was click-selected.
	// Unlike selected, it does NOT clear when the node is deselected (clicking empty space) —
	// only selecting a DIFFERENT node moves it. Set alongside selected in setSelected.
	latchedSel uint8
	// gotDragMsg is CURRENT-DRAG-SCOPED: 1 marks a node that has received a time.abc-drag
	// message during the drag in progress (see SnapshotState.abcDragged). Set from
	// KindAbcDrag's Event.Node id; cleared back to 0 on KindAbcDragReset (once per
	// drag, at the gesture FSM's pending→dragging transition, before that drag's fan) —
	// it's the per-node bit of the CURRENT drag's
	// recipient SET the AbcDragLabel overlay lists by name, not an accumulating session
	// total.
	gotDragMsg uint8
	// dragDeltaA/B/C mirror the DRAGGED node's own quantized-triple change (Event.
	// DeltaA/B/C) that THIS node received on the CURRENT drag's KindAbcDrag event.
	// DRAG-SCOPED like gotDragMsg: set alongside it from KindAbcDrag, cleared to 0
	// alongside it on KindAbcDragReset.
	dragDeltaA, dragDeltaB, dragDeltaC int32
	// kindID is the node's kind as its index into NODE_DEFS_ARRAY (from NodeKindID).
	// Set once on first KindNodeGeometry; subsequent re-emits don't change kind.
	kindID uint8
	// interior holds this node's 2x2 held/interior-bead grid (slot = row*2 + col).
	// PERSISTENT — a slot keeps its state until the next KindNodeBead updates it
	// (present=false explicitly clears a popped slot). Not touched by clearTransients.
	interior [BufInteriorSlotsPerNode]interiorSlotState
	// ports holds this node's port geometry (input + output), from the node-geometry
	// event's Ports. PERSISTENT — re-emitted on every node-move (only the dirs change;
	// the port set/order is stable), so buildSnapshot flattens the current ports across
	// all nodes in node-row order into the Port block. The numeric buffer carries no port
	// strings; a port hit is resolved by row index via the Go-side port-row table.
	ports []portSnapState
}

// portSnapState holds one port's unit surface direction (node center → port) and
// whether it is an input port. Populated from a node-geometry event's Ports.
type portSnapState struct {
	name       string
	dx, dy, dz float64
	px, py, pz float64
	isInput    bool
	// hovered is PERSISTENT: 1 marks this port as the one under the pointer. Set/cleared by
	// KindHover; NOT reset in clearTransients. Preserved across node-geometry re-emits below.
	hovered uint8
}

// interiorSlotState holds one interior grid slot's present/value + Go-owned
// NODE-LOCAL offset (relative to the node center).
type interiorSlotState struct {
	present    uint8
	value      int32
	ox, oy, oz float64
}

// edgeSnapState holds one edge's source/dest NODE ids (edge-graph topology used by the
// on-surface selection highlight) and PORT NAMES (used to resolve SrcPortRow/DstPortRow
// at buildEdgeFrame time via portRowLookup). srcNode/dstNode are resolved to node-row
// indices at buildSnapshot time (a node may register after its edges do). NO endpoint
// coordinates are stored here — see bufLayoutEdge's doc comment in layout.go for why
// (the endpoint-tear fix): the endpoint lives on the node-owned Port block, and this
// struct carries only the reference (node id + port name), never a copy of the value.
type edgeSnapState struct {
	srcNode string
	dstNode string
	srcPort string
	dstPort string
	// selected is PERSISTENT (not a transient event flag): 1 marks this edge as the
	// current click-selected edge. Set/cleared by KindSelect (Edge field); exclusive with
	// node selection. Not reset in clearTransients.
	selected uint8
}

// layoutLinkSnapState holds one LAYOUT-link pair's endpoint node ids. Resolved to node-row
// indices at buildSnapshot time, same as edgeSnapState's srcNode/dstNode.
type layoutLinkSnapState struct {
	srcNode string
	dstNode string
}

// beadSnapState holds current position + metadata for one in-flight bead.
type beadSnapState struct {
	x, y, z float64
	value   int
}

// cameraSnapState mirrors the camera block (single row).
type cameraSnapState struct {
	px, py, pz       float64
	r                float64
	posTheta, posPhi float64
	upTheta, upPhi   float64
}

// sceneSnapState mirrors the scene-sphere block (single row): the persisted world anchor
// every node's scene polar is measured about. Established ONCE at load and never moved
// (see KindSceneSphere); zero-value (radius 0) until that one-time startup event arrives,
// mirroring the sphereR "0 = not yet populated" convention used elsewhere in this file.
type sceneSnapState struct {
	cx, cy, cz float64
	radius     float64
}

// NewSnapshotState creates an empty SnapshotState that writes framed snapshots
// to out. Pass nil for out to build snapshots without emitting them (useful in
// tests that only inspect state).
func NewSnapshotState(out io.Writer) *SnapshotState {
	s := &SnapshotState{
		nodeIndex:       map[string]int{},
		edgeIndex:       map[string]int{},
		layoutLinkIndex: map[string]int{},
		beads:           map[beadSnapKey]beadSnapState{},
		abcDragged:      map[string]bool{},
		out:             out,
		kindID:          buildKindIDMap(),
		lastPosEmitTick: -1,
	}
	s.overlayFlagFields = overlayFlagFieldsOf(&s.overlay)
	return s
}

// buildKindIDMap indexes T.TraceEventKinds so the EVENT block Kind column matches the
// TS TRACE_EVENT_KINDS array (both generated from Trace.go's Kind* constants).
func buildKindIDMap() map[string]uint8 {
	m := make(map[string]uint8, len(T.TraceEventKinds))
	for i, k := range T.TraceEventKinds {
		m[k] = uint8(i)
	}
	return m
}

// Update processes one trace event, updating the snapshot state.
// Must be called from the Trace drain goroutine on every event.
// On KindPosition events it also triggers a snapshot emit.
func (s *SnapshotState) Update(ev T.Event) {
	// Record the causal event for the buffer-decoded .probe log BEFORE mutating state, so the
	// EVENT block flushed by the next emitSnapshot mirrors exactly the events seen this window.
	s.recordEvent(ev)
	// Overlay boolean-flag events (scene-tori, scene-poles, …) are dispatched via the
	// GENERATED IsOverlayFlagKind/overlayFlagFieldsOf (buffer_layout_gen.go, mechanically
	// derived from the Overlay block's u8 columns in Buffer/layout.go) rather than a
	// hand-listed switch case — adding a flag column requires no edit here.
	// emit tracks whether THIS event's arm should trigger emitSnapshot at the end of Update.
	// One visible line per arm below (instead of a call buried in each body) is the invariant
	// this refactor makes explicit: "does this arm emit?" — every state-mutating arm emits,
	// except the ones whose event rides the EVENT block only (Recv/Fire/Send) or whose bead
	// update is tick-coalesced (Position).
	var emit bool
	if IsOverlayFlagKind(ev.Kind) {
		if field, ok := s.overlayFlagFields[ev.Kind]; ok {
			*field = boolU8(ev.Visible)
		}
		emit = true
	} else {
		switch ev.Kind {
		case T.KindNodeGeometry:
			s.onNodeGeometry(ev)
			emit = true // state-change point: emit on geometry updates

		case T.KindGeometry:
			s.onEdgeGeometry(ev)
			emit = true // state-change point: emit on edge geometry updates

		case T.KindLayoutLink:
			s.onLayoutLink(ev)
			emit = true // state-change point: emit on layout-link registration

		case T.KindCamera:
			s.camera = cameraSnapState{
				px: ev.PX, py: ev.PY, pz: ev.PZ,
				r:        ev.R,
				posTheta: ev.PosTheta, posPhi: ev.PosPhi,
				upTheta: ev.UpTheta, upPhi: ev.UpPhi,
			}
			emit = true // state-change point: emit on camera changes

		case T.KindSceneSphere:
			// The scene sphere is established ONCE at load and never moves (MODEL.md); Go emits
			// this a single time at startup, so a plain assign (not a merge) is correct.
			s.scene = sceneSnapState{cx: ev.PX, cy: ev.PY, cz: ev.PZ, radius: ev.R}
			emit = true

		case T.KindAbcDragReset:
			// Re-scope the recipient SET to the drag that is about to start: clear the
			// drag-scoped set AND every node row's mirrored bit. Count (abcDragCount) is a
			// cumulative total-events affirmation and is intentionally left alone — only
			// the NAME SET is drag-scoped.
			s.abcDragged = map[string]bool{}
			for i := range s.nodes {
				s.nodes[i].gotDragMsg = 0
				s.nodes[i].dragDeltaA = 0
				s.nodes[i].dragDeltaB = 0
				s.nodes[i].dragDeltaC = 0
			}
			// Emit the CLEARED state. A drag that produces no AbcDrag marks at all is a real
			// path (isolated node, unresolved neighbor center, lossy-dropped fan) — without
			// this emit, the webview keeps rendering the PREVIOUS drag's recipients as if they
			// were this drag's. An empty log for a drag with no recipients is the truth.
			emit = true

		case T.KindAbcDrag:
			// Read-only affirmation counter for the in-editor overlay label; never
			// decrements, no gating semantics (unlike the bool overlay flags above).
			s.overlay.AbcDragCount++
			// Add the firing time node (ev.Node) to the current-drag recipient SET and mirror it
			// into that row's gotDragMsg bit, so the label can list every recipient by name.
			// Leave state in place if the node hasn't registered geometry yet (should not
			// happen in practice — the node already exists to have moved).
			s.abcDragged[ev.Node] = true
			if idx, ok := s.nodeIndex[ev.Node]; ok {
				s.nodes[idx].gotDragMsg = 1
				s.nodes[idx].dragDeltaA = int32(ev.DeltaA)
				s.nodes[idx].dragDeltaB = int32(ev.DeltaB)
				s.nodes[idx].dragDeltaC = int32(ev.DeltaC)
			}
			emit = true

		case T.KindPosition:
			// Update the live bead state. Every bead steps once per tick (paced_wire.go), so with
			// N beads in flight this event fires N times per tick — but the tick, not the event, is
			// the animation clock (clock.go), so coalesce to at most one emit per tick: emit only
			// when tickSource reports a tick we have not yet emitted for this stream. positionDirty
			// tracks any update an emit skip left unpublished, so FinalFlush cannot drop it.
			k := beadSnapKey{ev.Node, ev.Port, ev.Bead}
			s.beads[k] = beadSnapState{
				x: ev.X, y: ev.Y, z: ev.Z,
				value: ev.Value,
			}
			s.positionDirty = true
			if s.tickSource == nil {
				emit = true
			} else if t := s.tickSource(); t != s.lastPosEmitTick {
				s.lastPosEmitTick = t
				emit = true
			}

		case T.KindArrive:
			// Bead completed traversal: remove it from live beads.
			delete(s.beads, beadSnapKey{ev.Node, ev.Port, ev.Bead})

		case T.KindRecv:
			// No per-node state to update; the event itself is already recorded above
			// (recordEvent) for the EVENT block.

		case T.KindFire:
			// No per-node state to update; see KindRecv comment above.

		case T.KindSend:
			// Nothing further to record here; see KindRecv comment above.

		case T.KindNodeBead:
			// One interior grid slot's authoritative state (node's 2x2 held/interior
			// grid). Persistent per-node slot state; present=false clears a popped slot.
			// X/Y/Z are the Go-owned NODE-LOCAL offset (renderer adds the node center).
			s.setInteriorSlot(ev.Node, ev.Row, ev.Col, ev.Present, int32(ev.Value), ev.X, ev.Y, ev.Z)
			emit = true // state-change point: emit on interior-bead updates

		case T.KindSelect:
			// Go-owned selection: a select event marks EITHER one node (ev.Node) OR one edge
			// (ev.Edge) — never both (the gesture FSM enforces the exclusivity). ev.Edge!=""
			// selects that edge and clears any node selection; otherwise ev.Node selects that
			// node (or clears everything when empty) and clears any edge selection. Persistent —
			// survives across snapshots until the next select. Emit so the change is reflected
			// in the buffer immediately.
			if ev.Edge != "" {
				s.setSelectedEdge(ev.Edge)
			} else {
				s.setSelected(ev.Node)
			}
			emit = true

		case T.KindHover:
			// Go-owned hover: a hover event marks EITHER one port (ev.Port != "", on node
			// ev.Node, ev.Value=1 for an input port) OR one node (ev.Node), never both. It clears
			// all other node/port hover flags. ev.Node=="" && ev.Port=="" clears all hover.
			// Persistent until the next hover; emit so the change reflects in the buffer.
			s.setHovered(ev.Node, ev.Port, ev.Value == 1)
			emit = true
		}
	}
	if emit {
		s.emitSnapshot()
	}
}

// PortCount returns the total number of port rows across all nodes (for tests).
func (s *SnapshotState) PortCount() int {
	n := 0
	for i := range s.nodes {
		n += len(s.nodes[i].ports)
	}
	return n
}

// BuildSnapshot exposes the snapshot builder for tests.
func (s *SnapshotState) BuildSnapshot() []byte { return s.buildSnapshot() }

// BuildBeadFrame exposes the bead-frame builder for tests.
func (s *SnapshotState) BuildBeadFrame() []byte { return s.buildBeadFrame() }

// BuildNodeFrame exposes the node-owner-group frame builder (Node/Interior/Port blocks +
// Label/PortName bytes) for tests.
func (s *SnapshotState) BuildNodeFrame() []byte { return s.buildNodeFrame() }

// BuildEdgeFrame exposes the Edge-frame builder (Edge block + edge-label bytes) for tests.
func (s *SnapshotState) BuildEdgeFrame() []byte { return s.buildEdgeFrame() }

// --- internal helpers --------------------------------------------------------

func (s *SnapshotState) onNodeGeometry(ev T.Event) {
	id := ev.Node
	if _, exists := s.nodeIndex[id]; !exists {
		s.nodeIndex[id] = len(s.nodeIDs)
		s.nodeIDs = append(s.nodeIDs, id)
		s.nodes = append(s.nodes, nodeSnapState{kindID: NodeKindID(ev.NodeKind), gotDragMsg: boolU8(s.abcDragged[id])})
	}
	idx := s.nodeIndex[id]
	n := &s.nodes[idx]
	n.cx, n.cy, n.cz = ev.NX, ev.NY, ev.NZ
	n.radius = ev.Radius
	n.sphereR = ev.SphereR
	n.vrx, n.vry, n.vrz = ev.VRX, ev.VRY, ev.VRZ
	n.frx, n.fry, n.frz = ev.FRX, ev.FRY, ev.FRZ
	// Label: the node's human label (stable per run; re-set on each re-emit is harmless).
	// Streamed as bytes in the snapshot label section, keyed by this row's LabelOff/LabelLen.
	n.label = ev.Label
	// Port geometry: replace this node's ports with the event's current port set/dirs
	// (re-emit on move updates the dirs; the port set/order is stable). Kept in the
	// event's Ports order so the buffer Port block and the Go-side port-row table stay
	// in the same flattened row order.
	// Preserve any per-port hover flag across this re-emit: a node-move re-emits geometry
	// (only the dirs change; the port set/order is stable), and hover must not flicker off
	// mid-hover. Key by (name, isInput) since name alone can repeat across in/out.
	prevHover := make(map[[2]any]uint8, len(n.ports))
	for _, p := range n.ports {
		prevHover[[2]any{p.name, p.isInput}] = p.hovered
	}
	n.ports = n.ports[:0]
	for _, p := range ev.Ports {
		n.ports = append(n.ports, portSnapState{
			name: p.Name, dx: p.DX, dy: p.DY, dz: p.DZ,
			px: p.PX, py: p.PY, pz: p.PZ, isInput: p.IsInput,
			hovered: prevHover[[2]any{p.Name, p.IsInput}],
		})
	}
}

func (s *SnapshotState) onEdgeGeometry(ev T.Event) {
	label := ev.Edge
	if _, exists := s.edgeIndex[label]; !exists {
		s.edgeIndex[label] = len(s.edgeLabels)
		s.edgeLabels = append(s.edgeLabels, label)
		s.edges = append(s.edges, edgeSnapState{})
	}
	idx := s.edgeIndex[label]
	e := &s.edges[idx]
	// Node (source) and Target (dest) carry the edge's endpoint node ids for the
	// on-surface adjacency; preserve any previously-set ids if a later emit omits them.
	if ev.Node != "" {
		e.srcNode = ev.Node
	}
	if ev.Target != "" {
		e.dstNode = ev.Target
	}
	// SrcPort/DstPort carry the edge's endpoint port NAMES, resolved to buffer PORT-ROW
	// indices at buildEdgeFrame time (portRowLookup) — see edgeSnapState's doc comment
	// for why no endpoint coordinate is stored here at all.
	if ev.SrcPort != "" {
		e.srcPort = ev.SrcPort
	}
	if ev.DstPort != "" {
		e.dstPort = ev.DstPort
	}
}

// onLayoutLink registers one LAYOUT-link pair (Node=one endpoint, Target=the other), sourced
// from LocalPolars (nodes/Wiring/loader.go emitLayoutLinks) — NOT the bead-edge graph. Idempotent
// on the unordered pair key so a re-emit does not append a duplicate row.
func (s *SnapshotState) onLayoutLink(ev T.Event) {
	key := ev.Node + "\x00" + ev.Target
	if _, exists := s.layoutLinkIndex[key]; exists {
		return
	}
	s.layoutLinkIndex[key] = len(s.layoutLinks)
	s.layoutLinks = append(s.layoutLinks, layoutLinkSnapState{srcNode: ev.Node, dstNode: ev.Target})
}

// setInteriorSlot records one interior grid slot's state on a node. slot = row*2 + col;
// out-of-range (row,col) or unknown nodes are ignored. Persistent — survives across
// snapshots until the next node-bead updates the slot.
func (s *SnapshotState) setInteriorSlot(nodeID string, row, col int, present bool, value int32, ox, oy, oz float64) {
	idx, ok := s.nodeIndex[nodeID]
	if !ok {
		return
	}
	slot := row*2 + col
	if slot < 0 || slot >= BufInteriorSlotsPerNode {
		return
	}
	s.nodes[idx].interior[slot] = interiorSlotState{
		present: boolU8(present), value: value, ox: ox, oy: oy, oz: oz,
	}
}

// setSelected marks nodeID as the selected node and clears the flag on every other
// node. nodeID=="" clears all selection. Persistent state — not touched by
// clearTransients.
func (s *SnapshotState) setSelected(nodeID string) {
	sel := -1
	if nodeID != "" {
		if idx, ok := s.nodeIndex[nodeID]; ok {
			sel = idx
		}
	}
	for i := range s.nodes {
		if i == sel {
			s.nodes[i].selected = 1
			// latchedSel moves to the newly-selected node; a deselect (sel == -1) leaves
			// every node's latchedSel untouched here (the loop below never sets latchedSel
			// on i == -1), so the PREVIOUSLY latched node stays latched through deselect.
			s.nodes[i].latchedSel = 1
		} else {
			s.nodes[i].selected = 0
			if sel >= 0 {
				s.nodes[i].latchedSel = 0
			}
		}
	}
	// Node selection is exclusive with edge selection: selecting/clearing a node clears
	// any selected edge.
	s.clearSelectedEdges()
}

// setSelectedEdge marks the edge with the given label selected and clears the flag on
// every other edge; it also clears any node selection (selection is single + exclusive).
// An unknown label clears all edge selection. Persistent — not touched by clearTransients.
func (s *SnapshotState) setSelectedEdge(label string) {
	sel := -1
	if idx, ok := s.edgeIndex[label]; ok {
		sel = idx
	}
	for i := range s.edges {
		if i == sel {
			s.edges[i].selected = 1
		} else {
			s.edges[i].selected = 0
		}
	}
	// Exclusive with node selection.
	for i := range s.nodes {
		s.nodes[i].selected = 0
	}
	s.rebuildSelectedEdges()
}

// setHovered marks the hovered entity and clears hover on every other node and port. A
// non-empty port hovers that (node, port, isInput); otherwise a non-empty node hovers that
// node; both empty clears all hover. Persistent — not touched by clearTransients.
func (s *SnapshotState) setHovered(nodeID, port string, isInput bool) {
	// Clear all hover first.
	for i := range s.nodes {
		s.nodes[i].hovered = 0
		for j := range s.nodes[i].ports {
			s.nodes[i].ports[j].hovered = 0
		}
	}
	idx, ok := s.nodeIndex[nodeID]
	if !ok {
		return // unknown/empty node → nothing to set (already cleared)
	}
	if port != "" {
		for j := range s.nodes[idx].ports {
			p := &s.nodes[idx].ports[j]
			if p.name == port && p.isInput == isInput {
				p.hovered = 1
				return
			}
		}
		return // port not found → leave node unhovered (a port hover is not a node hover)
	}
	s.nodes[idx].hovered = 1
}

// clearSelectedEdges clears the selected flag on every edge.
func (s *SnapshotState) clearSelectedEdges() {
	for i := range s.edges {
		s.edges[i].selected = 0
	}
	s.rebuildSelectedEdges()
}

// nodeRowIndex returns the buffer node-row index for a node id, or -1 when the id is
// empty or not yet registered (edges can register before their endpoint nodes do).
func (s *SnapshotState) nodeRowIndex(nodeID string) int {
	if nodeID == "" {
		return -1
	}
	if idx, ok := s.nodeIndex[nodeID]; ok {
		return idx
	}
	return -1
}

// clearTransients drops the per-tick causal events now that they have been packed
// into the emitted snapshot's EVENT block.
func (s *SnapshotState) clearTransients() {
	s.pendingEvents = s.pendingEvents[:0]
}

// emitSnapshot builds one snapshot, writes a framed frame to s.out, then
// clears transient event flags. Ignores write errors: the ext host normally
// reads fd 3, but a disconnected/closed fd (e.g. headless tests, or the pipe
// closing on process exit) is harmless — there is no delivery guarantee on
// this channel (fire-and-forget, per MODEL.md).
func (s *SnapshotState) emitSnapshot() {
	// Defer any event whose identity references a node/edge not yet registered (e.g. an
	// Input node's startup send fires before its target's geometry is emitted). Such an event
	// would resolve to row -1 and drop its target/handle from the log; carry it forward to the
	// next emit instead, when the referenced entity is registered. Ordering is irrelevant to
	// the log (a multiset of events), and by end-of-run every node/edge is registered.
	ready := s.pendingEvents[:0:0]
	deferred := make([]eventRec, 0)
	for _, e := range s.pendingEvents {
		if s.eventReady(e) {
			ready = append(ready, e)
		} else {
			deferred = append(deferred, e)
		}
	}
	s.pendingEvents = ready

	snap := s.buildSnapshot()
	beadFrame := s.buildBeadFrame()
	nodeFrame := s.buildNodeFrame()
	edgeFrame := s.buildEdgeFrame()
	if s.out != nil {
		// Write errors are intentionally ignored: this is the fire-and-forget Go→TS render
		// stream (CLAUDE.md — no ack, no delivery signal), emitted every tick. Logging on
		// failure would be a per-tick firehose (see log-flood lesson), and there is no caller
		// that could act on the error. A dead peer (broken pipe) is a lifecycle event: the
		// host tears the Go process down, so there is nothing to recover here.
		//
		// Four frames, each independently length-prefixed + tagged: bead, then node, then
		// edge, then scene LAST. Order matters here (unlike the earlier bead-only split): the
		// ext host's .probe buffer-decoded log resolves the SCENE frame's EVENT block
		// node/port/edge rows against the NODE and EDGE frames — writing node and edge before
		// scene guarantees the ext host's per-tag frame cache already holds THIS tick's node
		// and edge frames by the time it decodes this tick's scene frame (see runCommand.ts
		// handleFd3). Edge is written after node for the same reason the LayoutLink block's
		// resolution needs the node frame cached first — no direct ordering requirement
		// between node and edge themselves, but this keeps every downstream-referenced frame
		// ahead of the frame that references it.
		// Either/or with the dedicated per-edge streams (edgeStreamActive — see its doc
		// comment): when active, every edgeMover writes its OWN combined edge+bead frame
		// to its OWN fd (node_mover.go's writeStreamFrame), so the fd-3 Bead and Edge
		// blocks are skipped here entirely — never double-sourced from both places.
		if !s.edgeStreamActive.Load() {
			s.writeFrame(BufBlockTagBead, beadFrame)
			s.writeFrame(BufBlockTagEdge, edgeFrame)
		}
		// Either/or with the dedicated per-node streams (nodeStreamActive — see its doc
		// comment): when active, every nodeMover writes its OWN combined node+ports+label
		// frame to its OWN fd, and every node's own Update goroutine writes its OWN
		// interior-bead frame to its OWN fd, so the fd-3 Node block (which carries
		// Node/Interior/Port/Label/PortName together) is skipped here entirely.
		if !s.nodeStreamActive.Load() {
			s.writeFrame(BufBlockTagNode, nodeFrame)
		}
		s.writeFrame(BufBlockTagScene, snap)
	}
	// Republish the node UI-state map every emit, regardless of which write path is
	// active (rebuildNodeUIStates' doc comment) — cheap, and keeps a dedicated nodeMover
	// stream's own poll (writeStreamFrame) from ever reading state more than one
	// snapshot cycle stale.
	s.rebuildNodeUIStates()
	// VIEW stream: when the dedicated fd is active (s.viewOut != nil), camera/overlay/
	// scene were already EXCLUDED from `snap` above (buildSnapshot's either/or) — write
	// them here instead, as their own untagged frame on their own fd (no tag byte: the
	// fd position already identifies the stream, see stream_fds.go). When s.viewOut is
	// nil (the fallback), buildSnapshot already embedded them in `snap` above and there
	// is nothing further to write here.
	if s.viewOut != nil {
		s.writeRawFrame(s.viewOut, s.buildViewFrame())
	}
	s.clearTransients()
	// Restore the deferred events (clearTransients truncated pendingEvents to empty).
	s.pendingEvents = append(s.pendingEvents, deferred...)
	// This emit just published the current bead state (buildSnapshot always packs s.beads),
	// regardless of what triggered it — clear the coalesce-tracking flag.
	s.positionDirty = false
}

// writeFrame writes one [len:u32-LE][tag:u8][payload] frame to s.out. len counts the tag
// byte plus len(payload). Errors are ignored — see emitSnapshot's comment on why.
func (s *SnapshotState) writeFrame(tag byte, payload []byte) {
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(1+len(payload)))
	_, _ = s.out.Write(hdr[:])
	_, _ = s.out.Write([]byte{tag})
	_, _ = s.out.Write(payload)
}

// writeRawFrame writes one [len:u32-LE][payload] frame to w, WITHOUT a tag byte — used
// for a stream on its OWN dedicated fd (today, the VIEW stream), where the fd position
// already identifies the kind (see Buffer/stream_fds.go), so there is nothing left to
// discriminate inside the frame. Errors are ignored, same reasoning as writeFrame/
// emitSnapshot's comment (fire-and-forget, no delivery guarantee on this channel).
func (s *SnapshotState) writeRawFrame(w io.Writer, payload []byte) {
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(payload)))
	_, _ = w.Write(hdr[:])
	_, _ = w.Write(payload)
}

// eventReady reports whether every node/edge identity an event references is registered, so
// buildSnapshot can resolve it to a real row (not the -1 sentinel that would drop it from the
// decoded log).
func (s *SnapshotState) eventReady(e eventRec) bool {
	if e.node != "" && s.nodeRowIndex(e.node) < 0 {
		return false
	}
	if e.target != "" && s.nodeRowIndex(e.target) < 0 {
		return false
	}
	if e.edge != "" {
		if _, ok := s.edgeIndex[e.edge]; !ok {
			return false
		}
	}
	return true
}

func boolU8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}
