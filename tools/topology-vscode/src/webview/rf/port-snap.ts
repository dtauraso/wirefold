// Drag-to-move port snap helpers for GenericNode.
// Pure computation — no React dependencies.

export type Side = "left" | "right" | "top" | "bottom";

export interface SnapPoint { side: Side; slot: 0 | 1 | 2; x: number; y: number }

export interface ActiveDrag {
  portName: string;
  oldSide: Side;
  oldSlot: 0 | 1 | 2;
  nearestSide: Side;
  nearestSlot: 0 | 1 | 2;
}

// 3 evenly-spaced snap slots: 25%, 50%, 75%
export const SLOT_PCT: [number, number, number] = [25, 50, 75];

export function pctToSlot(pct: number): 0 | 1 | 2 {
  if (pct <= 30) return 0;
  if (pct <= 62) return 1;
  return 2;
}

export function computeSnapPoints(rect: DOMRect, w: number, h: number): SnapPoint[] {
  const pts: SnapPoint[] = [];
  const sides: Side[] = ["left", "right", "top", "bottom"];
  for (const side of sides) {
    for (let s = 0 as 0 | 1 | 2; s <= 2; s = (s + 1) as 0 | 1 | 2) {
      const pct = SLOT_PCT[s] / 100;
      let x: number, y: number;
      if (side === "left")       { x = rect.left;  y = rect.top + pct * h; }
      else if (side === "right") { x = rect.right; y = rect.top + pct * h; }
      else if (side === "top")   { x = rect.left + pct * w; y = rect.top; }
      else                       { x = rect.left + pct * w; y = rect.bottom; }
      pts.push({ side, slot: s, x, y });
    }
  }
  return pts;
}

export function nearestSnap(pts: SnapPoint[], cx: number, cy: number): SnapPoint {
  let best = pts[0]; let bestD = Infinity;
  for (const p of pts) {
    const d = (p.x - cx) ** 2 + (p.y - cy) ** 2;
    if (d < bestD) { bestD = d; best = p; }
  }
  return best;
}

// Resolve pct positions for ports assigned to a single side.
// When ≤3 ports, respect slot assignments and fill gaps.
// When >3, fall back to uniform spacing.
export function resolvePositions(ports: { slot?: 0 | 1 | 2 }[]): number[] {
  const total = ports.length;
  if (total <= 3) {
    const claimed = new Map<number, number>();
    for (let i = 0; i < ports.length; i++) {
      const s = ports[i].slot;
      if (s !== undefined) claimed.set(s, i);
    }
    const result = new Array<number>(total);
    const available = ([0, 1, 2] as const).filter((s) => !claimed.has(s));
    let avIdx = 0;
    for (let i = 0; i < ports.length; i++) {
      const s = ports[i].slot;
      result[i] = s !== undefined ? SLOT_PCT[s] : SLOT_PCT[available[avIdx++] ?? 1];
    }
    return result;
  }
  return ports.map((_, i) => ((i + 1) * 100) / (total + 1));
}
