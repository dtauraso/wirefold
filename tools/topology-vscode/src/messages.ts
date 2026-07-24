// Shared discriminated unions for webview ↔ extension-host messaging.
// Both sides import from here so unknown / malformed messages are caught
// at type-narrow time rather than silently writing `[object Object]` to disk.

// Geometry-CRUD edit sent webview → host → Go. ONE message kind ("edit") whose sole op is
// "update" (mirroring nodes/Wiring/stdin_reader.go applyEdit). Go owns the clock; this
// seam carries no delivery signal. The create/delete ops were removed end-to-end: no live
// TS sender ever emitted them, and their only trigger (a port-drop gesture) unconditionally
// tore down a live wire's in-flight beads via PacedWire.Restore().
//
//   - update: set an ATTRIBUTE on a typed entity. `kind` names the entity
//     (node / edge / camera / overlays / scene); there is NO per-feature op — fading
//     an edge, moving a port anchor, orbiting the camera and toggling an overlay are
//     all attribute updates on their entity.
//
// For op="update" kind="node" attr="move" (node-move): the decentralized Go path
// mail-sorts the move to the owning node + each incident edge goroutine, so the
// entries map is keyed by the moved node id AND each incident edge id
// (source===moved || target===moved). Every entry carries the same moved node id +
// new position. The webview computes the incident edges from its React Flow graph
// (TS owns the graph; Go owns the recompute).
// OVERLAY_FLAG_NAMES is the SINGLE source for the overlay wire vocabulary — named
// boolean overlay attributes shared with Go's overlayToggles map (stdin_reader.go).
// The OverlayFlag union and the OVERLAY_FLAGS runtime set are ALL derived from it, so
// the field set is listed once.
// Guarded by check-edit-op-parity.sh.
// OVERLAY_FLAGS_START
const OVERLAY_FLAG_NAMES = [
  "tori",
  "scenePoles",
  "nodePoles",
  "selSpherePoles",
  "handholds",
  "labelsGlobal",
  "overlays",
  "doubleLinks",
] as const;
// OVERLAY_FLAGS_END

export type OverlayFlag = (typeof OVERLAY_FLAG_NAMES)[number];

// OVERLAY_FLAG_ORDER is the runtime array of the flag vocabulary, in the canonical wire
// ORDER (the overlays toggle/set binary records use a flag's index here as its u8/bit id —
// input-layout.ts). Exported so the binary codec keys off the exact same ordering.
export const OVERLAY_FLAG_ORDER = OVERLAY_FLAG_NAMES;

// EDIT_MSG_START
// The geometry-CRUD edit surface. The sole op is "update" — create/delete were removed
// end-to-end (see the comment above). The live update entities are overlays (toggle one
// flag) and clock (set the playback-speed multiplier); node/edge/camera edits became
// gesture-FSM-in-process (raw-input) and scene became the bare `save` command — none
// cross this seam any more. The former attr="set" full-visibility install was dead (its
// only caller, the load-time main.tsx push, was removed); only attr="toggle" is live for
// overlays.
type EditMsg =
  // op="update" — set an attribute on a typed entity (kind discriminator).
  | { type: "edit"; op: "update"; kind: "overlays"; attr: "toggle"; flag: OverlayFlag }
  | { type: "edit"; op: "update"; kind: "clock"; attr: "speed"; value: number };
// EDIT_MSG_END

// RAW INPUT (Phase 6, OFF by default behind USE_RAW_INPUT). A single raw pointer/wheel
// event plus the stateless three.js raycast hit, forwarded fire-and-forget to Go. TS does
// NOT interpret the gesture — Go's gesture state machine (nodes/Wiring/gesture.go) decides
// what the raw event means (orbit / zoom / pan / drag). The hit carries only the
// rendered ENTITY under the pointer (three.js computes the geometry); topology facts like
// "is this port already connected?" are Go's to decide, not carried here.
//
// This is a NEW top-level message kind ("raw-input"), NOT an edit op — it is INPUT, not a
// geometry-CRUD edit. Kept in message-kind parity with stdin_reader.go's msg.Type switch.
// RAW_INPUT_START
// "home" is a fit-to-content COMMAND on the raw-input channel (not a pointer/wheel
// gesture): TS carries only the render context Go needs to size the fit (camera fov +
// viewport aspect via rectWidth/rectHeight); Go computes the home pose from its OWN node
// geometry. It is NOT a camera pose sent by TS — the model keeps Go owning the camera.
export type RawPointerKind = "pointerdown" | "pointermove" | "pointerup" | "wheel" | "home";

