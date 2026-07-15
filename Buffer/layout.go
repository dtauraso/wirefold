// Buffer/layout.go — single source of truth for the agnostic content-buffer
// column layout schema.
//
// tools/gen-node-defs reads this file and emits:
//   - Buffer/buffer_layout_gen.go  (Go offset constants + typed writer helpers)
//   - tools/topology-vscode/src/schema/buffer-layout.ts  (TS constants + DataView readers)
//
// Regenerate with: cd tools/topology-vscode && npm run gen:node-defs
//
// Field tags: buf:"type" where type is one of f32 | i32 | u32 | u8.
// Offsets and strides are computed by the generator in field-declaration order
// (packed; no implicit padding — DataView handles unaligned reads on both sides).
// Struct names beginning with "bufLayout" are recognised by the generator as
// column-block definitions; the suffix becomes the block name (e.g. bufLayoutBead
// → block "Bead").
//
// Event enum constants beginning with BufEvent are emitted as matching integer
// constants on both Go and TS sides.
//
// BUF_LAYOUT_VERSION is bumped whenever any column definition changes; the
// generated files carry the same version so a stale regeneration is immediately
// visible.

package Buffer

// BufLayoutVersion is the schema version. Bump when any column changes.
const BufLayoutVersion = 23

// BufInteriorSlotsPerNode is the fixed number of interior grid slots reserved per
// node in the Interior block (a 2x2 held/interior-bead grid: slot = row*2 + col).
// The Interior block carries exactly nodeCount*BufInteriorSlotsPerNode rows in
// stable node order, so it needs no separate count in the header — the decoder
// derives its length from nodeCount. Not a per-column generated field (there is no
// bufLayoutInterior column for it), but gen-node-defs DOES read this const directly
// (parseBufferLayout) and emits it as generated TS (INTERIOR_SLOTS_PER_NODE in
// buffer-layout.ts) and Go (BufInteriorSlotsPerNodeGenerated) constants, folded into
// BUF_LAYOUT_FINGERPRINT — so a drift here fails check-buffer-layout-parity.sh, not
// just a same-symbolic-constant-on-both-sides test that could never catch a value change.
const BufInteriorSlotsPerNode = 4

// --- Semantic event enum ------------------------------------------------
// Transient flags stored in node rows (one u8 per event kind per node per tick).

const (
	BufEventRecv   = 0 // node received a bead on an input port this tick
	BufEventFire   = 1 // node fired (produced output) this tick
	BufEventSend   = 2 // node sent a bead on an output port this tick
	BufEventArrive = 3 // a bead arrived at this node's input this tick
	BufEventDone   = 4 // node finished consuming a held bead this tick
)

// --- Column block schemas -----------------------------------------------
// Each struct defines one column block. Fields are packed in declaration order.
// The generator computes byte offsets and stride from buf: tags.

// bufLayoutBead defines one row of the beads column block.
// One row per live in-flight bead. Matched from KindPosition trace events.
type bufLayoutBead struct {
	X     float32 `buf:"f32"` // world x position
	Y     float32 `buf:"f32"` // world y position
	Z     float32 `buf:"f32"` // world z position
	Value int32   `buf:"i32"` // bead integer value
	Live  uint8   `buf:"u8"`  // 1 = slot occupied; 0 = absent (sentinel row)
}

