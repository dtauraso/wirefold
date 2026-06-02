// Shared discriminated unions for webview ↔ extension-host messaging.
// Both sides import from here so unknown / malformed messages are caught
// at type-narrow time rather than silently writing `[object Object]` to disk.

export type RunStatus =
  | { state: "running" }
  | { state: "paused" }
  | { state: "ok" }
  | { state: "error"; message: string }
  | { state: "cancelled" };

export type WebviewToHostMsg =
  | { type: "ready" }
  | { type: "save"; text: string }
  | { type: "view-save"; text: string }
  | { type: "run"; text?: string }
  | { type: "run-cancel" }
  | { type: "pause" }
  | { type: "resume" }
  | { type: "stop" }
  | { type: "webview-log"; entry: string }
  | { type: "delivered"; target: string; targetHandle: string }
  | { type: "fade"; edges: string[] }
  | { type: "deleteEdge"; target: string; targetHandle: string }
  | { type: "addEdge"; target: string; targetHandle: string }
  | { type: "node-move"; nodeId: string; x: number; y: number; z?: number };

// Mirrors Go Trace.Event shape. kind ∈ {"recv","fire","send","done"}.
// recv/send carry port+value; fire carries only node; send also carries edge
// when the Go side has resolved it (currently omitted — raw form only).
export type TraceEvent =
  | { step: number; kind: "recv" | "fire"; node: string; port?: string; value?: number }
  | { step: number; kind: "send"; node: string; port?: string; edge?: string; value?: number; arcLength?: number; simLatencyMs?: number; target?: string; targetHandle?: string }
  | { step: number; kind: "done"; node: string; port: string };

export type HostToWebviewMsg =
  | { type: "load"; text: string }
  | { type: "run-status"; state: RunStatus["state"]; message?: string }
  | { type: "flush" }
  | { type: "save-error"; message: string }
  | { type: "trace-event"; event: TraceEvent };

export const WEBVIEW_TO_HOST_TYPES: ReadonlySet<WebviewToHostMsg["type"]> = new Set([
  "ready", "save", "view-save", "run", "run-cancel", "pause", "resume", "stop", "webview-log", "delivered", "fade", "deleteEdge", "addEdge", "node-move",
]);

export const HOST_TO_WEBVIEW_TYPES: ReadonlySet<HostToWebviewMsg["type"]> = new Set([
  "load", "run-status", "flush", "save-error", "trace-event",
]);

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
    case "delivered":
    case "deleteEdge":
    case "addEdge":
      return typeof m.target === "string" && typeof m.targetHandle === "string" ? (m as unknown as WebviewToHostMsg) : undefined;
    case "node-move":
      return typeof m.nodeId === "string" && typeof m.x === "number" && typeof m.y === "number"
        ? (m as unknown as WebviewToHostMsg)
        : undefined;
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
