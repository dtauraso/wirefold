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
const BufLayoutVersion = 11

// BufInteriorSlotsPerNode is the fixed number of interior grid slots reserved per
// node in the Interior block (a 2x2 held/interior-bead grid: slot = row*2 + col).
// The Interior block carries exactly nodeCount*BufInteriorSlotsPerNode rows in
// stable node order, so it needs no separate count in the header — the decoder
// derives its length from nodeCount. Not a generated column; kept in sync with the
// TS INTERIOR_SLOTS_PER_NODE by the interior-block decode test.
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
	X      float32 `buf:"f32"` // world x position
	Y      float32 `buf:"f32"` // world y position
	Z      float32 `buf:"f32"` // world z position
	Value  int32   `buf:"i32"` // bead integer value
	Frac   float32 `buf:"f32"` // fractional progress t in [0,1] along wire
	BeadID uint32  `buf:"u32"` // per-wire monotonic bead id (1-based)
	Live   uint8   `buf:"u8"`  // 1 = slot occupied; 0 = absent (sentinel row)
}

// bufLayoutNode defines one row of the nodes column block.
// One row per node. Persistent geometry + status + transient event flags.
// Matched from KindNodeGeometry, KindNodeStatus, KindRecv/Fire/Send/Arrive/Done.
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
	TorusRed uint8   `buf:"u8"`  // 1 = torus is RED (missed bead error)
	MissVal  int32   `buf:"i32"` // missed bead value (valid when TorusRed=1)
	MX       float32 `buf:"f32"` // missed-bead marker world x
	MY       float32 `buf:"f32"` // missed-bead marker world y
	MZ       float32 `buf:"f32"` // missed-bead marker world z
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
}

// bufLayoutPort defines one row of the ports column block.
// One row per node port (input or output). The block is self-sizing via a portCount
// field in the snapshot header (like beadCount/edgeCount), then flattened across all
// nodes in node-row order: for each node in its buffer row order, that node's ports in
// node-geometry Ports order. NodeRow is the owning node's buffer node-row index; DX/DY/DZ
// is the port's unit direction on the node surface (node center → port, the pre-branch
// portDir); IsInput=1 for an input port, 0 for an output port. The port world position is
// nodeCenter + DIR*nodeRadius, computed on the render side (mirrors the pre-branch
// PortSphere placement). The numeric buffer carries NO port strings: a port HIT is resolved
// by its port-row index, which Go maps back to its own (node, port) via the Go-side port-row
// table (LookupPortRow), built in this same flattened row order — so port row i ↔ (node,port) i.
type bufLayoutPort struct {
	NodeRow int32   `buf:"i32"` // owning node's buffer node-row index
	DX      float32 `buf:"f32"` // unit dir x (node center → port)
	DY      float32 `buf:"f32"` // unit dir y
	DZ      float32 `buf:"f32"` // unit dir z
	IsInput uint8   `buf:"u8"`  // 1 = input port, 0 = output port
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
// Matched from KindSceneTori/ScenePoles/…/DoubleLinks trace events.
// Field order matches overlayState in overlay_gen.go.
type bufLayoutOverlay struct {
	SceneTori      uint8 `buf:"u8"` // 1 = polar-guide tori visible
	ScenePoles     uint8 `buf:"u8"` // 1 = scene-center pole frame visible
	NodePoles      uint8 `buf:"u8"` // 1 = per-node pole frames visible
	AngleLabels    uint8 `buf:"u8"` // 1 = θ/φ arc+label overlays visible
	SelSpherePoles uint8 `buf:"u8"` // 1 = selection-sphere pole axes visible
	Handholds      uint8 `buf:"u8"` // 1 = rotation grab-sphere handholds visible
	LabelsGlobal   uint8 `buf:"u8"` // 1 = all node labels visible
	BadgesGlobal   uint8 `buf:"u8"` // 1 = all occlusion +N badges visible
	OverlaysVis    uint8 `buf:"u8"` // 1 = master overlays toggle on
	DoubleLinks    uint8 `buf:"u8"` // 1 = double-link arrow overlays visible
	// SelMode is NOT an overlay flag — it rides the overlay singleton row only because
	// it is a single global value that changes with selection. 1 = "own" (secondary /
	// two-finger select: owners = [selected]); 0 = "surface" (primary click: owners =
	// nodes that output TO selected). Set by KindSelect from the gesture button. The
	// on-surface highlight reads it to pick the pre-branch owner/surface mode.
	SelMode uint8 `buf:"u8"`
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
}
