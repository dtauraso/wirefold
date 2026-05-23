// Generic RF custom node. Driven by NODE_DEFS keyed on the node's RF type.
// One component for all substrate kinds. Handles, label, sublabel, data
// displays, fire-flash. Connected handles support drag-to-move: snap to
// 12 positions (3 slots × 4 sides), swap occupants on collision, persist
// side+slot per-port in the spec via setPortPosition action.

import { type NodeProps, useStore, useReactFlow } from "reactflow";
import { shallow } from "zustand/shallow";
import type { CSSProperties, PointerEvent } from "react";
import { useRef, useState, useCallback } from "react";
import { useFireFlash } from "./use-fire-flash";
import { useHeldValuesCtx } from "../held-values";
import { useLastFireCtx } from "../fire-flash-state";
import { useSlotsCtx } from "../slots-state";
import { NODE_DEFS, type NodeDef } from "./node-defs";
import type { NodeData } from "../types";
import type { Port } from "../../../schema/types";
import { useEdgeActions } from "../app/_edge-actions-ctx";
import { type Side, type ActiveDrag, computeSnapPoints, nearestSnap } from "../port-snap";
import { SUBLABEL } from "./node-styles";
import { renderPortHandles, renderDefHandles, renderSnapDots, renderDisplay } from "./node-render-helpers";

export function GenericNode({ id: nodeId, type, data }: NodeProps<NodeData>) {
  const def = NODE_DEFS[type];
  const lastFireMap = useLastFireCtx();
  const flashing = useFireFlash(lastFireMap.get(nodeId));
  const slotsMap = useSlotsCtx();
  const heldValues = useHeldValuesCtx();
  const actions = useEdgeActions();
  const rf = useReactFlow();
  const nodeElRef = useRef<HTMLDivElement | null>(null);
  const [drag, setDrag] = useState<ActiveDrag | null>(null);

  const connected = useStore((s) => {
    const r: Record<string, boolean> = {};
    for (const p of data.inputs ?? [])  r[p.name] = s.edges.some((e) => e.target === nodeId && e.targetHandle === p.name);
    for (const p of data.outputs ?? []) r[p.name] = s.edges.some((e) => e.source === nodeId && e.sourceHandle === p.name);
    return r;
  }, shallow);

  const handlePointerDown = useCallback((e: PointerEvent<HTMLDivElement>, portName: string, curSide: Side, curSlot: 0|1|2) => {
    if (!connected[portName]) return;
    e.stopPropagation(); e.preventDefault();
    (e.target as HTMLElement).setPointerCapture(e.pointerId);
    nodeElRef.current = (e.currentTarget as HTMLElement).closest(".react-flow__node") as HTMLDivElement | null;
    const w = data.width ?? 110; const h = data.height ?? 60;
    setDrag({ portName, oldSide: curSide, oldSlot: curSlot, nearestSide: curSide, nearestSlot: curSlot });
    const getRect = () => nodeElRef.current?.getBoundingClientRect();
    const onMove = (ev: globalThis.PointerEvent) => {
      const rect = getRect(); if (!rect) return;
      const n = nearestSnap(computeSnapPoints(rect, w, h), ev.clientX, ev.clientY);
      setDrag((d) => d ? { ...d, nearestSide: n.side, nearestSlot: n.slot } : d);
    };
    const onUp = (ev: globalThis.PointerEvent) => {
      window.removeEventListener("pointermove", onMove); window.removeEventListener("pointerup", onUp);
      const rect = getRect(); if (!rect) { setDrag(null); return; }
      const n = nearestSnap(computeSnapPoints(rect, w, h), ev.clientX, ev.clientY);
      setDrag(null);
      if (n.side === curSide && n.slot === curSlot) return;
      rf.setNodes((nodes) => nodes.map((nd) => {
        if (nd.id !== nodeId) return nd;
        const all = [...(nd.data?.inputs ?? []), ...(nd.data?.outputs ?? [])];
        const occupant = all.find((p) => {
          if (p.name === portName) return false;
          const inp = (nd.data?.inputs ?? []).some((x: Port) => x.name === p.name);
          return ((p.side as Side|undefined) ?? (inp ? "left" : "right")) === n.side && (p.slot ?? 1) === n.slot;
        });
        const patch = (p: Port): Port => {
          if (p.name === portName) return { ...p, side: n.side, slot: n.slot };
          if (occupant && p.name === occupant.name) return { ...p, side: curSide, slot: curSlot };
          return p;
        };
        return { ...nd, data: { ...nd.data, inputs: (nd.data?.inputs ?? []).map(patch), outputs: (nd.data?.outputs ?? []).map(patch) } };
      }));
      actions?.setPortPosition(nodeId, portName, n.side, n.slot);
    };
    window.addEventListener("pointermove", onMove); window.addEventListener("pointerup", onUp);
  }, [connected, data.width, data.height, data.inputs, data.outputs, nodeId, rf, actions]);

  if (!def) return <div style={{ padding: 4, color: "#c62828", fontFamily: "monospace" }}>unknown kind: {type}</div>;

  const inputs: Port[] = data.inputs ?? []; const outputs: Port[] = data.outputs ?? [];
  const hasPortData = inputs.length > 0 || outputs.length > 0;
  const container: CSSProperties = { background: def.bg, border: `1px solid ${def.border}`, borderRadius: 4, padding: "4px 8px", minWidth: def.minWidth ?? 70, minHeight: def.height ?? 40, fontSize: 11, color: def.text, boxShadow: flashing ? `0 0 8px 2px ${def.accent}` : undefined };
  return (
    <div ref={nodeElRef} style={container}>
      {hasPortData ? renderPortHandles(inputs, outputs, def, drag, handlePointerDown, slotsMap.get(nodeId), nodeId, heldValues) : renderDefHandles(def)}
      {renderSnapDots(drag)}
      <div style={{ fontWeight: 500, textAlign: "center" }}>{data.label ?? def.defaultLabel}</div>
      {def.sublabel && <div style={SUBLABEL}>{def.sublabel}</div>}
      {def.displays?.map((d) => renderDisplay(d, data))}
    </div>
  );
}

export type { NodeDef };
