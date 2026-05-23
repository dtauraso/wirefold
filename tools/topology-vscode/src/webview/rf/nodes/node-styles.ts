// Style helpers for GenericNode and its render helpers.
// Kept separate to stay under the 100-LOC refactor target.

import type { CSSProperties } from "react";
import { Position } from "reactflow";
import type { Side } from "../port-snap";

export const SIDE_POS: Record<Side, Position> = {
  left: Position.Left,
  right: Position.Right,
  top: Position.Top,
  bottom: Position.Bottom,
};

export const SUBLABEL: CSSProperties = { fontSize: 9, color: "#666", textAlign: "center" };

export function badgeStyle(side: Side, pct: number): CSSProperties {
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

export function portStyle(side: Side, pct: number, color: string): CSSProperties {
  const iv = side === "left" || side === "right";
  return { [side]: -5, ...(iv ? { top: `${pct}%` } : { left: `${pct}%` }), width: 8, height: 8, background: color, border: "1px solid #333" } as CSSProperties;
}

export function simpleStyle(bg: string, i: number, n: number): CSSProperties {
  const s: CSSProperties = { background: bg };
  if (n > 1) s.top = `${((i + 1) / (n + 1)) * 100}%`;
  return s;
}
