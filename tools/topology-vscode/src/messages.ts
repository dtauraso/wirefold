// Shared discriminated unions for webview ↔ extension-host messaging.
// Both sides import from here so unknown / malformed messages are caught
// at type-narrow time rather than silently writing `[object Object]` to disk.

export type RunStatus =
  | { state: "running" }
  | { state: "ok" }
  | { state: "error"; message: string }
  | { state: "cancelled" };

export type WebviewToHostMsg =
  | { type: "ready" }
  | { type: "save"; text: string }
  | { type: "view-save"; text: string }
  | { type: "run"; text?: string }
  | { type: "run-cancel" }
  | { type: "webview-log"; entry: string };

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
  | SlotEvent;

export type HostToWebviewMsg =
  | { type: "load"; text: string }
  | { type: "view-load"; text?: string }
  | { type: "run-status"; state: RunStatus["state"]; message?: string }
  | { type: "flush" }
  | { type: "save-error"; message: string }
  | { type: "trace-event"; event: TraceEvent };

export const WEBVIEW_TO_HOST_TYPES: ReadonlySet<WebviewToHostMsg["type"]> = new Set([
  "ready", "save", "view-save", "run", "run-cancel", "webview-log",
]);

export const HOST_TO_WEBVIEW_TYPES: ReadonlySet<HostToWebviewMsg["type"]> = new Set([
  "load", "view-load", "run-status", "flush", "save-error", "trace-event",
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
