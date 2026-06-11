// Shared discriminated unions for webview ↔ extension-host messaging.
// Both sides import from here so unknown / malformed messages are caught
// at type-narrow time rather than silently writing `[object Object]` to disk.

export type RunStatus =
  | { state: "running" }
  | { state: "paused" }
  | { state: "ok" }
  | { state: "error"; message: string }
  | { state: "cancelled" };

// Geometry-CRUD / animation edit sent webview → host → Go. ONE message kind
// ("edit") with an `op` discriminator, mirroring the single Go stdin "edit" shape
// (nodes/Wiring/stdin_reader.go applyEdit). Go owns the clock; this seam carries no
// delivery signal. ops: create/delete a wire (edge add/remove), update node
// geometry (node-move), fade an edge set.
//
// For op="update" (node-move): the decentralized Go path mail-sorts the move to the
// owning node + each incident edge goroutine, so the entries map is keyed by the
// moved node id AND each incident edge id (source===moved || target===moved). Every
// entry carries the same moved node id + new position. The webview computes the
// incident edges from its React Flow graph (TS owns the graph; Go owns the recompute).
export type MoveEntry = { nodeId: string; x: number; y: number; z: number };
export type EditMsg =
  | { type: "edit"; op: "create"; target: string; targetHandle: string }
  | { type: "edit"; op: "delete"; target: string; targetHandle: string }
  | { type: "edit"; op: "update"; entries: Record<string, MoveEntry> }
  | { type: "edit"; op: "fade"; edges: Record<string, boolean> };

export type WebviewToHostMsg =
  | { type: "ready" }
  | { type: "save"; text: string }
  | { type: "view-save"; text: string }
  | { type: "run"; text?: string }
  | { type: "run-cancel" }
  | { type: "play" }
  | { type: "pause" }
  | { type: "resume" }
  | { type: "stop" }
  | { type: "webview-log"; entry: string }
  | EditMsg;

// Mirrors Go Trace.Event shape. kind ∈
// {"recv","fire","send","done","position","geometry","pulse-cancelled"}.
// recv/send carry port+value; fire carries only node; send also carries edge
// when the Go side has resolved it (currently omitted — raw form only).
// position (Phase 2) carries the bead's Go-computed 3-D world position (x,y,z) plus
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
  | { step: number; kind: "position"; node: string; port: string; value?: number; x: number; y: number; z: number; f: number }
  | { step: number; kind: "geometry"; edge: string; sx: number; sy: number; sz: number; ex: number; ey: number; ez: number }
  | { step: number; kind: "pulse-cancelled"; node: string; port: string; value?: number }
  | { step: number; kind: "arrive"; node: string; port: string; value?: number }
  | { step: number; kind: "node-geometry"; node: string; nx: number; ny: number; nz: number; ports: { name: string; isInput: boolean; px: number; py: number; pz: number; dx: number; dy: number; dz: number }[] };

export type HostToWebviewMsg =
  | { type: "load"; text: string; sceneText?: string }
  | { type: "run-status"; state: RunStatus["state"]; message?: string }
  | { type: "flush" }
  | { type: "save-error"; message: string }
  | { type: "trace-event"; event: TraceEvent };

export const WEBVIEW_TO_HOST_TYPES: ReadonlySet<WebviewToHostMsg["type"]> = new Set([
  "ready", "save", "view-save", "run", "run-cancel", "play", "pause", "resume", "stop", "webview-log", "edit",
]);

export const HOST_TO_WEBVIEW_TYPES: ReadonlySet<HostToWebviewMsg["type"]> = new Set([
  "load", "run-status", "flush", "save-error", "trace-event",
]);

// parseEdit validates an "edit" message by its op, mirroring the per-op payloads
// in EditMsg (and Go's applyEdit). Returns undefined for an unknown op or a payload
// missing required fields, so a malformed edit is dropped rather than forwarded.
function parseEdit(m: Record<string, unknown>): WebviewToHostMsg | undefined {
  switch (m.op) {
    case "create":
    case "delete":
      return typeof m.target === "string" && typeof m.targetHandle === "string"
        ? (m as unknown as WebviewToHostMsg)
        : undefined;
    case "update": {
      // Entries-shaped node-move (decentralized): a non-empty map of routing key →
      // MoveEntry{nodeId,x,y,z}. Validate the map and at least one well-formed entry.
      const entries = m.entries;
      if (!entries || typeof entries !== "object") return undefined;
      const vals = Object.values(entries as Record<string, unknown>);
      if (vals.length === 0) return undefined;
      const ok = vals.every((v) => {
        if (!v || typeof v !== "object") return false;
        const e = v as Record<string, unknown>;
        return typeof e.nodeId === "string" && typeof e.x === "number" && typeof e.y === "number";
      });
      return ok ? (m as unknown as WebviewToHostMsg) : undefined;
    }
    case "fade": {
      // edges is Record<string, boolean>: edgeId → desired faded state.
      const edgesMap = m.edges;
      if (!edgesMap || typeof edgesMap !== "object" || Array.isArray(edgesMap)) return undefined;
      const ok = Object.entries(edgesMap as Record<string, unknown>).every(
        ([k, v]) => typeof k === "string" && typeof v === "boolean",
      );
      return ok ? (m as unknown as WebviewToHostMsg) : undefined;
    }
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
    case "save":
    case "view-save":
      return typeof m.text === "string" ? (m as unknown as WebviewToHostMsg) : undefined;
    case "run":
      return m.text === undefined || typeof m.text === "string"
        ? (m as unknown as WebviewToHostMsg)
        : undefined;
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
