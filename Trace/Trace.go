// Trace is now a thin breadcrumb writer plus the closed EVENT-KIND vocabulary shared
// with the per-owner buffer streams (memory/feedback_no_single_writer_bridge.md, memory/
// feedback_no_single_writer_bridge.md). Every domain event (recv/fire/send/geometry/
// camera/selection/overlay-toggle/...) is now written by its OWNING goroutine directly
// as a RowEvent onto that goroutine's own dedicated stream frame (nodes/Wiring's
// owner_events.go and friends) — there is no more central Trace channel, no drain
// goroutine, and no second (redundant) serialization of those events through this
// package. Buffer.KindID resolves a RowEvent's string Kind to its numeric id via
// TraceEventKinds below, which stays the single source of that vocabulary (also
// generated into tools/topology-vscode/src/schema/trace-kinds.ts).
//
// The one exception is NodeBead: emitRefillSlide's per-frame interior-refill-slide
// animation (nodes/Wiring/emit_geometry.go) calls Trace.NodeBead directly with no
// RowEvent dual of its own — kept as an actual Trace event for that reason, delivered
// synchronously (no channel) to an optional in-process onEvent hook (headless tests)
// and/or sink (in-process test buffer). Neither is wired in production (main.go passes
// none), so this is a no-op cost on the live path.
//
// Breadcrumb is the other survivor: a free-form diagnostic line (outside the closed
// Kind vocabulary), written directly — one `sink.Write` call per breadcrumb, on the
// calling goroutine, no channel and no lock. A single small write to a pipe is atomic
// per POSIX PIPE_BUF, so concurrent breadcrumbs from many goroutines never interleave
// into a fused line; breadcrumbs are short, sparse control-event lines (see CLAUDE.md's
// "Debugging the Go layer" section), never a per-tick firehose, so this holds in practice.

package Trace

import "io"

