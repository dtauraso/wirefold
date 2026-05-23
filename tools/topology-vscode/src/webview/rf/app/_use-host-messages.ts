import { useEffect } from "react";
import { setInlineEditRerender } from "../../inline-edit";
import { vscode } from "../../vscode-api";
import { viewerState } from "../viewer-state";
import { rfGetNodes, rfGetEdges } from "../rf-imperative";
import { flowToSpec } from "../adapter/flow-to-spec";
import { getFolds } from "../folds-state";
import { getDimmed } from "../dimmed";
import { specToFlow } from "../adapter";
import type { AppCtx } from "./_ctx";
import { handleLoad } from "./_handle-load";
import { handleViewLoad } from "./_handle-view-load";
import { installHostMessageRouter } from "./_install-host-message-router";
import { handleTraceEvent } from "../pump";

export function useHostMessages(ctx: AppCtx) {
  // Inline-edit rerender hook: after an inline edit commits (rename/sublabel),
  // RF state is already updated. Rebuild the flow from the current RF nodes/edges
  // so specToFlow can apply viewer-state decorations (folds, dimming, etc.).
  useEffect(() => {
    const rerenderFromRF = () => {
      const nodes = rfGetNodes();
      const edges = rfGetEdges();
      const next = flowToSpec(nodes, edges, { nodes: [], edges: [] });
      ctx.lastSpec.current = next;
      const flow = specToFlow(next, getFolds(), viewerState, viewerState.lastSelectionIds ?? [], getDimmed());
      ctx.setNodes(flow.nodes);
      ctx.setEdges(flow.edges);
    };
    setInlineEditRerender(rerenderFromRF);
  }, [ctx]);

  useEffect(() => {
    return installHostMessageRouter(
      {
        addEventListener: (t, h) => window.addEventListener(t, h as EventListener),
        removeEventListener: (t, h) => window.removeEventListener(t, h as EventListener),
        postMessage: (m) => vscode.postMessage(m),
      },
      {
        load: (text) => handleLoad(ctx, text),
        viewLoad: (text) => handleViewLoad(ctx, text),
        traceEvent: handleTraceEvent,
      },
    );
  }, [ctx]);
}
