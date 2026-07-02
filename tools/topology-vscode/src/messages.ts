// Shared discriminated unions for webview ↔ extension-host messaging.
// Both sides import from here so unknown / malformed messages are caught
// at type-narrow time rather than silently writing `[object Object]` to disk.

import type { NodeStatusEvent } from "./schema/trace-event-fields";

export type RunStatus =
  | { state: "running" }
  | { state: "paused" }
  | { state: "ok" }
  | { state: "error"; message: string }
  | { state: "cancelled" };

// Geometry-CRUD edit sent webview → host → Go. ONE message kind ("edit") with
// EXACTLY THREE ops: create / update / delete (mirroring nodes/Wiring/stdin_reader.go
// applyEdit). Go owns the clock; this seam carries no delivery signal.
//
//   - create / delete: add or remove an edge by its destination slot (target + targetHandle).
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
export type MoveEntry = { nodeId: string; x: number; y: number; z: number };

// OVERLAY_FLAG_NAMES is the SINGLE source for the overlay wire vocabulary — named
// boolean overlay attributes shared with Go's overlayToggles map and stdinGuideVisPayload
// (stdin_reader.go). The OverlayFlag union, the OVERLAY_FLAGS runtime set, and the
// OverlayState snapshot type are ALL derived from it, so the field set is listed once.
// Guarded by check-edit-op-parity.sh.
// OVERLAY_FLAGS_START
const OVERLAY_FLAG_NAMES = [
  "tori",
  "scenePoles",
  "nodePoles",
  "angleLabels",
  "selSpherePoles",
  "handholds",
  "labelsGlobal",
  "badgesGlobal",
  "overlays",
  "doubleLinks",
] as const;
// OVERLAY_FLAGS_END

export type OverlayFlag = (typeof OVERLAY_FLAG_NAMES)[number];

// OverlayState is the full explicit-visibility snapshot pushed on load (attr="set").
// Derived from OverlayFlag so the field set can never drift from the flag vocabulary.
type OverlayState = Record<OverlayFlag, boolean>;

// VIEWPOINT_KINDS is the single source for the camera viewpoint sub-kind vocabulary,
// shared with Go's vp.Kind switch (stdin_reader.go). Parity guarded by
// check-edit-op-parity.sh via the VP_KINDS sentinels.
// VP_KINDS_START
const VIEWPOINT_KINDS = [
  "set",
  "orbit",
  "orbit-locked",
  "zoom",
  "pan",
] as const;
// VP_KINDS_END

type ViewpointKind = (typeof VIEWPOINT_KINDS)[number];

type ViewpointPayload =
  | {
      kind: "set";
      pivotX?: number; pivotY?: number; pivotZ?: number;
      r?: number;
      posTheta?: number; posPhi?: number;
      upTheta?: number; upPhi?: number;
    }
  | { kind: "orbit"; fromTheta: number; fromPhi: number; toTheta: number; toPhi: number }
  | { kind: "orbit-locked"; fromTheta: number; fromPhi: number; toTheta: number; toPhi: number }
  | { kind: "zoom"; factor: number }
  | { kind: "pan"; dx: number; dy: number; dz: number };

// EDIT_MSG_START
type EditMsg =
  | { type: "edit"; op: "create"; target: string; targetHandle: string }
  | { type: "edit"; op: "delete"; target: string; targetHandle: string }
  // op="update" — set an attribute on a typed entity (kind discriminator).
  | { type: "edit"; op: "update"; kind: "node"; attr: "move"; entries: Record<string, MoveEntry> }
  // Port-anchor: move a port along its node's ring. node/port identify the port,
  // isInput selects the input vs output list, anchor is the new direction offset from
  // the node center. keys lists the routing keys Go mail-sorts to — the node id AND
  // each incident edge id (same fan-out shape as attr="move").
  | {
      type: "edit"; op: "update"; kind: "node"; attr: "anchor";
      node: string; port: string; isInput: boolean;
      anchor: { x: number; y: number; z: number };
      keys: string[];
    }
  | { type: "edit"; op: "update"; kind: "edge"; attr: "faded"; edges: Record<string, boolean> }
  | { type: "edit"; op: "update"; kind: "camera"; viewpoint: ViewpointPayload }
  | { type: "edit"; op: "update"; kind: "overlays"; attr: "toggle"; flag: OverlayFlag }
  | { type: "edit"; op: "update"; kind: "overlays"; attr: "set"; state: OverlayState }
  | { type: "edit"; op: "update"; kind: "scene"; scene: unknown };