// Closed event-kind vocabulary. Every per-owner stream's RowEvent.Kind (nodes/Wiring's
// owner_events.go and friends) is one of these strings; Buffer.KindID resolves it to
// its TraceEventKinds index for the wire encoding. Node is always the emitting node —
// the one that received the value (recv) or sent it (send/fire) — Port distinguishes
// input vs output where applicable.
const (
	KindRecv = "recv"
	KindFire = "fire"
	KindSend = "send"
	// KindPosition is the per-frame bead-position kind. The wire's delivery goroutine
	// resolves one every ~16 ms while a bead is in flight, carrying the bead's evaluated
	// 3-D position so the renderer plots it without computing geometry itself.
	KindPosition = "edge-bead"
	// KindGeometry carries an edge's authoritative straight-segment endpoints. The
	// edgeMover resolves one per edge on load and again whenever a node-move re-derives
	// that edge's segment, so the renderer draws the wire tube from Go's endpoints and
	// computes no geometry of its own. Keyed by edge label (== the TS edge id).
	KindGeometry = "geometry"
	// KindNodeGeometry carries one node's authoritative center + per-port world
	// positions/directions. Each nodeMover resolves this once on startup and again on
	// every node-move — the node owns its own geometry (wires own bead-position).
	// Keyed by node id.
	KindNodeGeometry = "node-geometry"
	// KindArrive marks a bead COMPLETING its traversal on a wire — the bead has reached
	// the destination port and is delivered into the slot. The wire resolves it from
	// deliverLocked (the single delivery path), keyed by the bead's SOURCE node+port —
	// the same routing key as send/position — so the renderer clears the transit pulse
	// the instant the bead arrives.
	KindArrive = "arrive"
	// KindNodeBead carries one INTERIOR slot's authoritative grid-slot state (node 1's
	// depleting/refilling buffer). Node 1's Update computes the 2x2 grid slot positions
	// coupled to the working/backup array mutation and resolves a 4-slot SNAPSHOT (one
	// node-bead kind per slot) whenever the array changes. Keyed by node id +
	// (row,col): row 0 = top/backup, row 1 = bottom/working; col is the index within
	// that row. Payload = present (filled?) + value (0|1) + world position (x,y,z). A
	// popped slot carries present=false so TS clears it (absence can't be rendered,
	// presence can).
	KindNodeBead = "node-bead"
	// KindCamera carries the polar camera viewpoint state. Go resolves it whenever the
	// camera is set, orbited, zoomed, or panned, so the renderer can reconstruct the
	// camera pose without computing any geometry itself.
	KindCamera = "camera"
	// KindSceneTori carries the polar-guide tori visibility state.
	KindSceneTori = "scene-tori"
	// KindScenePoles carries the scene-center pole frame visibility state.
	KindScenePoles = "scene-poles"
	// KindNodePoles carries the per-node pole frame visibility state.
	KindNodePoles = "node-poles"
	// KindSelSpherePoles carries the selection-sphere pole axis visibility state.
	KindSelSpherePoles = "sel-sphere-poles"
	// KindHandholds carries the rotation-handhold grab-sphere visibility state.
	KindHandholds = "handholds"
	// KindLabelsGlobal carries the global node-label visibility state.
	KindLabelsGlobal = "labels-global"
	// KindOverlaysVis carries the master overlays visibility state.
	KindOverlaysVis = "overlays-vis"
	// KindDoubleLinks carries the double-link (layout-link) overlay visibility state.
	// Default OFF (unlike the other overlay flags).
	KindDoubleLinks = "double-links"
	// KindLayoutLink carries one double-linked node PAIR from the LAYOUT model
	// (nodes/Wiring/layout_holder.go LocalPolars) — NOT the bead-edge graph. Streamed
	// once per pair at load (deduplicated so each unordered pair streams exactly once),
	// keyed by Node (one endpoint) and Target (the other).
	KindLayoutLink = "layout-link"
	// KindSelect carries the CURRENTLY-SELECTED node id (click-select), or an edge label
	// on Edge with Node empty (edge selection — selection is single + exclusive across
	// nodes and edges). Node="" clears the selection (empty-space click).
	KindSelect = "select"
	// KindHover carries the CURRENTLY-HOVERED entity (pointer hover). Port!="" hovers
	// that port (Node is its owning node, Value=1 for an input port); otherwise Node
	// hovers that node (Node="" clears all hover).
	KindHover = "hover"
	// KindSceneSphere carries the persisted scene sphere (center + radius) — the fixed
	// world anchor every node's scene polar is measured about. Established ONCE at load
	// and never moves.
	KindSceneSphere = "scene-sphere"
	// KindAbcDrag marks one time-node (HoldNewSendOld) abc-drag re-quantize event — the
	// routed counterpart to the "time.abc-drag" debug breadcrumb emitted alongside it
	// (nodes/Wiring/node_move.go neighborSetCRequantize).
	KindAbcDrag = "abc-drag"
	// KindAbcDragReset marks the START of one drag operation — resolved exactly once at
	// the gesture FSM's pending→dragging transition, BEFORE the dragged node's
	// neighborSetC fan resolves any KindAbcDrag marks for that drag.
	KindAbcDragReset = "abc-drag-reset"
	// KindBreadcrumb carries a DEBUG BREADCRUMB (see CLAUDE.md's "Debugging the Go
	// layer" section) as a structured buffer EVENT row instead of a free-form JSON
	// stdout line. It rides the EMITTING goroutine's own per-owner stream (node/edge/
	// interior/VIEW) — main.go's own breadcrumbs (no per-node stream) ride the VIEW
	// stream. Label (a BreadcrumbLabel* index below) names which of the 9 breadcrumb
	// sites emitted it; the row's other columns (Value/X/Y/Z/NodeRow/PortRow/
	// TargetRow/TargetPortRow) are REUSED per label, with Label/TextOff/TextLen (the
	// bufLayoutEvent.Debug flag is always 1 on this Kind) as the two dedicated
	// breadcrumb-only columns.
	KindBreadcrumb = "breadcrumb"
)