// bufLayoutNode defines one row of the nodes column block.
// One row per node. Persistent geometry + transient event flags.
// Matched from KindNodeGeometry, KindRecv/Fire/Send/Arrive/Done.
type bufLayoutNode struct {
	CX      float32 `buf:"f32"` // node center x (world)
	CY      float32 `buf:"f32"` // node center y (world)
	CZ      float32 `buf:"f32"` // node center z (world)
	Radius  float32 `buf:"f32"` // body/ring sphere radius
	SphereR float32 `buf:"f32"` // sphere-chain radius (port placement)
	// VR*/FR* are the node's two great-circle ring-plane normals (vertical vr, flat fr),
	// the same orientation vectors the pre-branch SphereRing read from nodeGeometryStore.
	// SphereRing draws two tori at the owner's center oriented by these; they arrive on the
	// node-geometry trace event (Trace VRX.., FRX..).
	VRX      float32 `buf:"f32"` // vertical ring-plane normal x
	VRY      float32 `buf:"f32"` // vertical ring-plane normal y
	VRZ      float32 `buf:"f32"` // vertical ring-plane normal z
	FRX      float32 `buf:"f32"` // flat (equatorial) ring-plane normal x
	FRY      float32 `buf:"f32"` // flat ring-plane normal y
	FRZ      float32 `buf:"f32"` // flat ring-plane normal z
	EvRecv   uint8   `buf:"u8"`  // transient: node received a bead this tick
	EvFire   uint8   `buf:"u8"`  // transient: node fired this tick
	EvSend   uint8   `buf:"u8"`  // transient: node sent a bead this tick
	EvArrive uint8   `buf:"u8"`  // transient: a bead arrived at node this tick
	EvDone   uint8   `buf:"u8"`  // transient: node finished consuming this tick
	Selected uint8   `buf:"u8"`  // persistent: 1 = this node is the click-selected node
	// KindId is the node's kind as a 0-based index into the alphabetically-sorted
	// NODE_DEFS array (generated by tools/gen-node-defs). Populated once on
	// first KindNodeGeometry; 0xFF = unknown kind.
	KindId uint8 `buf:"u8"` // kind index into NODE_DEFS_ARRAY; 0xFF = unknown
	// LabelOff/LabelLen are this node's slice into the snapshot's trailing LABEL BYTES
	// section: LabelOff is the byte offset into that section, LabelLen the UTF-8 byte
	// length. The label bytes for all nodes are concatenated in node-row order (self-
	// sizing via the header labelBytesCount field, like portCount), so the numeric node
	// row carries its human label with no sidecar: the renderer slices
	// labelBytes[LabelOff : LabelOff+LabelLen) and TextDecoder-decodes it. LabelLen=0 = no
	// label (fall back to nothing / row index on the render side).
	LabelOff uint32 `buf:"u32"` // byte offset into the label-bytes section
	LabelLen uint32 `buf:"u32"` // label UTF-8 byte length
	// Faded is the fixpoint fade mask for this node (1 = dimmed). Go owns the directly-
	// faded seeds and recomputes the fixpoint (computeFade) each snapshot; the renderer
	// dims a faded node's body/ring opacity. Persistent (not a transient event flag).
	Faded uint8 `buf:"u8"` // 1 = node is faded (dimmed)
	// Hovered is the Go-owned pointer-hover flag: 1 marks the node currently under the
	// pointer (the gesture FSM tracks it from the raycast hit on each pointer-move and
	// emits KindHover). The renderer thickens+recolors this node's border ring (pre-branch
	// hover style: #aaddff, r*0.14). Persistent-until-next-move; NOT a transient event flag.
	// Selection styling takes precedence over hover where both apply (renderer-side).
	Hovered uint8 `buf:"u8"` // 1 = node is pointer-hovered
	// LatchedSel is Go-owned: 1 marks the LAST node that was click-selected, and stays 1
	// through a deselect (clicking empty space clears Selected but NOT LatchedSel; selecting
	// a DIFFERENT node moves LatchedSel to it). Set by setSelected alongside Selected — see
	// nodeSnapState.latchedSel in Buffer/snapshot.go. Replaces the old TS-owned
	// `latchedSel` React state in NavGuides.tsx (that was a second, TS-invented selection
	// concept unreachable from Go); the render path now just reads this column.
	LatchedSel uint8 `buf:"u8"` // 1 = this is the last-selected node (persists through deselect)
}