// EDIT_MSG_END

// RAW INPUT (Phase 6, OFF by default behind USE_RAW_INPUT). A single raw pointer/wheel
// event plus the stateless three.js raycast hit, forwarded fire-and-forget to Go. TS does
// NOT interpret the gesture — Go's gesture state machine (nodes/Wiring/gesture.go) decides
// what the raw event means (orbit / zoom / pan / create / delete). The hit carries only the
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
 *  kind classifies the rendered target (three.js hit-testing); id is the entity id
 *  (node id, or "nodeId:in|out:portName" for a port on the OLD path); isInput selects the
 *  port side (old path). portRow is the numeric buffer PORT-ROW index for a NEW-system port
 *  hit (the port InstancedMesh instanceId == its buffer row); -1 when not a new-system port.
 *  Go resolves portRow → (node, port) via its own port-row table — no port name crosses. */
export type RawHit = {
  kind: "port" | "handhold" | "node" | "edge" | "empty";
  id: string;
  isInput: boolean;
  portRow: number;
  /** Numeric buffer EDGE-ROW index for a NEW-system edge hit (the edge's pick-halo carries
   *  its buffer edge row); -1 when not an edge hit. Go resolves edgeRow → its edge via its
   *  own edge-row table — no edge label crosses the bridge. */
  edgeRow: number;
  x: number;
  y: number;
  z: number;
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
  | { type: "raw-input"; event: RawInputEvent }
  | { type: "run" }
  | { type: "run-cancel" }
  | { type: "play" }
  | { type: "pause" }
  | { type: "resume" }
  | { type: "stop" }
  // Host-originated control re-sent to Go's stdin (geometry resend on webview remount).
  // Declared here for seam parity with stdin_reader.go's "resend" kind; the webview
  // itself does not emit it (the host triggers it in the "ready" handler).
  | { type: "resend" }
  | { type: "webview-log"; entry: string }
  | EditMsg;

// Mirrors Go Trace.Event shape. kind ∈
// {"recv","fire","send","done","edge-bead","geometry","pulse-cancelled"}.
// recv/send carry port+value; fire carries only node; send also carries edge
// when the Go side has resolved it (currently omitted — raw form only).
// edge-bead (Phase 2) carries the bead's Go-computed 3-D world position (x,y,z) plus
// its FRACTIONAL progress t along the wire (f, 0..1), keyed by source node+port like
// send so the renderer routes it by source+sourceHandle. Go owns progress; the editor
// places the bead at lerp(liveStart, liveEnd, f) on its LOCAL (dragged) node port
// positions so the bead rides the live wire with no round-trip lag (it does not
// compute geometry — it places a Go-owned fraction on editor-owned node positions).
// geometry (Phase 3) carries an edge's authoritative straight-segment endpoints
// (sx/sy/sz = Start, ex/ey/ez = End), keyed by edge (== the edge id), so the
// renderer draws the wire tube from Go's segment. pulse-cancelled (Phase 3) tells the renderer to drop an
// in-flight bead's sprite (edge deleted mid-flight), keyed by source node+port.
export type TraceEvent =
  | { step: number; kind: "recv" | "fire"; node: string; port?: string; value?: number }
  | { step: number; kind: "send"; node: string; port?: string; value?: number; arcLength?: number; simLatencyMs?: number; target?: string; targetHandle?: string }
  | { step: number; kind: "done"; node: string; port: string }
  | { step: number; kind: "edge-bead"; node: string; port: string; value?: number; x: number; y: number; z: number; f: number; bead?: number }
  | { step: number; kind: "geometry"; edge: string; sx: number; sy: number; sz: number; ex: number; ey: number; ez: number }
  | { step: number; kind: "pulse-cancelled"; node: string; port: string; value?: number; bead?: number }
  | { step: number; kind: "arrive"; node: string; port: string; value?: number; bead?: number }
  | { step: number; kind: "node-geometry"; node: string; label?: string; nodeKind?: string; nx: number; ny: number; nz: number; radius: number; sphereR?: number; vrx: number; vry: number; vrz: number; frx: number; fry: number; frz: number; ports: { name: string; isInput: boolean; px: number; py: number; pz: number; dx: number; dy: number; dz: number }[] }
  | { step: number; kind: "node-bead"; node: string; row: number; col: number; present: boolean; value: number; x: number; y: number; z: number }
  | { step: number; kind: "camera"; px: number; py: number; pz: number; r: number; posTheta: number; posPhi: number; upTheta: number; upPhi: number }
  | { step: number; kind: "scene-tori"; visible: boolean }
  | { step: number; kind: "scene-poles"; visible: boolean }
  | { step: number; kind: "node-poles"; visible: boolean }
  | { step: number; kind: "angle-labels"; visible: boolean }
  | { step: number; kind: "sel-sphere-poles"; visible: boolean }
  | { step: number; kind: "handholds"; visible: boolean }
  | { step: number; kind: "labels-global"; visible: boolean }
  | { step: number; kind: "badges-global"; visible: boolean }
  | { step: number; kind: "overlays-vis"; visible: boolean }
  | { step: number; kind: "double-links"; visible: boolean }
  // Go-owned click-selection: the currently-selected node id (node="" clears it).
  | { step: number; kind: "select"; node: string }
  // node-status payload is GENERATED from Trace.go (schema/trace-event-fields.ts):
  // a Go field rename/retype regenerates NodeStatusEvent and breaks tsc here +
  // at pump.ts, closing the hand-authored field-drift gap.
  | NodeStatusEvent;

export type HostToWebviewMsg =
  | { type: "load"; text: string; sceneText?: string }
  | { type: "run-status"; state: RunStatus["state"]; message?: string }
  | { type: "flush" }
  | { type: "save-error"; message: string }
  | { type: "trace-event"; event: TraceEvent }
  // Phase 3: binary snapshot from Go's fd3 side channel.
  // The ArrayBuffer is transferred zero-copy (postMessage transferable).
  // Phase 5 will render from it; for now the webview stubs the handler.
  | { type: "buffer-snapshot"; buffer: ArrayBuffer }
  // New-system label sidecar: per-node {id,label} derived by the host from each
  // node-geometry trace event (which now carries the node's human label). The webview
  // records it into the row-keyed buffer-nav label table (first-seen order == buffer
  // node-row order), so the new render path resolves pill text without the old spec
  // store. One message per node id per run (host dedups); repopulated on resend.
  // `kind` is the node's Go KIND (PascalCase, e.g. "Hold") so the new render path can
  // map the row to its NODE_DEFS fill/stroke color; empty string when Go omits it.
  | { type: "node-label"; id: string; label: string; kind: string };

// Note: "resend" is host-originated (runner.resend() writes it straight to Go's
// stdin) and is never emitted by the webview. It is kept in this set so the
// message-kind-parity guard stays in lockstep with stdin_reader.go's "resend"
// kind (every kind Go reads on stdin has a seam here); the webview simply never
// sends it.
export const WEBVIEW_TO_HOST_TYPES: ReadonlySet<WebviewToHostMsg["type"]> = new Set([
  "ready", "run", "run-cancel", "play", "pause", "resume", "stop", "webview-log", "edit", "resend", "raw-input",
]);

const HOST_TO_WEBVIEW_TYPES: ReadonlySet<HostToWebviewMsg["type"]> = new Set([
  "load", "run-status", "flush", "save-error", "trace-event", "buffer-snapshot", "node-label",
]);

// parseEdit validates an "edit" message by its op, mirroring the per-op payloads
// in EditMsg (and Go's applyEdit). Returns undefined for an unknown op or a payload
// missing required fields, so a malformed edit is dropped rather than forwarded.
const OVERLAY_FLAGS: ReadonlySet<string> = new Set<OverlayFlag>(OVERLAY_FLAG_NAMES);

// Allowed camera viewpoint sub-kinds (derived from the single VIEWPOINT_KINDS source).
const VP_KIND_SET: ReadonlySet<string> = new Set<ViewpointKind>(VIEWPOINT_KINDS);

// Validates that v is a full OverlayState (every flag a boolean).
function isOverlayState(v: unknown): boolean {
  if (!v || typeof v !== "object") return false;
  const s = v as Record<string, unknown>;
  for (const flag of OVERLAY_FLAGS) {
    if (typeof s[flag] !== "boolean") return false;
  }
  return true;
}

// Validates the op="update" payload by entity kind (and attr where present).
function parseUpdate(m: Record<string, unknown>): WebviewToHostMsg | undefined {
  switch (m.kind) {
    case "node": {
      if (m.attr === "move") {
        // Entries-shaped node-move (decentralized): a non-empty map of routing key →
        // MoveEntry{nodeId,x,y,z}. Validate the map and every entry.
        const entries = m.entries;
        if (!entries || typeof entries !== "object") return undefined;
        const vals = Object.values(entries as Record<string, unknown>);
        if (vals.length === 0) return undefined;
        const ok = vals.every((v) => {
          if (!v || typeof v !== "object") return false;
          const e = v as Record<string, unknown>;
          return (
            typeof e.nodeId === "string" &&
            typeof e.x === "number" && Number.isFinite(e.x) &&
            typeof e.y === "number" && Number.isFinite(e.y) &&
            typeof e.z === "number" && Number.isFinite(e.z)
          );
        });
        return ok ? (m as unknown as WebviewToHostMsg) : undefined;
      }
      if (m.attr === "anchor") {
        // node/port strings, isInput boolean, anchor {x,y,z} numbers, keys non-empty string[].
        const a = m.anchor;
        const okAnchor =
          !!a &&
          typeof a === "object" &&
          typeof (a as Record<string, unknown>).x === "number" &&
          typeof (a as Record<string, unknown>).y === "number" &&
          typeof (a as Record<string, unknown>).z === "number";
        const keys = m.keys;
        const okKeys =
          Array.isArray(keys) && keys.length > 0 && keys.every((k) => typeof k === "string");
        return typeof m.node === "string" &&
          typeof m.port === "string" &&
          typeof m.isInput === "boolean" &&
          okAnchor &&
          okKeys
          ? (m as unknown as WebviewToHostMsg)
          : undefined;
      }
      return undefined;
    }
    case "edge": {
      // attr="faded": edges is Record<string, boolean>: edgeId → desired faded state.
      if (m.attr !== "faded") return undefined;
      const edgesMap = m.edges;
      if (!edgesMap || typeof edgesMap !== "object" || Array.isArray(edgesMap)) return undefined;
      const ok = Object.entries(edgesMap as Record<string, unknown>).every(
        ([k, v]) => typeof k === "string" && typeof v === "boolean",
      );
      return ok ? (m as unknown as WebviewToHostMsg) : undefined;
    }
    case "camera": {
      // viewpoint must be a nested object with a kind in the allowed VIEWPOINT_KINDS set;
      // an unknown kind is dropped rather than forwarded to Go (where it would no-op).
      const vp = m.viewpoint;
      if (!vp || typeof vp !== "object") return undefined;
      const vk = (vp as Record<string, unknown>).kind;
      if (typeof vk !== "string" || !VP_KIND_SET.has(vk)) return undefined;
      return (m as unknown as WebviewToHostMsg);
    }
    case "overlays": {
      if (m.attr === "toggle") {
        return typeof m.flag === "string" && OVERLAY_FLAGS.has(m.flag)
          ? (m as unknown as WebviewToHostMsg)
          : undefined;
      }
      if (m.attr === "set") {
        return isOverlayState(m.state) ? (m as unknown as WebviewToHostMsg) : undefined;
      }
      return undefined;
    }
    case "scene":
      return m.scene !== undefined ? (m as unknown as WebviewToHostMsg) : undefined;
    default:
      return undefined;
  }
}

const RAW_POINTER_KINDS: ReadonlySet<string> = new Set<RawPointerKind>([
  "pointerdown", "pointermove", "pointerup", "wheel", "home",
]);
const RAW_HIT_KINDS: ReadonlySet<string> = new Set<RawHit["kind"]>([
  "port", "handhold", "node", "empty",
]);

// parseRawInput validates a "raw-input" message: a raw pointer/wheel event with a
// classified raycast hit. All numeric fields must be finite; kind and hit.kind must be
// in their allowed sets. A malformed event is dropped rather than forwarded to Go.
function parseRawInput(m: Record<string, unknown>): WebviewToHostMsg | undefined {
  const ev = m.event;
  if (!ev || typeof ev !== "object") return undefined;
  const e = ev as Record<string, unknown>;
  const num = (v: unknown): boolean => typeof v === "number" && Number.isFinite(v);
  const bool = (v: unknown): boolean => typeof v === "boolean";
  if (typeof e.kind !== "string" || !RAW_POINTER_KINDS.has(e.kind)) return undefined;
  if (![e.x, e.y, e.rectLeft, e.rectTop, e.rectWidth, e.rectHeight, e.button, e.deltaX, e.deltaY, e.fov].every(num)) {
    return undefined;
  }
  if (![e.ctrl, e.shift, e.alt, e.meta].every(bool)) return undefined;
  const h = e.hit;
  if (!h || typeof h !== "object") return undefined;
  const hit = h as Record<string, unknown>;
  if (typeof hit.kind !== "string" || !RAW_HIT_KINDS.has(hit.kind)) return undefined;
  if (typeof hit.id !== "string" || !bool(hit.isInput)) return undefined;
  if (![hit.portRow, hit.x, hit.y, hit.z].every(num)) return undefined;
  return m as unknown as WebviewToHostMsg;
}

function parseEdit(m: Record<string, unknown>): WebviewToHostMsg | undefined {
  switch (m.op) {
    case "create":
    case "delete":
      return typeof m.target === "string" && typeof m.targetHandle === "string"
        ? (m as unknown as WebviewToHostMsg)
        : undefined;
    case "update":
      return parseUpdate(m);
    default:
      return undefined;
  }
}

export function parseWebviewToHost(raw: unknown): WebviewToHostMsg | undefined {
  if (!raw || typeof raw !== "object") return undefined;
  const t = (raw as { type?: unknown }).type;
  if (typeof t !== "string" || !WEBVIEW_TO_HOST_TYPES.has(t as WebviewToHostMsg["type"])) {
    return undefined;
  }
  const m = raw as Record<string, unknown>;
  switch (t) {
    case "run":
      return m as unknown as WebviewToHostMsg;
    case "webview-log":
      return typeof m.entry === "string" ? (m as unknown as WebviewToHostMsg) : undefined;
    case "edit":
      return parseEdit(m);
    case "raw-input":
      return parseRawInput(m);
    default:
      return m as unknown as WebviewToHostMsg;
  }
}

// Documented run-status states (RunStatus["state"] union), used to validate the
// run-status payload without duplicating the union literals.
const RUN_STATUS_STATES: ReadonlySet<string> = new Set([
  "running", "paused", "ok", "error", "cancelled",
]);

// parseHostToWebview validates a host→webview message by its type AND its payload,
// mirroring parseWebviewToHost's per-op shape checks. A malformed message (esp.
// type="trace-event" with a missing/malformed event) is dropped (undefined) rather
// than forwarded — the webview message listener drops undefined, so a bad envelope
// can never reach pump.ts (`const {step,kind}=event`) and throw, blanking the editor.
// Only the ENVELOPE is checked here (event is a non-null object with numeric step +
// string kind); full per-kind field validation lives in runCommand.ts/trace-event-fields.
export function parseHostToWebview(raw: unknown): HostToWebviewMsg | undefined {
  if (!raw || typeof raw !== "object") return undefined;
  const m = raw as Record<string, unknown>;
  const t = m.type;
  if (typeof t !== "string" || !HOST_TO_WEBVIEW_TYPES.has(t as HostToWebviewMsg["type"])) {
    return undefined;
  }
  switch (t) {
    case "load":
      // text is fed to JSON.parse in store.load(); require it to be a string here
      // (the single authoritative type check — store keeps its try/catch as
      // defense-in-depth). sceneText is optional and, if present, must be a string.
      if (typeof m.text !== "string") return undefined;
      if (m.sceneText !== undefined && typeof m.sceneText !== "string") return undefined;
      return m as unknown as HostToWebviewMsg;
    case "run-status":
      // state must be a documented RunStatus state; message (if present) a string.
      if (typeof m.state !== "string" || !RUN_STATUS_STATES.has(m.state)) return undefined;
      if (m.message !== undefined && typeof m.message !== "string") return undefined;
      return m as unknown as HostToWebviewMsg;
    case "save-error":
      // message is the error text — required string.
      return typeof m.message === "string" ? (m as unknown as HostToWebviewMsg) : undefined;
    case "flush":
      // No payload.
      return m as unknown as HostToWebviewMsg;
    case "trace-event": {
      // Minimal envelope pump.ts relies on: event is a non-null object carrying a
      // numeric step and a string kind. Per-kind fields are validated downstream.
      const ev = m.event;
      if (!ev || typeof ev !== "object") return undefined;
      const e = ev as Record<string, unknown>;
      if (typeof e.step !== "number" || !Number.isFinite(e.step)) return undefined;
      if (typeof e.kind !== "string") return undefined;
      return m as unknown as HostToWebviewMsg;
    }
    case "buffer-snapshot":
      // buffer must be an ArrayBuffer (transferred zero-copy from the host).
      return m.buffer instanceof ArrayBuffer ? (m as unknown as HostToWebviewMsg) : undefined;
    case "node-label":
      // id + label + kind are all required strings (the row-keyed sidecar payload).
      return typeof m.id === "string" && typeof m.label === "string" && typeof m.kind === "string"
        ? (m as unknown as HostToWebviewMsg)
        : undefined;
    default:
      return undefined;
  }
}
