// Transport for the webview's structured log channel. Posts one JSON
// entry to the extension host, which routes it to .probe/ts.jsonl or
// .probe/ts-errors.jsonl. Replaces the slog() side-channel from the
// pre-collapse webview.
//
// Failure is swallowed: a logging path that throws would mask the real
// error it was trying to report.

import { vscode } from "../vscode-api";

export function postLog(label: string, data?: Record<string, unknown>): void {
  const stepVal = typeof data?.step === "number" ? data.step
    : typeof data?.simStep === "number" ? data.simStep
    : undefined;
  const entry = JSON.stringify({
    ts_ms: Date.now(),
    src: "ts-webview",
    ...(stepVal !== undefined ? { step: stepVal } : {}),
    label,
    ...data,
  });
  console.log(`[wirefold] ${label}`, data ?? {});
  if (typeof window === "undefined") return;
  try {
    (vscode as unknown as { postMessage(msg: unknown): void }).postMessage({ type: "webview-log", entry });
  } catch {
    /* swallow */
  }
}