// bufLayoutInterior defines one row of the interior-bead column block.
// The block carries a FIXED BufInteriorSlotsPerNode (4) rows per node, in stable
// node order: row = nodeRow*BufInteriorSlotsPerNode + slot, slot = gridRow*2 + gridCol.
// Matched from KindNodeBead trace events (node's 2x2 held/interior grid). OX/OY/OZ
// are the Go-owned NODE-LOCAL slot offset (relative to the node center — the renderer
// adds the node center to get the world position); Present=0 hides the slot even when
// Value/offset are present (a popped/empty slot is streamed explicitly so it clears).
type bufLayoutInterior struct {
	Present uint8   `buf:"u8"`  // 1 = slot filled (draw); 0 = empty (hide)
	Value   int32   `buf:"i32"` // bead value (0|1); colored via bead-style
	OX      float32 `buf:"f32"` // node-local slot offset x
	OY      float32 `buf:"f32"` // node-local slot offset y
	OZ      float32 `buf:"f32"` // node-local slot offset z
}

// bufLayoutEdge defines one row of the edges column block.
// One row per edge (wire). Matched from KindGeometry trace events.
// SrcNodeRow/DstNodeRow are the buffer NODE-ROW indices of this edge's source and
// destination nodes (same first-seen node order as the Node block); -1 = not yet
// resolved. They carry the edge-graph topology the on-surface selection highlight
// needs (the pre-branch computed this from the React edge list; the buffer path has
// no such list, so Go streams the adjacency here). Stored as i32 (the generator has
// no i16 tag) — node counts are small, so the width is inconsequential.
type bufLayoutEdge struct {
	SX         float32 `buf:"f32"` // start (source OUT-port) world x
	SY         float32 `buf:"f32"` // start world y
	SZ         float32 `buf:"f32"` // start world z
	EX         float32 `buf:"f32"` // end (dest IN-port) world x
	EY         float32 `buf:"f32"` // end world y
	EZ         float32 `buf:"f32"` // end world z
	SrcNodeRow int32   `buf:"i32"` // source node's buffer node-row index (-1 = unresolved)
	DstNodeRow int32   `buf:"i32"` // destination node's buffer node-row index (-1 = unresolved)
	Selected   uint8   `buf:"u8"`  // persistent: 1 = this edge is the click-selected edge
	// Faded is the fixpoint fade mask for this edge (1 = dimmed). Set by Go's fade fixpoint
	// (computeFade) each snapshot; the renderer dims a faded edge's tube. A faded edge's
	// transit bead is suppressed Go-side (its bead rows are written Live=0).
	Faded uint8 `buf:"u8"` // 1 = edge is faded (dimmed)
	// EdgeLabelOff/EdgeLabelLen are this edge's slice into the snapshot's trailing EDGE-LABEL
	// BYTES section (the label-section analogue for edges): EdgeLabelOff is the byte offset,
	// EdgeLabelLen the UTF-8 byte length. Edge labels are carried ONLY for the .probe buffer-
	// decoded log (geometry `edge`, select-edge, fade `fadedEdges`) — the render/bridge path
	// still resolves an edge hit by row index (LookupEdgeRow), never by this string.
	// Concatenated in the same stable edge-row order as the Edge block.
	EdgeLabelOff uint32 `buf:"u32"` // byte offset into the edge-label-bytes section
	EdgeLabelLen uint32 `buf:"u32"` // edge-label UTF-8 byte length
}

