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
  | { type: "delivered"; edge: string }
  | { type: "pseudo-render"; nodeId: string }
  | { type: "pseudo-save"; nodeId: string; pseudo: string };

// Mirrors Go Trace.Event shape. kind ∈ {"recv","fire","send","slot"}.
// recv/send carry port+value; fire carries only node; send also carries edge
// when the Go side has resolved it (currently omitted — raw form only).
// slot carries nodeId/port/phase and optionally value (filled only).
export type SlotPhase = "filled" | "empty";
export type SlotEntry = { phase: "filled"; value: number } | { phase: "empty" };
export type SlotMap = Record<string, SlotEntry>;

export type SlotEvent = {
  step: number;
  kind: "slot";
  nodeId: string;
  port: string;
  phase: SlotPhase;
  value?: number;
};

export type TraceEvent =
  | { step: number; kind: "recv" | "fire" | "send"; node: string; port?: string; edge?: string; value?: number }
  | { step: number; kind: "done"; node: string; port: string }
  | SlotEvent;

export type HostToWebviewMsg =
  | { type: "load"; text: string }
  | { type: "view-load"; text?: string }
  | { type: "run-status"; state: RunStatus["state"]; message?: string }
  | { type: "flush" }
  | { type: "save-error"; message: string }
  | { type: "trace-event"; event: TraceEvent }
  | { type: "pseudo-render-result"; nodeId: string; pseudo: string }
  | { type: "pseudo-save-result"; nodeId: string }
  | { type: "pseudo-error"; nodeId: string; message: string; suggestion?: string };

export const WEBVIEW_TO_HOST_TYPES: ReadonlySet<WebviewToHostMsg["type"]> = new Set([
  "ready", "save", "view-save", "run", "run-cancel", "pause", "resume", "stop", "webview-log", "delivered",
  "pseudo-render", "pseudo-save",
]);

export const HOST_TO_WEBVIEW_TYPES: ReadonlySet<HostToWebviewMsg["type"]> = new Set([
  "load", "view-load", "run-status", "flush", "save-error", "trace-event",
  "pseudo-render-result", "pseudo-save-result", "pseudo-error",
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
      return typeof m.edge === "string" ? (m as unknown as WebviewToHostMsg) : undefined;
    case "pseudo-render":
      return typeof m.nodeId === "string" ? (m as unknown as WebviewToHostMsg) : undefined;
    case "pseudo-save":
      return typeof m.nodeId === "string" && typeof m.pseudo === "string"
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