/** The stateless raycast hit: which rendered entity is under the pointer + its world point.
 *  kind classifies the rendered target (three.js hit-testing). Every entity hit carries ONLY a
 *  numeric buffer ROW — Go resolves the row back to its entity via its own row tables, so NO id
 *  string crosses the bridge. isInput is vestigial (Go derives the port side from its port-row
 *  table). nodeRow/portRow/edgeRow are the buffer NODE/PORT/EDGE row indices (the InstancedMesh
 *  instanceId == its buffer row), each -1 when the hit is not of that kind. */
export type RawHit = {
  kind: "port" | "handhold" | "node" | "edge" | "torus" | "empty";
  isInput: boolean;
  /** Numeric buffer NODE-ROW index for a node hit (the node InstancedMesh instanceId == its
   *  buffer row); -1 when not a node hit. Go resolves nodeRow → node id via its node-row table.
   *  A torus (border-ring) hit ALSO carries the owning node's row here (rings are drawn per-node
   *  in the same row order as bodies) — Go resolves it the same way. */
  nodeRow: number;
  portRow: number;
  /** Numeric buffer EDGE-ROW index for an edge hit (the edge's pick-halo carries its buffer
   *  edge row); -1 when not an edge hit. Go resolves edgeRow → its edge via its edge-row table. */
  edgeRow: number;
  /** Term-id for a handhold hit (+θ=0, +φ=1, -θ=2, -φ=3; NavGuides.tsx HANDHOLD_TERM_TAG);
   *  -1 when not a handhold hit. Go decodes comp/sign from it (nodes/Wiring/gesture.go). */
  handholdTerm: number;
};

export type RawInputEvent = {
  kind: RawPointerKind;
  x: number; // client pixel X
  y: number; // client pixel Y
  rectLeft: number;
  rectTop: number;
  rectWidth: number;
  rectHeight: number;
  button: number; // pointer button (0 primary, 2 secondary); -1 for move/wheel
  ctrl: boolean;
  shift: boolean;
  alt: boolean;
  meta: boolean;
  deltaX: number; // wheel delta X (0 otherwise)
  deltaY: number; // wheel delta Y (0 otherwise)
  fov: number; // camera vertical fov (degrees)
  hit: RawHit;
};
// RAW_INPUT_END

export type WebviewToHostMsg =
  | { type: "ready" }
  // BINARY bridge envelope: a fully-encoded editor→Go record (raw-input or edit). The
  // webview builds the binary record via schema/input-layout.ts and posts it here; the
  // host writes it FRAMED to Go's stdin. This is the TS→Go binary buffer (symmetric with
  // the fd-3 content buffer). The logical Go-bound kinds ("raw-input", "edit") are no
  // longer posted as JSON objects — they are encoded into this record — but stay declared
  // below (and EditMsg is kept in this union) as the shared vocabulary the message-kind +
  // edit-op parity guards check.
  | { type: "go-record"; record: ArrayBuffer }
  | { type: "raw-input"; event: RawInputEvent }
  // The bare SAVE command (encoded as a single kind byte in schema/input-layout.ts and
  // sent via go-record). Go persists its OWN authoritative scene state — no payload. Kept
  // in this union + WEBVIEW_TO_HOST_TYPES so message-kind-parity tracks stdin_reader.go's
  // "save" msg.Type.
  | { type: "save" }
  | { type: "webview-log"; entry: string }
  | EditMsg;

