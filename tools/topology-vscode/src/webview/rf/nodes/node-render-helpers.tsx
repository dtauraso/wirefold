// Render helpers for GenericNode: port handles, def handles, snap dots, displays.
// Split from GenericNode.tsx to stay under the 100-LOC refactor target.

import { Handle, Position } from "reactflow";
import type { CSSProperties, PointerEvent, ReactNode } from "react";
import React from "react";
import type { SlotMap } from "../../../messages";
import type { NodeDef } from "./node-defs";
import type { NodeData } from "../types";
import { KIND_COLORS } from "../../../schema";
import type { Port } from "../../../schema/types";
import { type Side, type ActiveDrag, SLOT_PCT, resolvePositions, pctToSlot } from "../port-snap";
import { SIDE_POS, badgeStyle, portStyle, simpleStyle } from "./node-styles";

export function renderPortHandles(
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

export function renderDefHandles(def: NodeDef) {
  const targets = def.targets ?? []; const sources = def.sources ?? [];
  return [
    ...targets.map((p, i) => <Handle key={p.id} type="target" position={Position.Left} id={p.id} style={simpleStyle(p.accent ?? def.accent, i, targets.length)} />),
    ...sources.map((p, i) => <Handle key={p.id} type="source" position={Position.Right} id={p.id} style={simpleStyle(p.accent ?? def.accent, i, sources.length)} />),
  ];
}

export function renderSnapDots(drag: ActiveDrag | null) {
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

export function renderDisplay(kind: string, data: NodeData): ReactNode {
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
