// Helpers for embedding viewer state under the `view` key of topology.json,
// and for reading/writing the scene sidecar (topology.scene.json).
//
// SPLIT:
//   topology.json#view  → diagram fields: nodes (positions), directlyFadedNodes,
//                          directlyFadedEdges, fadeEdgeOrder. Go reads view.nodes.
//   topology.scene.json → scene fields (flat top-level): camera, camera3d,
//                          labelsGlobalHidden, badgesHidden. Gitignored; absent on fresh clone.

// The scene-owned keys (stored flat in topology.scene.json).
const SCENE_KEYS = new Set(["camera", "camera3d", "labelsGlobalHidden", "badgesHidden"]);

// Extract the `view` key from topology.json text, with scene keys removed.
// Returns undefined when absent / not parseable.
export function extractViewText(docText: string): string | undefined {
  try {
    const raw = JSON.parse(docText);
    if (raw && typeof raw === "object" && raw.view !== null && typeof raw.view === "object") {
      // Strip scene keys — they live in topology.scene.json now.
      const diagramView: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(raw.view as Record<string, unknown>)) {
        if (!SCENE_KEYS.has(k)) diagramView[k] = v;
      }
      return JSON.stringify(diagramView, null, 2) + "\n";
    }
    return undefined;
  } catch {
    return undefined;
  }
}

// Inject (or replace) the `view` key in the topology.json text.
// viewText is the serialised diagram ViewerState (positions + fades — no scene keys).
// Returns the updated topology.json text, or the original if parsing fails.
export function injectViewText(docText: string, viewText: string): string {
  try {
    const raw = JSON.parse(docText);
    if (!raw || typeof raw !== "object") return docText;
    const view = JSON.parse(viewText);
    if (view === null || typeof view !== "object") return docText;
    // Merge: keep any existing scene keys already in view (shouldn't be any after
    // migration, but guard for safety), then overlay diagram keys.
    const existingView = (raw.view && typeof raw.view === "object") ? raw.view as Record<string, unknown> : {};
    const merged: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(existingView)) {
      if (SCENE_KEYS.has(k)) merged[k] = v; // preserve legacy scene keys if present
    }
    for (const [k, v] of Object.entries(view as Record<string, unknown>)) {
      if (!SCENE_KEYS.has(k)) merged[k] = v; // diagram keys only
    }
    raw.view = merged;
    return JSON.stringify(raw, null, 2) + "\n";
  } catch {
    return docText;
  }
}

// ---------------------------------------------------------------------------
// Scene sidecar (topology.scene.json) helpers
// ---------------------------------------------------------------------------

// Parse a flat topology.scene.json text into its scene fields.
// Returns {} on any error or missing file (safe default).
export function parseSceneText(sceneText: string | undefined): Record<string, unknown> {
  if (!sceneText) return {};
  try {
    const raw = JSON.parse(sceneText);
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return {};
    // Allow only the expected scene keys through.
    const out: Record<string, unknown> = {};
    for (const k of SCENE_KEYS) {
      if (k in (raw as Record<string, unknown>)) out[k] = (raw as Record<string, unknown>)[k];
    }
    return out;
  } catch {
    return {};
  }
}

// Serialise scene fields (camera, camera3d, labelsGlobalHidden) to a flat JSON string.
export function serializeSceneText(fields: Record<string, unknown>): string {
  const out: Record<string, unknown> = {};
  for (const k of SCENE_KEYS) {
    if (k in fields && fields[k] !== undefined) out[k] = fields[k];
  }
  return JSON.stringify(out, null, 2) + "\n";
}
