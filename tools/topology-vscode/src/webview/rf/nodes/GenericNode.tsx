// Generic RF custom node. Driven by NODE_DEFS keyed on the node's RF type.
// One component for all substrate kinds. Static render only: handles,
// label, optional sublabel, optional data display, fire-flash via boxShadow.

import { Handle, Position, type NodeProps } from "reactflow";
import type { CSSProperties, ReactNode } from "react";
import { useFireFlash, LAST_FIRE_FIELD } from "./use-fire-flash";
import { NODE_DEFS, type DisplayKind, type NodeDef } from "./node-defs";

interface GenericNodeData {
  label?: string;
  lastFire?: number;
  initialQueue?: unknown[];
  repeat?: boolean;
  nodeData?: { held?: unknown };
}

export function GenericNode({ type, data }: NodeProps<GenericNodeData>) {
  const def = NODE_DEFS[type];
  const flashing = useFireFlash(data[LAST_FIRE_FIELD]);
  if (!def) return <div style={{ padding: 4, color: "#c62828", fontFamily: "monospace" }}>unknown kind: {type}</div>;
  const targets = def.targets ?? [];
  const sources = def.sources ?? [];
  const container: CSSProperties = {
    background: def.bg,
    border: `1px solid ${def.border}`,
    borderRadius: 4,
    padding: "6px 10px",
    minWidth: def.minWidth ?? 70,
    fontFamily: "monospace",
    fontSize: 11,
    color: def.text,
    boxShadow: flashing ? `0 0 8px 2px ${def.border}` : undefined,
  };
  return (
    <div style={container}>
      {targets.map((p, i) => (
        <Handle key={p.id} type="target" position={Position.Left} id={p.id} style={handleStyle(p.accent ?? def.accent, i, targets.length)} />
      ))}
      <div style={{ fontWeight: 700, color: def.accent, textAlign: "center" }}>{data.label ?? def.defaultLabel}</div>
      {def.sublabel && <div style={SUBLABEL}>{def.sublabel}</div>}
      {def.displays?.map((d) => renderDisplay(d, data))}
      {sources.map((p, i) => (
        <Handle key={p.id} type="source" position={Position.Right} id={p.id} style={handleStyle(p.accent ?? def.accent, i, sources.length)} />
      ))}
    </div>
  );
}

function handleStyle(bg: string, i: number, n: number): CSSProperties {
  const s: CSSProperties = { background: bg };
  if (n > 1) s.top = `${((i + 1) / (n + 1)) * 100}%`;
  return s;
}

const SUBLABEL: CSSProperties = { fontSize: 9, color: "#8b949e", textAlign: "center" };

function renderDisplay(kind: DisplayKind, data: GenericNodeData): ReactNode {
  if (kind === "queue") {
    const q = data.initialQueue ?? [];
    const s = q.length > 0 ? q.map((v) => JSON.stringify(v)).join(", ") : "—";
    return <div key="queue" style={{ fontSize: 10, color: "#8b949e", wordBreak: "break-all", maxWidth: 160 }} title="init queue">[{s}]</div>;
  }
  if (kind === "repeat") {
    return data.repeat ? <div key="repeat" style={{ fontSize: 9, color: "#58a6ff", marginTop: 2 }}>↺ repeat</div> : null;
  }
  if (kind === "held") {
    const h = data.nodeData?.held;
    return h !== undefined ? <div key="held" style={{ fontSize: 9, color: "#8b949e", textAlign: "center", marginTop: 2 }}>held={JSON.stringify(h)}</div> : null;
  }
  return null;
}

export type { NodeDef };
