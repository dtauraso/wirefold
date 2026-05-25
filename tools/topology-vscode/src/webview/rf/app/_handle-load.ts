import { parseSpec, requiredInputDiagnostics, type Spec } from "../../../schema";
import { postLog } from "../../log/post";
import { specToFlow } from "../adapter";
import { viewerState, patchViewerState } from "../viewer-state";
import { getFolds } from "../../rf/folds-state";
import { getDimmed } from "../../rf/dimmed";
import { scheduleViewSave, setSpecMeta } from "../../save";
import { migrateLegacyFields } from "./_migrate-legacy-fields";
import { reconcileSelection } from "./_reconcile-selection";
import type { AppCtx } from "./_ctx";

// Fresh "load" message: parse, install spec, kick the renderer, then
// reconcile any persisted selection against the new node set so stale
// ids from a prior session don't leak through as ghost selections.
export function handleLoad(ctx: AppCtx, text: string) {
  try {
    const rawJson = JSON.parse(text);
    // One-shot migration: extract x/y/sublabel/state/route from legacy spec
    // fields and seed viewerState before parseSpec drops them.
    let migrated = false;
    patchViewerState((v) => {
      const nodesBefore = JSON.stringify(v.nodes);
      const edgesBefore = JSON.stringify(v.edges);
      migrateLegacyFields(rawJson, v);
      if (JSON.stringify(v.nodes) !== nodesBefore || JSON.stringify(v.edges) !== edgesBefore) {
        migrated = true;
      }
    });
    if (migrated) scheduleViewSave();
    const next: Spec = parseSpec(rawJson);
    setSpecMeta(next);
    ctx.lastSpec.current = next;
    postLog("load", { nodes: next.nodes.length, edges: next.edges.length });
    postLog("validation-diag", { flagged: [...requiredInputDiagnostics(next).keys()] });
    const flow = specToFlow(next, getFolds(), viewerState, viewerState.lastSelectionIds ?? [], getDimmed());
    const filtered = reconcileSelection(viewerState.lastSelectionIds, flow.nodes.map((n) => n.id));
    const sel = new Set(filtered);
    if (sel.size > 0) {
      flow.nodes = flow.nodes.map((n) => sel.has(n.id) ? { ...n, selected: true } : n);
    }
    // Transient run state (pulse, lastFire, slots) lives in dedicated state stores
    // outside RF nodes/edges, so it survives file round-trip rebuilds automatically.
    ctx.setNodes(flow.nodes);
    ctx.setEdges(flow.edges);
  } catch (err) {
    console.error("invalid topology.json", err);
  }
}