// BreadcrumbLabel* enumerate the 9 breadcrumb call sites (Buffer/layout.go's
// bufLayoutEvent.Label column, Kind==KindBreadcrumb rows only). Order is the wire id —
// append only; do not reorder or delete a label without a migration. BreadcrumbLabels
// is the string lookup gen-node-defs mirrors into TS for the .probe decode/log.
const (
	BreadcrumbTopologyLoaded uint8 = iota
	BreadcrumbRowSeedCountMismatch
	BreadcrumbPoleToggleGo
	BreadcrumbWindowClear
	BreadcrumbWindowOpen
	BreadcrumbDwellStart
	BreadcrumbAbcDrag
	BreadcrumbWireSendBufferFull
	BreadcrumbCascadeRoot
)

// BreadcrumbLabels is the single source of truth for the BreadcrumbLabel* enum's
// string names, indexed by the enum value — mirrored into TS by gen-node-defs for the
// .probe buffer-decoded breadcrumb log.
var BreadcrumbLabels = []string{
	"topology-loaded",
	"row-seed-count-mismatch",
	"pole-toggle-go",
	"window_clear",
	"window_open",
	"dwell_start",
	"abc-drag",
	"wire-send-buffer-full",
	"cascade.root",
}

// TraceEventKinds is the single source of truth for the closed kind vocabulary.
// gen-node-defs reads this slice to emit trace-kinds.ts (the TS decode side's kindId →
// name lookup), and Buffer.KindID indexes it to resolve a RowEvent's string Kind to its
// numeric id for the wire encoding. There is no tsc exhaustiveness check derived from
// it — adding a kind here does not force a TS branch anywhere; it only extends the
// lookup table.
var TraceEventKinds = []string{KindRecv, KindFire, KindSend, KindPosition, KindGeometry, KindNodeGeometry, KindArrive, KindNodeBead, KindCamera, KindSceneTori, KindScenePoles, KindNodePoles, KindSelSpherePoles, KindHandholds, KindLabelsGlobal, KindOverlaysVis, KindDoubleLinks, KindLayoutLink, KindSelect, KindHover, KindSceneSphere, KindAbcDrag, KindAbcDragReset, KindBreadcrumb}

// PortGeom is one port's authoritative world geometry: its name, whether it is an
// input, its sphere-surface world position (PX/PY/PZ), and the unit direction from node
// center toward the port (DX/DY/DZ). Shared value type used by nodes/Wiring's own
// per-node stream-frame builders (node_mover.go/node_move.go) — independent of the
// (deleted) central NodeGeometry event this used to also ride on.
type PortGeom struct {
	Name       string
	IsInput    bool
	PX, PY, PZ float64
	DX, DY, DZ float64
}

// Event is the payload NodeBead (the one surviving Trace event) and Breadcrumb (outside
// the closed Kind vocabulary) carry. Trimmed to just the fields those two use — every
// other field the pre-decentralization Event struct carried (Step, Bead, geometry,
// camera, overlay-visibility, ...) died with the methods that populated them.
type Event struct {
	Kind     string
	Node     string
	Port     string
	Value    int
	Row, Col int
	Present  bool
	X, Y, Z  float64
	// BreadcrumbLabel/BreadcrumbValue carry a Breadcrumb() call's label/value strings.
	// Node/Port above are reused for a breadcrumb's node/port arguments.
	BreadcrumbLabel string
	BreadcrumbValue string
}

// Trace holds the optional in-process test sink (headless tests only — never wired in
// production) and the production DEBUG BREADCRUMB sink (main.go's os.Stdout). Both
// fields are set ONCE at startup (New*/SetDebugSink), before any producer goroutine
// exists, and never mutated again — read-only for the rest of the process, so every
// later caller (running in a goroutine spawned after startup) sees the write via the
// ordinary happens-before edge from goroutine creation. No lock needed: there is no
// second writer of either field, and NodeBead/Breadcrumb only ever READ them.
type Trace struct {
	sink      io.Writer
	debugSink io.Writer
	onEvent   func(Event) // optional in-process observation hook (headless tests only)
}

// New allocates a Trace with no sinks wired. buf is accepted only for call-site
// compatibility with the pre-decentralization API (there is no channel to size
// anymore); pass any value.
func New(buf int) *Trace {
	return NewWithSink(buf, nil)
}

