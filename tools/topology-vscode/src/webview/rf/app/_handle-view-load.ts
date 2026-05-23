import { specToFlow } from "../adapter";
import { markViewSynced, scheduleViewSave } from "../../save";
import { setViewerState } from "../viewer-state";
import { getFolds, setFolds } from "../folds-state";
import { getDimmed } from "../dimmed";
import { parseViewerState, serializeViewerState } from "../../state/viewer/types";
import { resolveViewLoadViewport } from "./_resolve-view-load-viewport";
import { reconcileSelection } from "./_reconcile-selection";
import { postLog } from "../../log/post";
import type { AppCtx } from "./_ctx";

// "view-load" sidecar message: install viewerState, reconcile selection
// against the live node set, and apply persisted camera (with legacy
// SVG-viewBox migration if needed).
export function handleViewLoad(ctx: AppCtx, text: string | undefined) {
  const next = parseViewerState(text);
  if (next.edges && ctx.lastSpec.current) {
    const knownEdgeIds = ctx.lastSpec.current.edges.map((e) => e.id);
    for (const key of Object.keys(next.edges)) {
      if (!knownEdgeIds.includes(key)) {
        console.warn(`topology.view.json: edge key "${key}" has no matching edge in spec`);
        postLog("view.orphan-edge-key", { key, knownEdgeIds });
      }
    }
  }
  setViewerState(next);
  setFolds(next.folds ?? []);
  markViewSynced(text ?? serializeViewerState(next));
  // If the spec already loaded, rebuild now so the freshly-installed
  // viewerState (positions, folds, sublabels, edge routes) is applied.
  // The "load" message races ahead of the async sidecar read and runs
  // specToFlow with empty viewerState, so without this rebuild every
  // node falls back to its default position (visually: stacked at the
  // origin) whenever the sidecar lacks folds.
  if (ctx.lastSpec.current) {
    const flow = specToFlow(ctx.lastSpec.current, getFolds(), next, next.lastSelectionIds ?? [], getDimmed());
    ctx.setNodes(flow.nodes);
    ctx.setEdges(flow.edges);
  }
  // Always reconcile selection — empty case clears stale `selected`
  // flags from a prior render.
  ctx.setNodes((ns) => {
    if (ns.length === 0) return ns;
    const reconciled = reconcileSelection(next.lastSelectionIds, ns.map((n) => n.id));
    const want = new Set(reconciled);
    return ns.map((n) => {
      const wantSel = want.has(n.id);
      return n.selected === wantSel ? n : { ...n, selected: wantSel };
    });
  });
  const paneRect = ctx.paneRef.current?.getBoundingClientRect() ?? null;
  const resolution = resolveViewLoadViewport(next.camera, paneRect);
  if (resolution.kind === "direct") {
    ctx.rf.setViewport(resolution.viewport);
  } else if (resolution.kind === "migrated") {
    // Legacy SVG viewBox sidecar — apply the migrated viewport and rewrite
    // next.camera in canonical form so the next save persists the new shape.
    ctx.rf.setViewport(resolution.viewport);
    next.camera = resolution.viewport;
    scheduleViewSave();
  }
}