// bufLayoutPort defines one row of the ports column block.
// One row per node port (input or output). The block is self-sizing via a portCount
// field in the snapshot header (like beadCount/edgeCount), then flattened across all
// nodes in node-row order: for each node in its buffer row order, that node's ports in
// node-geometry Ports order. NodeRow is the owning node's buffer node-row index; DX/DY/DZ
// is the port's unit direction on the node surface (node center → port, the pre-branch
// portDir); IsInput=1 for an input port, 0 for an output port. PX/PY/PZ is the port's
// AUTHORITATIVE world position (Go-computed, the same point the connected edge's endpoint
// uses) — the renderer plots the marker there directly, no client-side recompute. The
// numeric buffer carries NO port strings: a port HIT is resolved
// by its port-row index, which Go maps back to its own (node, port) via the Go-side port-row
// table (LookupPortRow), built in this same flattened row order — so port row i ↔ (node,port) i.
type bufLayoutPort struct {
	NodeRow int32   `buf:"i32"` // owning node's buffer node-row index
	DX      float32 `buf:"f32"` // unit dir x (node center → port)
	DY      float32 `buf:"f32"` // unit dir y
	DZ      float32 `buf:"f32"` // unit dir z
	// PX/PY/PZ are the port's AUTHORITATIVE world position — the same point the
	// connected edge's endpoint uses (portWorldPosAimed: aimed dir × per-port PortR).
	// The renderer plots the port marker directly at PX/PY/PZ (no nodeCenter+DIR*radius
	// recompute on the TS side), so the marker, the edge endpoint, and the node center
	// are guaranteed polar-colinear by construction.
	PX      float32 `buf:"f32"` // world x
	PY      float32 `buf:"f32"` // world y
	PZ      float32 `buf:"f32"` // world z
	IsInput uint8   `buf:"u8"`  // 1 = input port, 0 = output port
	// Hovered is the Go-owned pointer-hover flag for this port: 1 marks the port currently
	// under the pointer (the gesture FSM tracks it from the raycast port hit and emits
	// KindHover). The renderer highlights this port (pre-branch isHov style). Persistent-
	// until-next-move; NOT a transient event flag.
	Hovered uint8 `buf:"u8"` // 1 = port is pointer-hovered
	// PortNameOff/PortNameLen are this port's slice into the snapshot's trailing PORT-NAME
	// BYTES section (the label-section analogue for ports): PortNameOff is the byte offset,
	// PortNameLen the UTF-8 byte length. Port names are carried ONLY for the .probe buffer-
	// decoded log (send targetHandle, recv/send/done/hover port, node-geometry port names) —
	// the render/bridge path still resolves a port hit by row index (LookupPortRow), never by
	// this string. Concatenated in the same flattened port-row order as the Port block.
	PortNameOff uint32 `buf:"u32"` // byte offset into the port-name-bytes section
	PortNameLen uint32 `buf:"u32"` // port-name UTF-8 byte length
}

// bufLayoutCamera defines the camera column block (always 1 row).
// Matched from KindCamera trace events.
type bufLayoutCamera struct {
	PX       float32 `buf:"f32"` // pivot world x
	PY       float32 `buf:"f32"` // pivot world y
	PZ       float32 `buf:"f32"` // pivot world z
	R        float32 `buf:"f32"` // orbit radius
	PosTheta float32 `buf:"f32"` // pivot→camera polar θ
	PosPhi   float32 `buf:"f32"` // pivot→camera polar φ
	UpTheta  float32 `buf:"f32"` // up-hint polar θ
	UpPhi    float32 `buf:"f32"` // up-hint polar φ
}

// bufLayoutOverlay defines the overlay visibility column block (always 1 row).
// Matched from KindSceneTori/ScenePoles/…/OverlaysVis trace events.
// Field order matches overlayState in overlay_gen.go.
type bufLayoutOverlay struct {
	SceneTori      uint8 `buf:"u8"` // 1 = polar-guide tori visible
	ScenePoles     uint8 `buf:"u8"` // 1 = scene-center pole frame visible
	NodePoles      uint8 `buf:"u8"` // 1 = per-node pole frames visible
	SelSpherePoles uint8 `buf:"u8"` // 1 = selection-sphere pole axes visible
	Handholds      uint8 `buf:"u8"` // 1 = rotation grab-sphere handholds visible
	LabelsGlobal   uint8 `buf:"u8"` // 1 = all node labels visible
	BadgesGlobal   uint8 `buf:"u8"` // 1 = all occlusion +N badges visible
	OverlaysVis    uint8 `buf:"u8"` // 1 = master overlays toggle on
	// SelMode is NOT an overlay flag — it rides the overlay singleton row only because
	// it is a single global value that changes with selection. 1 = "own" (secondary /
	// two-finger select: owners = [selected]); 0 = "surface" (primary click: owners =
	// nodes that output TO selected). Set by KindSelect from the gesture button. The
	// on-surface highlight reads it to pick the pre-branch owner/surface mode.
	SelMode uint8 `buf:"u8"`
}