// NewWithSink is like New but wires sink as the in-process test-observation sink (see
// Breadcrumb/NodeBead's doc comments) — never wired in production.
func NewWithSink(buf int, sink io.Writer) *Trace {
	return NewWithSinkHook(buf, sink, nil)
}

// NewWithSinkHook is like NewWithSink but also installs onEvent, called synchronously
// (on the calling goroutine) by NodeBead — the one surviving Trace event. Pass nil for
// onEvent to omit the hook (production always does).
func NewWithSinkHook(buf int, sink io.Writer, onEvent func(Event)) *Trace {
	return &Trace{sink: sink, onEvent: onEvent}
}

// SetDebugSink wires the production DEBUG BREADCRUMB channel: after this call every
// Breadcrumb() line is ALSO written to w in real time (in addition to the optional
// in-process test sink). main passes os.Stdout so breadcrumbs ride stdout as
// {"kind":"breadcrumb",...} lines; the ext host routes those to .probe/go-debug.jsonl
// (distinct from RowEvents, which ride the per-owner stream fds, and from errors on
// stderr). Diagnostic-only and fire-and-forget — never blocks a node loop and is safe
// to leave unset (Breadcrumb stays a no-op on that sink). Set once at startup before
// nodes run — no lock: the happens-before edge from goroutine creation is what makes
// every later Breadcrumb() caller (running in a goroutine spawned after this call
// returns) see the write.
func (t *Trace) SetDebugSink(w io.Writer) {
	if t == nil {
		return
	}
	t.debugSink = w
}

// Close is a no-op kept for call-site compatibility (there is no channel/goroutine to
// drain anymore — every producer writes its own RowEvent/breadcrumb directly and
// synchronously).
func (t *Trace) Close() {}

// Breadcrumb writes a free-form diagnostic line DIRECTLY — one `sink.Write` call per
// sink, on the CALLING goroutine. No channel, no lock, no ordinal: breadcrumbs are
// outside the closed Kind vocabulary (RowEvents carry the closed vocabulary; this is a
// control-event log line). A single small write to a pipe is atomic per POSIX
// PIPE_BUF, so concurrent breadcrumb lines from many goroutines never interleave into
// one fused line — breadcrumbs are short, sparse control events (see CLAUDE.md's
// "Debugging the Go layer" section), never a per-tick firehose, so line length never
// approaches that limit in practice.
//
// sink = the in-process test observation buffer (headless model/gate tests poll it).
// debugSink = the PRODUCTION debug channel (os.Stdout, wired via SetDebugSink), which
// the ext host recognises by the "breadcrumb" kind and routes to .probe/go-debug.jsonl.
// A breadcrumb with neither sink wired is a cheap no-op.
func (t *Trace) Breadcrumb(label, node, port, value string) {
	if t == nil || (t.sink == nil && t.debugSink == nil) {
		return
	}
	b, err := marshalBreadcrumb(label, node, port, value)
	if err != nil {
		return
	}
	b = append(b, '\n')
	if t.sink != nil {
		_, _ = t.sink.Write(b)
	}
	if t.debugSink != nil {
		_, _ = t.debugSink.Write(b)
	}
}

// NodeBead is the one surviving Trace EVENT (see this file's header doc comment):
// emitRefillSlide (nodes/Wiring/emit_geometry.go) calls it directly, once per animation
// frame, with no RowEvent dual of its own. Delivered synchronously to the optional
// onEvent hook and/or sink — neither wired in production, so this is a cheap no-op on
// the live path. nodeID + (row,col) key the slot; present/value/x/y/z carry its state.
func (t *Trace) NodeBead(nodeID string, row, col int, present bool, value int, x, y, z float64) {
	if t == nil {
		return
	}
	ev := Event{Kind: KindNodeBead, Node: nodeID, Row: row, Col: col, Present: present, Value: value, X: x, Y: y, Z: z}
	if t.sink != nil {
		if b, err := marshalNodeBead(ev); err == nil {
			_, _ = t.sink.Write(append(b, '\n'))
		}
	}
	if t.onEvent != nil {
		t.onEvent(ev)
	}
}
