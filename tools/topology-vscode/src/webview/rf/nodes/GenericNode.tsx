// Generic RF custom node. Driven by NODE_DEFS keyed on the node's RF type.
// One component for all substrate kinds. Handles, label, sublabel, data
// displays, fire-flash. Connected handles support drag-to-move: snap to
// 12 positions (3 slots × 4 sides), swap occupants on collision, persist
// side+slot per-port in the spec via setPortPosition action.

import { Handle, Position, type NodeProps, useStore, useReactFlow } from "reactflow";
import { shallow } from "zustand/shallow";
import type { CSSProperties, PointerEvent, ReactNode } from "react";
import React, { useRef, useState, useCallback } from "react";
import { useFireFlash } from "./use-fire-flash";
import { useHeldValuesCtx } from "../held-values";
import { useLastFireCtx } from "../fire-flash-state";
import { useSlotsCtx } from "../slots-state";
import { NODE_DEFS, type NodeDef } from "./node-defs";
import type { NodeData } from "../types";
import { KIND_COLORS } from "../../../schema";
import type { Port } from "../../../schema/types";
import type { SlotMap } from "../../../messages";
import { useEdgeActions } from "../app/_edge-actions-ctx";
import { type Side, type ActiveDrag, SLOT_PCT, computeSnapPoints, nearestSnap, resolvePositions, pctToSlot } from "../port-snap";

// ── Style helpers ────────────────────────────────────────────────────────────

const SIDE_POS: Record<Side, Position> = {
  left: Position.Left,
  right: Position.Right,
  top: Position.Top,
  bottom: Position.Bottom,
};

const SUBLABEL: CSSProperties = { fontSize: 9, color: "#666", textAlign: "center" };

function badgeStyle(side: Side, pct: number): CSSProperties {
  const iv = side === "left" || side === "right";
  const offset = side === "left" ? { left: 12 } : side === "right" ? { right: 12 } : {};
  return {
    position: "absolute",
    ...(iv ? { top: `${pct}%`, transform: "translateY(-50%)" } : { left: `${pct}%`, transform: "translateX(-50%)" }),
    ...offset,
    background: "#1a237e",
    color: "#fff",
    fontFamily: "monospace",
    fontSize: 9,
    padding: "1px 3px",
    borderRadius: 3,
    pointerEvents: "none",
    zIndex: 10,
    whiteSpace: "nowrap",
  } as CSSProperties;
}

function portStyle(side: Side, pct: number, color: string): CSSProperties {
  const iv = side === "left" || side === "right";
  return { [side]: -5, ...(iv ? { top: `${pct}%` } : { left: `${pct}%` }), width: 8, height: 8, background: color, border: "1px solid #333" } as CSSProperties;
}

function simpleStyle(bg: string, i: number, n: number): CSSProperties {
  const s: CSSProperties = { background: bg };
  if (n > 1) s.top = `${((i + 1) / (n + 1)) * 100}%`;
  return s;
}

// ── Render helpers ───────────────────────────────────────────────────────────

