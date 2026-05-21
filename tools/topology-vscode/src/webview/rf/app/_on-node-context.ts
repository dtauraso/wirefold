import { useCallback } from "react";
import type { Node as RFNode } from "reactflow";
import { createFold } from "../../state/ops/fold";
import { beginEditSublabel } from "../../inline-edit";
import { flushViewSave } from "../../save";
import { mutateViewer, viewerState } from "../viewer-state";
import { rfGetNodes } from "../rf-imperative";
import { pushSnapshot } from "../history";
import { toggleFoldCollapse, getFolds, setFolds } from "../folds-state";
import type { AppCtx } from "./_ctx";

export function useNodeContextHandlers(ctx: AppCtx) {
  const onNodeDoubleClick = useCallback((ev: React.MouseEvent, node: RFNode) => {
    // Fold placeholder → toggle collapsed state. Expanded folds aren't
    // selectable, so dbl-click never fires on them; collapsing again
    // uses the right-click "fold selection" path on regular nodes.
    if (node.type === "fold") {
      pushSnapshot();
      const ok = toggleFoldCollapse(node.id);
      if (ok) {
        const f = getFolds().find((x) => x.id === node.id);
        console.info(`[fold] toggled ${node.id} -> collapsed=${f?.collapsed}`);
        ctx.rebuildFlow();
        flushViewSave();
      }
      return;
    }
    const t = ev.target as HTMLElement | null;
    const sublabelEl = t?.closest<HTMLElement>(".node-sublabel");
    if (sublabelEl) {
      beginEditSublabel(node.id, sublabelEl);
    }
  }, [ctx]);

  const foldCurrentSelection = useCallback(() => {
    const sel = new Set(viewerState.lastSelectionIds ?? []);
    const rfNodes = rfGetNodes();
    const memberIds = Array.from(sel).filter((id) => rfNodes.some((n) => n.id === id));
    if (memberIds.length < 2) {
      console.info(`[fold] need >=2 nodes selected to fold; have ${memberIds.length}`);
      return;
    }
    let cx = 0, cy = 0;
    for (const id of memberIds) {
      const nv = viewerState.nodes?.[id];
      if (nv) { cx += nv.x; cy += nv.y; }
    }
    cx = cx / memberIds.length;
    cy = cy / memberIds.length;
    pushSnapshot();
    const draft = { folds: getFolds() } as { folds: ReturnType<typeof getFolds> };
    const id = createFold(draft as Parameters<typeof createFold>[0], memberIds, [cx, cy]);
    if (!id) {
      console.info(
        `[fold] createFold rejected (members may already be inside another fold): ${memberIds.join(", ")}`,
      );
      return;
    }
    setFolds(draft.folds ?? []);
    console.info(`[fold] created ${id} with ${memberIds.length} members: ${memberIds.join(", ")}`);
    ctx.rebuildFlow();
    flushViewSave();
  }, [ctx]);

  const onSelectionContextMenu = useCallback(
    (ev: React.MouseEvent, _selNodes: RFNode[]) => {
      ev.preventDefault();
      foldCurrentSelection();
    },
    [foldCurrentSelection],
  );

  const onNodeContextMenu = useCallback((ev: React.MouseEvent, node: RFNode) => {
    ev.preventDefault();
    if (node.type === "fold") {
      // Right-click on a collapsed fold placeholder toggles it open/closed.
      pushSnapshot();
      const ok = toggleFoldCollapse(node.id);
      if (ok) {
        const f = getFolds().find((x) => x.id === node.id);
        console.info(`[fold] right-click toggled ${node.id} -> collapsed=${f?.collapsed}`);
        ctx.rebuildFlow();
        flushViewSave();
      }
      return;
    }
    const sel = new Set(viewerState.lastSelectionIds ?? []);
    if (!sel.has(node.id)) {
      console.info(
        `[fold] right-clicked node "${node.id}" is not in the selection (${sel.size} selected); shift-click to add it before folding`,
      );
      return;
    }
    foldCurrentSelection();
  }, [foldCurrentSelection]);

  return { onNodeDoubleClick, onSelectionContextMenu, onNodeContextMenu };
}