export type HostToWebviewMsg =
  // Binary snapshot from Go's fd3 side channel — the ONLY render path (see
  // webview/snapshot-buffer.ts + handle-message.ts); there is no separate JSON-trace
  // render path and no pending phase to flip to.
  // The ArrayBuffer is transferred zero-copy (postMessage transferable).
  // Go → TS is the binary content buffer ONLY (no id/label sidecar): each node's human label
  // rides the buffer node block (LabelOff/LabelLen into the trailing label section) and is
  // decoded row-keyed via buffer-decode nodeLabel.
  //
  // tag carries the fd-3 frame's block tag (schema/frame-tags.ts BUF_BLOCK_TAG_SCENE /
  // BUF_BLOCK_TAG_BEAD / BUF_BLOCK_TAG_NODE / BUF_BLOCK_TAG_EDGE), or a SYNTHETIC tag the
  // ext host attaches when relaying a frame decoded off a dedicated fd (never a wire tag
  // byte there — see frame-tags.ts): BUF_BLOCK_TAG_VIEW (the singleton view fd) or
  // BUF_BLOCK_TAG_EDGE_STREAM (one of the per-edge fds — row names WHICH edge, since
  // there are many, unlike view's singleton). main.tsx routes the payload to the right
  // cell (snapshot-buffer.ts's scene/bead/node/edge/view cells, or the per-edge-row cell
  // for BUF_BLOCK_TAG_EDGE_STREAM) without re-decoding the buffer.
  | { type: "buffer-snapshot"; buffer: ArrayBuffer; tag: number; row?: number };

export const WEBVIEW_TO_HOST_TYPES: ReadonlySet<WebviewToHostMsg["type"]> = new Set([
  "ready", "webview-log", "edit", "save", "raw-input", "go-record",
]);

const HOST_TO_WEBVIEW_TYPES: ReadonlySet<HostToWebviewMsg["type"]> = new Set([
  "buffer-snapshot",
]);

// The editor→Go payload validators (parseEdit / parseUpdate / parseRawInput) were removed
// when the TS→Go bridge became a purely BINARY buffer: Go-bound messages are now built as
// typed binary records in schema/input-layout.ts (compile-time shape safety from EditMsg /
// RawInputEvent) and decoded + bounds-checked in Go (input_codec.go). The host no longer
// sees a JSON edit/raw-input envelope to validate — it receives an opaque "go-record"
// ArrayBuffer and writes it framed to Go. parseWebviewToHost below validates only the
// remaining host-CONTROL messages (webview-log/go-record/etc.) plus the go-record envelope.

export function parseWebviewToHost(raw: unknown): WebviewToHostMsg | undefined {
  if (!raw || typeof raw !== "object") return undefined;
  const t = (raw as { type?: unknown }).type;
  if (typeof t !== "string" || !WEBVIEW_TO_HOST_TYPES.has(t as WebviewToHostMsg["type"])) {
    return undefined;
  }
  const m = raw as Record<string, unknown>;
  switch (t) {
    case "webview-log":
      return typeof m.entry === "string" ? (m as unknown as WebviewToHostMsg) : undefined;
    case "go-record":
      // The BINARY editor→Go record envelope. Only the ArrayBuffer wrapper is validated
      // here; the record's own layout is decoded + bounds-checked in Go (input_codec.go).
      return m.record instanceof ArrayBuffer ? (m as unknown as WebviewToHostMsg) : undefined;
    default:
      return m as unknown as WebviewToHostMsg;
  }
}

// parseHostToWebview validates a host→webview message by its type AND its payload,
// mirroring parseWebviewToHost's per-op shape checks. A malformed message is dropped
// (undefined) rather than forwarded — the webview message listener drops undefined, so a
// bad envelope can never reach a downstream consumer that destructures it and throws,
// blanking the editor.
export function parseHostToWebview(raw: unknown): HostToWebviewMsg | undefined {
  if (!raw || typeof raw !== "object") return undefined;
  const m = raw as Record<string, unknown>;
  const t = m.type;
  if (typeof t !== "string" || !HOST_TO_WEBVIEW_TYPES.has(t as HostToWebviewMsg["type"])) {
    return undefined;
  }
  switch (t) {
    case "buffer-snapshot":
      // buffer must be an ArrayBuffer (transferred zero-copy from the host); tag must be
      // the numeric fd-3 block tag it carries.
      return m.buffer instanceof ArrayBuffer && typeof m.tag === "number"
        ? (m as unknown as HostToWebviewMsg)
        : undefined;
    default:
      return undefined;
  }
}
