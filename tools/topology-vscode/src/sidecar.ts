// Helpers for embedding viewer state under the `view` key of topology.json.
// The sidecar (topology.view.json) was removed in Step 4 of collapse-representations;
// viewer state now lives at topology.json#view so there is only one file to edit.

// Extract the `view` key from the topology.json text, serialised as a string
// (or undefined when absent / not parseable).
export function extractViewText(docText: string): string | undefined {
  try {
    const raw = JSON.parse(docText);
    if (raw && typeof raw === "object" && raw.view !== null && typeof raw.view === "object") {
      return JSON.stringify(raw.view, null, 2) + "\n";
    }
    return undefined;
  } catch {
    return undefined;
  }
}

// Inject (or replace) the `view` key in the topology.json text.
// viewText is the serialised ViewerState (same format the webview produces).
// Returns the updated topology.json text, or the original if parsing fails.
export function injectViewText(docText: string, viewText: string): string {
  try {
    const raw = JSON.parse(docText);
    if (!raw || typeof raw !== "object") return docText;
    const view = JSON.parse(viewText);
    if (view === null || typeof view !== "object") return docText;
    raw.view = view;
    return JSON.stringify(raw, null, 2) + "\n";
  } catch {
    return docText;
  }
}
