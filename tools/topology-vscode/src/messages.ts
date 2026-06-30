// Shared discriminated unions for webview ↔ extension-host messaging.
// Both sides import from here so unknown / malformed messages are caught
// at type-narrow time rather than silently writing `[object Object]` to disk.

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

// OverlayFlag is the wire vocabulary of named boolean overlay attributes, shared
// with Go's overlayToggles map (stdin_reader.go). Guarded by check-edit-op-parity.sh.
// OVERLAY_FLAGS_START
export type OverlayFlag =
  | "tori"
  | "scenePoles"
  | "nodePoles"
  | "angleLabels"
  | "selSpherePoles"
  | "handholds"
  | "labelsGlobal"
  | "badgesGlobal"
  | "overlays"
  | "doubleLinks";
// OVERLAY_FLAGS_END

// OverlayState is the full explicit-visibility snapshot pushed on load (attr="set").
export type OverlayState = {
  tori: boolean; scenePoles: boolean; nodePoles: boolean; angleLabels: boolean;
  selSpherePoles: boolean; handholds: boolean; doubleLinks: boolean;
  labelsGlobal: boolean; badgesGlobal: boolean; overlays: boolean;
};

// VP_KINDS_START
export type ViewpointPayload =
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
// VP_KINDS_END

export type EditMsg =
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

export type WebviewToHostMsg =
  | { type: "ready" }
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
  | { step: number; kind: "node-geometry"; node: string; nx: number; ny: number; nz: number; radius: number; sphereR?: number; vrx: number; vry: number; vrz: number; frx: number; fry: number; frz: number; ports: { name: string; isInput: boolean; px: number; py: number; pz: number; dx: number; dy: number; dz: number }[] }
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
  | { step: number; kind: "double-links"; visible: boolean };

export type HostToWebviewMsg =
  | { type: "load"; text: string; sceneText?: string }
  | { type: "run-status"; state: RunStatus["state"]; message?: string }
  | { type: "flush" }
  | { type: "save-error"; message: string }
  | { type: "trace-event"; event: TraceEvent };

export const WEBVIEW_TO_HOST_TYPES: ReadonlySet<WebviewToHostMsg["type"]> = new Set([
  "ready", "run", "run-cancel", "play", "pause", "resume", "resend", "stop", "webview-log", "edit",
]);

export const HOST_TO_WEBVIEW_TYPES: ReadonlySet<HostToWebviewMsg["type"]> = new Set([
  "load", "run-status", "flush", "save-error", "trace-event",
]);

// parseEdit validates an "edit" message by its op, mirroring the per-op payloads
// in EditMsg (and Go's applyEdit). Returns undefined for an unknown op or a payload
// missing required fields, so a malformed edit is dropped rather than forwarded.
const OVERLAY_FLAGS: ReadonlySet<string> = new Set<OverlayFlag>([
  "tori", "scenePoles", "nodePoles", "angleLabels", "selSpherePoles",
  "handholds", "labelsGlobal", "badgesGlobal", "overlays", "doubleLinks",
]);

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
      // viewpoint must be a nested object with a string kind discriminator.
      const vp = m.viewpoint;
      if (!vp || typeof vp !== "object") return undefined;
      if (typeof (vp as Record<string, unknown>).kind !== "string") return undefined;
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
    default:
      return m as unknown as WebviewToHostMsg;
  }
}

export function parseHostToWebview(raw: unknown): HostToWebviewMsg | undefined {
  if (!raw || typeof raw !== "object") return undefined;
  const t = (raw as { type?: unknown }).type;
  if (typeof t !== "string" || !HOST_TO_WEBVIEW_TYPES.has(t as HostToWebviewMsg["type"])) {
    return undefined;
  }
  return raw as HostToWebviewMsg;
}
