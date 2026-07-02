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
const BufLayoutVersion = 1

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
	CX        float32 `buf:"f32"` // node center x (world)
	CY        float32 `buf:"f32"` // node center y (world)
	CZ        float32 `buf:"f32"` // node center z (world)
	Radius    float32 `buf:"f32"` // body/ring sphere radius
	SphereR   float32 `buf:"f32"` // sphere-chain radius (port placement)
	TorusRed  uint8   `buf:"u8"`  // 1 = torus is RED (missed bead error)
	MissVal   int32   `buf:"i32"` // missed bead value (valid when TorusRed=1)
	MX        float32 `buf:"f32"` // missed-bead marker world x
	MY        float32 `buf:"f32"` // missed-bead marker world y
	MZ        float32 `buf:"f32"` // missed-bead marker world z
	EvRecv    uint8   `buf:"u8"`  // transient: node received a bead this tick
	EvFire    uint8   `buf:"u8"`  // transient: node fired this tick
	EvSend    uint8   `buf:"u8"`  // transient: node sent a bead this tick
	EvArrive  uint8   `buf:"u8"`  // transient: a bead arrived at node this tick
	EvDone    uint8   `buf:"u8"`  // transient: node finished consuming this tick
}

// bufLayoutEdge defines one row of the edges column block.
// One row per edge (wire). Matched from KindGeometry trace events.
type bufLayoutEdge struct {
	SX float32 `buf:"f32"` // start (source OUT-port) world x
	SY float32 `buf:"f32"` // start world y
	SZ float32 `buf:"f32"` // start world z
	EX float32 `buf:"f32"` // end (dest IN-port) world x
	EY float32 `buf:"f32"` // end world y
	EZ float32 `buf:"f32"` // end world z
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
}
