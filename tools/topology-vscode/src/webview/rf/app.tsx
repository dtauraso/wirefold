import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ReactFlowProvider, applyEdgeChanges, applyNodeChanges, useReactFlow,
  type Edge as RFEdge, type EdgeChange, type Node as RFNode, type NodeChange,
} from "reactflow";
import { specToFlow } from "./adapter";
import { RunButton } from "./panels/RunButton";
import { SaveLifecycle } from "../SaveLifecycle";
import { viewerState } from "./viewer-state";
import { getFolds } from "./folds-state";
import { isLegacyCamera } from "../state/viewer/types";
import { AppView } from "./app/AppView";
import { decorate } from "./app/_decorate";
import { useDeleteHandlers } from "./app/_handle-delete";
import { useDragDrop } from "./app/_use-drag-drop";
import { useEdgeHandlers } from "./app/_use-edge-handlers";
import { useFitViewHotkeys } from "./app/_use-fit-view";
import { useHostMessages } from "./app/_use-host-messages";
import { useInnerState } from "./app/_use-inner-state";
import { useNodeContextHandlers } from "./app/_on-node-context";
import { useNodeDrag } from "./app/_on-node-drag";
import { useUndoRedo } from "./app/_use-undo-redo";
import type { AppCtx } from "./app/_ctx";
import { EdgeActionsCtx } from "./app/_edge-actions-ctx";
import { registerRFSetters, notifyRFState } from "./rf-imperative";
import { registerHistory, undo as rfUndo, redo as rfRedo } from "./history";
import { registerRunStatusSetter, RunStatusCtx } from "./run-status";
import type { RunStatusUI } from "./run-status";
import { registerDimmedSetter, DimmedCtx, useDimmedCtx } from "./dimmed";
import { registerHeldValuesSetter, HeldValuesCtx } from "./held-values";
import type { HeldValues } from "./held-values";
import { useHotkeys } from "react-hotkeys-hook";