function renderPortHandles(
  inputs: Port[], outputs: Port[], def: NodeDef, drag: ActiveDrag | null,
  onPointerDown: (e: PointerEvent<HTMLDivElement>, name: string, side: Side, slot: 0|1|2) => void,
  slots?: SlotMap,
  nodeId?: string,
  heldValues?: ReadonlyMap<string, unknown>,
) {
  const buckets: Record<Side, { port: Port; isInput: boolean }[]> = { left: [], right: [], top: [], bottom: [] };
  for (const p of inputs)  buckets[(p.side as Side) ?? "left"].push({ port: p, isInput: true });
  for (const p of outputs) buckets[(p.side as Side) ?? "right"].push({ port: p, isInput: false });
  return (["left","right","top","bottom"] as Side[]).flatMap((side) => {
    const bucket = buckets[side];
    const positions = resolvePositions(bucket.map((x) => x.port));
    return bucket.map(({ port: p, isInput }, i) => {
      const pct = positions[i];
      const curSide = (p.side as Side|undefined) ?? (isInput ? "left" : "right");
      const curSlot: 0|1|2 = p.slot ?? pctToSlot(pct);
      const color = (KIND_COLORS as Record<string, string>)[p.kind] ?? def.accent ?? "#888";
      const slotEntry = slots?.[p.name];
      const showSlotBadge = isInput && slotEntry?.phase === "filled";
      const heldKey = nodeId ? `${nodeId}:${p.name}` : undefined;
      const heldVal = heldKey ? heldValues?.get(heldKey) : undefined;
      const showHeldBadge = isInput && heldVal !== undefined;
      return (
        <React.Fragment key={`${side}-${p.name}`}>
          <Handle id={p.name} type={isInput ? "target" : "source"} position={SIDE_POS[side]} style={{ ...portStyle(side, pct, color), opacity: drag?.portName === p.name ? 0.4 : 1 }} onPointerDown={(e) => onPointerDown(e, p.name, curSide, curSlot)} />
          {showSlotBadge && <div style={badgeStyle(side, pct)}>{slotEntry.value}</div>}
          {showHeldBadge && !showSlotBadge && (
            <div style={{ ...badgeStyle(side, pct), background: "#4a148c", opacity: 0.85 }}>{String(heldVal)}</div>
          )}
        </React.Fragment>
      );
    });
  });
}

function renderDefHandles(def: NodeDef) {
  const targets = def.targets ?? []; const sources = def.sources ?? [];
  return [
    ...targets.map((p, i) => <Handle key={p.id} type="target" position={Position.Left} id={p.id} style={simpleStyle(p.accent ?? def.accent, i, targets.length)} />),
    ...sources.map((p, i) => <Handle key={p.id} type="source" position={Position.Right} id={p.id} style={simpleStyle(p.accent ?? def.accent, i, sources.length)} />),
  ];
}

function renderSnapDots(drag: ActiveDrag | null) {
  if (!drag) return null;
  return (["left","right","top","bottom"] as Side[]).flatMap((side) =>
    ([0,1,2] as const).map((slot) => {
      const pct = SLOT_PCT[slot];
      const isNearest = drag.nearestSide === side && drag.nearestSlot === slot;
      const iv = side === "left" || side === "right";
      const dotStyle: CSSProperties = {
        position: "absolute", width: 6, height: 6, borderRadius: "50%",
        background: isNearest ? "rgba(80,160,255,0.9)" : "rgba(120,120,200,0.35)",
        pointerEvents: "none",
        ...(iv ? { [side]: -3, top: `${pct}%`, transform: "translate(-50%,-50%)" }
          : side === "top" ? { top: -3, left: `${pct}%`, transform: "translate(-50%,-50%)" }
          : { bottom: -3, left: `${pct}%`, transform: "translate(-50%,50%)" }),
      };
      return <div key={`snap-${side}-${slot}`} style={dotStyle} />;
    })
  );
}

function renderDisplay(kind: string, data: NodeData): ReactNode {
  if (kind === "queue") {
    const q = (data.nodeData as { init?: unknown[] }|undefined)?.init ?? [];
    const s = q.length > 0 ? q.map((v) => JSON.stringify(v)).join(", ") : "—";
    return <div key="queue" style={{ fontSize: 10, color: "#444", fontFamily: "monospace", wordBreak: "break-all", maxWidth: 160, textAlign: "center" }} title="init queue">[{s}]</div>;
  }
  if (kind === "repeat") {
    const repeat = (data.props as { repeat?: boolean }|undefined)?.repeat ?? (data.nodeData as { repeat?: boolean }|undefined)?.repeat;
    return repeat ? <div key="repeat" style={{ fontSize: 9, color: "#666", marginTop: 2, textAlign: "center" }}>↺ repeat</div> : null;
  }
  if (kind === "held") {
    const h = (data.nodeData as { held?: unknown }|undefined)?.held;
    return h !== undefined ? <div key="held" style={{ fontSize: 9, color: "#444", fontFamily: "monospace", textAlign: "center", marginTop: 2 }}>held={JSON.stringify(h)}</div> : null;
  }
  return null;
}

// ── Component ────────────────────────────────────────────────────────────────

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