// bufLayoutScene defines the scene-sphere column block (always 1 row).
// Matched from KindSceneSphere trace events. The scene sphere is the persisted, first-class
// world anchor every node's scene polar is measured about (nodes/Wiring/sphere_layout.go
// sceneSphere) — established ONCE at load and never moved. Replaces the TS-side
// contentSphereFromCenters (a derived, non-authoritative content-sphere centroid recomputed
// from live node positions every frame) as the sphere NavGuides draws its polar tori around.
type bufLayoutScene struct {
	CX     float32 `buf:"f32"` // scene-sphere center x (world)
	CY     float32 `buf:"f32"` // scene-sphere center y (world)
	CZ     float32 `buf:"f32"` // scene-sphere center z (world)
	Radius float32 `buf:"f32"` // scene-sphere radius
}

// bufLayoutEvent defines one row of the per-tick EVENT column block.
// The block is self-sizing via an eventCount field in the snapshot header; it carries
// the causal trace events that occurred since the previous snapshot (recv/fire/send/done/
// arrive/pulse-cancelled/position and the state-change kinds), cleared each emit like the
// transient node flags. It is consumed ONLY by the ext-host buffer-decoded .probe logger —
// the render path ignores it. Kind is the event's index into TRACE_EVENT_KINDS (shared
// Go/TS vocabulary); the row/label references resolve identities via the existing row
// tables + string sections, so no id/port/edge strings are duplicated per event.
// Sentinel: row/index fields are -1 when the event does not carry that reference.
type bufLayoutEvent struct {
	Kind          uint8   `buf:"u8"`  // index into TRACE_EVENT_KINDS
	NodeRow       int32   `buf:"i32"` // emitting node's buffer row (-1 = none)
	PortRow       int32   `buf:"i32"` // port's buffer row (-1 = none)
	TargetRow     int32   `buf:"i32"` // target node's buffer row (send; -1 = none)
	TargetPortRow int32   `buf:"i32"` // target handle's port row (send; -1 = none)
	EdgeRow       int32   `buf:"i32"` // edge's buffer row (geometry/select-edge; -1 = none)
	Slot          int32   `buf:"i32"` // node-bead interior slot = row*2+col (-1 = none)
	Value         int32   `buf:"i32"` // event value (recv/send/position/status/select mode/…)
	Bead          uint32  `buf:"u32"` // per-wire bead id (wire-bead events; 0 = none)
	ArcLength     float32 `buf:"f32"` // send: wire arc length
	SimLatencyMs  float32 `buf:"f32"` // send: wire traversal latency (ms)
	X             float32 `buf:"f32"` // position/status world/marker x
	Y             float32 `buf:"f32"` // position/status world/marker y
	Z             float32 `buf:"f32"` // position/status world/marker z
	F             float32 `buf:"f32"` // position: fractional progress t
}

// schemaTypes prevents the bufLayout* types from being flagged as unused by
// staticcheck. They are schema sources: the generator reads them via AST at
// codegen time; they are not used at runtime.
var _ = [...]any{
	bufLayoutBead{},
	bufLayoutNode{},
	bufLayoutInterior{},
	bufLayoutEdge{},
	bufLayoutPort{},
	bufLayoutCamera{},
	bufLayoutOverlay{},
	bufLayoutScene{},
	bufLayoutEvent{},
}
