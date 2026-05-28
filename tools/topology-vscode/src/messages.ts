// Shared discriminated unions for webview ↔ extension-host messaging.
// Both sides import from here so unknown / malformed messages are caught
// at type-narrow time rather than silently writing `[object Object]` to disk.

export type RunStatus =
  | { state: "running" }
  | { state: "paused" }
  | { state: "ok" }
  | { state: "error"; message: string }
  | { state: "cancelled" };

// Curated pseudo-editable subset of registered node kinds. InhibitRightGate is
// intentionally excluded (not pseudo-editable). PseudoKind (below) is derived
// from these keys, and handle-message.ts's pseudoTable is typed
// Record<PseudoKind, ...> — so adding a kind here without a pseudoTable entry
// is a compile error.
export const PSEUDO_KIND_PREFIX = { Input: "pseudo", ReadGate: "readgate", ChainInhibitor: "chaininhibitor" } as const;
export type PseudoKind = keyof typeof PSEUDO_KIND_PREFIX;
export type PseudoPrefix = (typeof PSEUDO_KIND_PREFIX)[PseudoKind];
export function pseudoMsgTypes(prefix: PseudoPrefix) {
  return {
    render:       `${prefix}-render`,
    save:         `${prefix}-save`,
    renderResult: `${prefix}-render-result`,
    saveResult:   `${prefix}-save-result`,
    error:        `${prefix}-error`,
  } as const;
}

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
  | { type: "fade"; edges: string[] }
  | { type: `${PseudoPrefix}-render`; nodeId: string }
  | { type: `${PseudoPrefix}-save`; nodeId: string; pseudo: string };

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
  | { type: "run-status"; state: RunStatus["state"]; message?: string }
  | { type: "flush" }
  | { type: "save-error"; message: string }
  | { type: "trace-event"; event: TraceEvent }
  | { type: `${PseudoPrefix}-render-result`; nodeId: string; pseudo: string }
  | { type: `${PseudoPrefix}-save-result`; nodeId: string }
  | { type: `${PseudoPrefix}-error`; nodeId: string; message: string; suggestion?: string };

const _pseudoPrefixes = Object.values(PSEUDO_KIND_PREFIX) as PseudoPrefix[];

export const ALL_PSEUDO_ERROR_TYPES: ReadonlySet<string> = new Set(
  _pseudoPrefixes.map((p) => `${p}-error`)
);
export const ALL_PSEUDO_SAVE_RESULT_TYPES: ReadonlySet<string> = new Set(
  _pseudoPrefixes.map((p) => `${p}-save-result`)
);

// Reverse map: prefix string → PseudoKind (e.g. "pseudo" → "Input", "readgate" → "ReadGate")
export const PSEUDO_PREFIX_TO_KIND: Readonly<Record<PseudoPrefix, PseudoKind>> = Object.fromEntries(
  (Object.entries(PSEUDO_KIND_PREFIX) as [PseudoKind, PseudoPrefix][]).map(([k, p]) => [p, k])
) as Record<PseudoPrefix, PseudoKind>;

// Render-type set and save-type set for data-driven dispatch.
export const ALL_PSEUDO_RENDER_TYPES: ReadonlySet<string> = new Set(
  _pseudoPrefixes.map((p) => `${p}-render`)
);
export const ALL_PSEUDO_SAVE_TYPES: ReadonlySet<string> = new Set(
  _pseudoPrefixes.map((p) => `${p}-save`)
);

export const WEBVIEW_TO_HOST_TYPES: ReadonlySet<WebviewToHostMsg["type"]> = new Set([
  "ready", "save", "view-save", "run", "run-cancel", "pause", "resume", "stop", "webview-log", "delivered", "fade",
  ..._pseudoPrefixes.flatMap((p) => [`${p}-render`, `${p}-save`] as const),
]);

export const HOST_TO_WEBVIEW_TYPES: ReadonlySet<HostToWebviewMsg["type"]> = new Set([
  "load", "run-status", "flush", "save-error", "trace-event",
  ..._pseudoPrefixes.flatMap((p) => [`${p}-render-result`, `${p}-save-result`, `${p}-error`] as const),
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
    default: {
      // Handle pseudo render/save types derived from PSEUDO_KIND_PREFIX
      const renderTypes = new Set(_pseudoPrefixes.map((p) => `${p}-render`));
      const saveTypes   = new Set(_pseudoPrefixes.map((p) => `${p}-save`));
      if (renderTypes.has(t)) {
        return typeof m.nodeId === "string" ? (m as unknown as WebviewToHostMsg) : undefined;
      }
      if (saveTypes.has(t)) {
        return typeof m.nodeId === "string" && typeof m.pseudo === "string"
          ? (m as unknown as WebviewToHostMsg)
          : undefined;
      }
      return m as unknown as WebviewToHostMsg;
    }
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
