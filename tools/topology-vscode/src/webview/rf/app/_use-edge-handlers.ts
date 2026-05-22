import { useCallback, useState } from "react";
import type { Connection, Edge as RFEdge } from "reactflow";
import { useReactFlow, useUpdateNodeInternals } from "reactflow";
import type { EdgeKind } from "../../../schema";
import { KIND_COLORS } from "../../../schema";
import { scheduleSave } from "../../save";
import { rfGetEdges, rfSetEdges } from "../rf-imperative";
import { pushSnapshot } from "../history";
import type { AppCtx } from "./_ctx";
import { onConnectImpl } from "./_on-connect";
import { onReconnectImpl } from "./_on-reconnect";

export type EdgeMenu = { x: number; y: number; edgeId: string } | null;

type Side = "left" | "right" | "top" | "bottom";

export function useEdgeHandlers(ctx: AppCtx) {
  const [edgeMenu, setEdgeMenu] = useState<EdgeMenu>(null);
  const rf = useReactFlow();
  const updateNodeInternals = useUpdateNodeInternals();

  // Input ports are 1-to-1: each target.handle is a single chan field on
  // the runtime node struct, so two senders into the same port can't be
  // wired. Returning false makes ReactFlow skip onConnect / onReconnect.
  // Exception: __grow handles are always valid — they represent a new port
  // that doesn't exist yet, so no existing edge can occupy them.
  const isValidConnection = useCallback((conn: Connection) => {
    if (!conn.target || !conn.targetHandle) return false;
    if (conn.targetHandle.startsWith("__grow:")) return true;
    return !rfGetEdges().some(
      (e) => e.target === conn.target && e.targetHandle === conn.targetHandle,
    );
  }, []);

  const onConnect = useCallback((conn: Connection) => onConnectImpl(ctx, conn), [ctx]);
  const onReconnectStart = useCallback(() => { ctx.reconnectOk.current = false; }, [ctx]);
  const onReconnect = useCallback(
    (oldEdge: RFEdge, conn: Connection) => onReconnectImpl(ctx, oldEdge, conn),
    [ctx],
  );
  // Drop-in-empty-space leaves the edge untouched (reroute, not delete).
  const onReconnectEnd = useCallback(() => { ctx.reconnectOk.current = false; }, [ctx]);

  const onEdgeContextMenu = useCallback((ev: React.MouseEvent, edge: RFEdge) => {
    ev.preventDefault();
    setEdgeMenu({ x: ev.clientX, y: ev.clientY, edgeId: edge.id });
  }, []);

  const closeEdgeMenu = useCallback(() => setEdgeMenu(null), []);

  const setEdgeKind = useCallback((edgeId: string, kind: EdgeKind) => {
    if (!ctx.lastSpec.current) return;
    if (!rfGetEdges().some((e) => e.id === edgeId)) return;
    pushSnapshot();
    rfSetEdges((es) => es.map((e) =>
      e.id !== edgeId ? e : {
        ...e,
        style: { ...e.style, stroke: KIND_COLORS[kind] ?? "#888" },
        data: { ...e.data, kind },
      }
    ));
    scheduleSave();
    setEdgeMenu(null);
  }, [ctx]);

  const setEdgeMidpointOffset = useCallback((edgeId: string, midpointOffset: number) => {
    if (!ctx.lastSpec.current) return;
    if (!rfGetEdges().some((e) => e.id === edgeId)) return;
    pushSnapshot();
    rfSetEdges((es) => es.map((e) =>
      e.id !== edgeId ? e : { ...e, data: { ...e.data, midpointOffset } }
    ));
    scheduleSave();
  }, [ctx]);

  const setPortPosition = useCallback((nodeId: string, portName: string, side: Side, slot: 0 | 1 | 2) => {
    if (!ctx.lastSpec.current) return;
    pushSnapshot();
    rf.setNodes((nodes) => nodes.map((nd) => {
      if (nd.id !== nodeId) return nd;
      const allPorts = [...(nd.data?.inputs ?? []), ...(nd.data?.outputs ?? [])];
      const dragged = allPorts.find((p) => p.name === portName); if (!dragged) return nd;
      const isInput = (nd.data?.inputs ?? []).some((p) => p.name === portName);
      const oldSide: Side = (dragged.side as Side | undefined) ?? (isInput ? "left" : "right");
      const oldSlot: 0 | 1 | 2 = dragged.slot ?? 1;
      const occupant = allPorts.find((p) => {
        if (p.name === portName) return false;
        const pIsInput = (nd.data?.inputs ?? []).some((x) => x.name === p.name);
        return ((p.side as Side | undefined) ?? (pIsInput ? "left" : "right")) === side && (p.slot ?? 1) === slot;
      });
      const patch = (p: { name: string; side?: unknown; slot?: unknown }) => {
        if (p.name === portName) return { ...p, side, slot };
        if (occupant && p.name === occupant.name) return { ...p, side: oldSide, slot: oldSlot };
        return p;
      };
      return { ...nd, data: { ...nd.data,
        inputs: (nd.data?.inputs ?? []).map(patch),
        outputs: (nd.data?.outputs ?? []).map(patch),
      } };
    }));
    updateNodeInternals(nodeId);
    scheduleSave();
  }, [ctx, rf, updateNodeInternals]);

  return {
    edgeMenu, isValidConnection, onConnect, onReconnectStart, onReconnect,
    onReconnectEnd, onEdgeContextMenu, closeEdgeMenu, setEdgeKind, setEdgeMidpointOffset, setPortPosition,
  };
}