function Inner() {
  const [nodes, setNodes] = useState<RFNode[]>([]);
  const [edges, setEdges] = useState<RFEdge[]>([]);
  const dimmed = useDimmedCtx();

  const rf = useReactFlow();
  // Expose setNodes/setEdges imperatively for non-React callers (inline-edit).
  useEffect(() => { registerRFSetters(setNodes, setEdges); }, []);
  // Keep module-level snapshots current so rfGetNodes/rfGetEdges are fresh.
  useEffect(() => { notifyRFState(nodes, edges); }, [nodes, edges]);
  // Register RF instance for snapshot-based history.
  useEffect(() => { registerHistory(rf); }, [rf]);
  // RF-snapshot undo/redo — runs alongside the existing Zustand-backed hotkeys.
  useHotkeys("mod+z", (e) => { e.preventDefault(); rfUndo(); }, { enableOnContentEditable: false });
  useHotkeys("mod+shift+z, mod+y", (e) => { e.preventDefault(); rfRedo(); }, { enableOnContentEditable: false });
  const s = useInnerState();
  const [guides, setGuides] = useState<{ vx: number | null; hy: number | null }>({ vx: null, hy: null });

  // Hydrate RF viewport from persisted camera on mount. view-load will
  // overwrite this once the sidecar message arrives; this covers the gap
  // before that message fires (or when there is no sidecar).
  useEffect(() => {
    const cam = viewerState.camera;
    if (!cam || isLegacyCamera(cam)) return;
    rf.setViewport({ x: cam.x, y: cam.y, zoom: cam.zoom });
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const rebuildFlow = useCallback(() => {
    if (!s.lastSpec.current) return;
    const flow = specToFlow(s.lastSpec.current, getFolds(), viewerState, viewerState.lastSelectionIds ?? [], dimmed);
    setNodes(flow.nodes);
    setEdges(flow.edges);
  }, [s.lastSpec, dimmed]);

  const ctx: AppCtx = useMemo(() => ({
    setNodes, setEdges,
    lastSpec: s.lastSpec, reconnectOk: s.reconnectOk, paneRef: s.paneRef,
    flashIdsRef: s.flashIdsRef, flashTimerRef: s.flashTimerRef,
    rebuildFlow, rf,
  }), [rebuildFlow, rf, s.flashIdsRef, s.flashTimerRef, s.lastSpec, s.paneRef, s.reconnectOk]);

  useUndoRedo(ctx, true);
  useFitViewHotkeys(rf);
  useHostMessages(ctx);

  const onNodesChange = useCallback(
    (c: NodeChange[]) => setNodes((ns) => applyNodeChanges(c, ns)), []);
  const onEdgesChange = useCallback(
    (c: EdgeChange[]) => setEdges((es) => applyEdgeChanges(c, es)), []);

  const edgeH = useEdgeHandlers(ctx);
  const delH = useDeleteHandlers(ctx);
  useEffect(() => {
    const handler = (ev: KeyboardEvent) => {
      if (ev.key !== "Backspace" && ev.key !== "Delete") return;
      const target = ev.target as HTMLElement | null;
      if (target && (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.isContentEditable)) return;
      const selectedFolds = nodes.filter((n) => n.type === "fold" && n.selected);
      if (selectedFolds.length === 0) return;
      ev.preventDefault();
      ev.stopPropagation();
      delH.onNodesDelete(selectedFolds);
    };
    window.addEventListener("keydown", handler, { capture: true });
    return () => window.removeEventListener("keydown", handler, { capture: true } as EventListenerOptions);
  }, [nodes, delH]);
  const ddH = useDragDrop(ctx);
  const dragH = useNodeDrag(ctx, guides, setGuides);
  const ctxH = useNodeContextHandlers(ctx);

  const styled = decorate(nodes, edges, dimmed);
  const edgeActions = useMemo(() => ({ setEdgeMidpointOffset: edgeH.setEdgeMidpointOffset, setPortPosition: edgeH.setPortPosition }), [edgeH.setEdgeMidpointOffset, edgeH.setPortPosition]);

  return (
    <EdgeActionsCtx.Provider value={edgeActions}>
    <AppView
      paneRef={s.paneRef}
      styledNodes={styled.nodes} styledEdges={styled.edges}
      guides={guides} edgeMenu={edgeH.edgeMenu}
      onNodesChange={onNodesChange} onEdgesChange={onEdgesChange}
      onNodeDoubleClick={ctxH.onNodeDoubleClick}
      onNodeContextMenu={ctxH.onNodeContextMenu}
      onSelectionContextMenu={ctxH.onSelectionContextMenu}
      onNodeDragStart={dragH.onNodeDragStart}
      onNodeDrag={dragH.onNodeDrag} onNodeDragStop={dragH.onNodeDragStop}
      onNodesDelete={delH.onNodesDelete} onEdgesDelete={delH.onEdgesDelete}
      onConnect={edgeH.onConnect} isValidConnection={edgeH.isValidConnection}
      onReconnect={edgeH.onReconnect}
      onReconnectStart={edgeH.onReconnectStart} onReconnectEnd={edgeH.onReconnectEnd}
      onEdgeContextMenu={edgeH.onEdgeContextMenu}
      closeEdgeMenu={edgeH.closeEdgeMenu} setEdgeKind={edgeH.setEdgeKind}
      onDragOver={ddH.onDragOver} onDrop={ddH.onDrop}
    />
    </EdgeActionsCtx.Provider>
  );
}

export default function App() {
  const [runStatus, setRunStatus] = useState<RunStatusUI>({ state: "idle" });
  const [dimmed, setDimmed] = useState<Set<string> | null>(null);
  const [heldValues, setHeldValues] = useState<HeldValues>(new Map());
  useEffect(() => { registerRunStatusSetter(setRunStatus); }, []);
  useEffect(() => { registerDimmedSetter(setDimmed); }, []);
  useEffect(() => { registerHeldValuesSetter(setHeldValues); }, []);
  return (
    <HeldValuesCtx.Provider value={heldValues}>
    <DimmedCtx.Provider value={dimmed}>
    <RunStatusCtx.Provider value={runStatus}>
      <ReactFlowProvider>
        <SaveLifecycle />
        <Inner />
        <RunButton />
      </ReactFlowProvider>
    </RunStatusCtx.Provider>
    </DimmedCtx.Provider>
    </HeldValuesCtx.Provider>
  );
}
