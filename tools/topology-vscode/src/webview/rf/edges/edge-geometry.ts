// Pure geometry for substrate edges — path building, route selection, midpoint.
// No React, no RF imports. Consumed by SubstrateEdge.tsx.

export type EdgeRoute = "line" | "snake" | "snake-v" | "below";
export type SideName = "left" | "right" | "top" | "bottom";

// ── Route picker ──────────────────────────────────────────────────────

const COLLINEAR_TOLERANCE = 8;

export function pickShape(
  sx: number, sy: number, sp: SideName,
  tx: number, ty: number, tp: SideName,
): EdgeRoute {
  const sourceHorizontal = sp === "left" || sp === "right";
  const targetHorizontal = tp === "left" || tp === "right";
  if (sourceHorizontal !== targetHorizontal) return "line";
  if (sp === "bottom" && tp === "bottom") return "below";
  const dx = tx - sx;
  const dy = ty - sy;
  if (sourceHorizontal) {
    const exitsAway = (sp === "right" && dx < 0) || (sp === "left" && dx > 0);
    if (exitsAway) return "snake";
    if (Math.abs(dy) < COLLINEAR_TOLERANCE) return "line";
    return "snake";
  }
  const exitsAway = (sp === "bottom" && dy < 0) || (sp === "top" && dy > 0);
  if (exitsAway) return "snake-v";
  if (Math.abs(dx) < COLLINEAR_TOLERANCE) return "line";
  return "snake-v";
}

// ── Path helpers ──────────────────────────────────────────────────────

function snakeD(sx: number, sy: number, tx: number, ty: number, midpointOffset: number): string {
  const midX = (sx + tx) / 2 + midpointOffset;
  const r = Math.min(15, Math.abs(midX - sx) / 2, Math.abs(tx - midX) / 2, Math.abs(ty - sy) / 2);
  if (!(r > 0.5)) {
    return `M ${sx},${sy} L ${midX},${sy} L ${midX},${ty} L ${tx},${ty}`;
  }
  const sxDir = midX >= sx ? 1 : -1;
  const yDir  = ty >= sy ? 1 : -1;
  const txDir = tx >= midX ? 1 : -1;
  return (
    `M ${sx},${sy} ` +
    `L ${midX - sxDir * r},${sy} ` +
    `Q ${midX},${sy} ${midX},${sy + yDir * r} ` +
    `L ${midX},${ty - yDir * r} ` +
    `Q ${midX},${ty} ${midX + txDir * r},${ty} ` +
    `L ${tx},${ty}`
  );
}

function snakeVD(sx: number, sy: number, tx: number, ty: number, midpointOffset: number): string {
  const midY = (sy + ty) / 2 + midpointOffset;
  const r = Math.min(15, Math.abs(midY - sy) / 2, Math.abs(ty - midY) / 2, Math.abs(tx - sx) / 2);
  if (!(r > 0.5)) {
    return `M ${sx},${sy} L ${sx},${midY} L ${tx},${midY} L ${tx},${ty}`;
  }
  const syDir = midY >= sy ? 1 : -1;
  const xDir  = tx >= sx ? 1 : -1;
  const tyDir = ty >= midY ? 1 : -1;
  return (
    `M ${sx},${sy} ` +
    `L ${sx},${midY - syDir * r} ` +
    `Q ${sx},${midY} ${sx + xDir * r},${midY} ` +
    `L ${tx - xDir * r},${midY} ` +
    `Q ${tx},${midY} ${tx},${midY + tyDir * r} ` +
    `L ${tx},${ty}`
  );
}

function belowD(sx: number, sy: number, tx: number, ty: number, midpointOffset: number): string {
  const corridorY = Math.max(sy, ty) + 80 + midpointOffset;
  const r = Math.min(15, Math.abs(corridorY - sy) / 2, Math.abs(corridorY - ty) / 2, Math.abs(tx - sx) / 2);
  if (!(r > 0.5)) {
    return `M ${sx},${sy} L ${sx},${corridorY} L ${tx},${corridorY} L ${tx},${ty}`;
  }
  const xDir = tx >= sx ? 1 : -1;
  return (
    `M ${sx},${sy} ` +
    `L ${sx},${corridorY - r} ` +
    `Q ${sx},${corridorY} ${sx + xDir * r},${corridorY} ` +
    `L ${tx - xDir * r},${corridorY} ` +
    `Q ${tx},${corridorY} ${tx},${corridorY - r} ` +
    `L ${tx},${ty}`
  );
}

// ── Public geometry API ───────────────────────────────────────────────

/** Length of the final leg the arrowhead sits on. */
export function finalSegmentLength(
  route: EdgeRoute,
  sx: number, sy: number,
  tx: number, ty: number,
  midpointOffset: number,
): number {
  if (route === "snake") {
    const midX = (sx + tx) / 2 + midpointOffset;
    return Math.abs(tx - midX);
  }
  if (route === "snake-v") {
    const midY = (sy + ty) / 2 + midpointOffset;
    return Math.abs(ty - midY);
  }
  if (route === "below") {
    const corridorY = Math.max(sy, ty) + 80 + midpointOffset;
    return corridorY - ty;
  }
  const dx = tx - sx, dy = ty - sy;
  return Math.sqrt(dx * dx + dy * dy);
}

function controlOffset(distance: number): number {
  return distance >= 0 ? 0.5 * distance : 0.25 * 25 * Math.sqrt(-distance);
}

function controlPoint(pos: string, x1: number, y1: number, x2: number, y2: number) {
  switch (pos) {
    case "left":   return { x: x1 - controlOffset(x1 - x2), y: y1 };
    case "right":  return { x: x1 + controlOffset(x2 - x1), y: y1 };
    case "top":    return { x: x1, y: y1 - controlOffset(y1 - y2) };
    case "bottom": return { x: x1, y: y1 + controlOffset(y2 - y1) };
    default:       return { x: x1, y: y1 };
  }
}

export function buildEdgePathD(
  route: EdgeRoute,
  sx: number, sy: number, sp: string,
  tx: number, ty: number, tp: string,
  midpointOffset: number,
): string {
  if (route === "snake")   return snakeD(sx, sy, tx, ty, midpointOffset);
  if (route === "snake-v") return snakeVD(sx, sy, tx, ty, midpointOffset);
  if (route === "below")   return belowD(sx, sy, tx, ty, midpointOffset);
  const c1 = controlPoint(sp, sx, sy, tx, ty);
  const c2 = controlPoint(tp, tx, ty, sx, sy);
  return `M ${sx},${sy} C ${c1.x},${c1.y} ${c2.x},${c2.y} ${tx},${ty}`;
}

export function edgeMidpoint(
  route: EdgeRoute,
  sx: number, sy: number,
  tx: number, ty: number,
  midpointOffset: number,
): { x: number; y: number } {
  if (route === "snake")   return { x: (sx + tx) / 2 + midpointOffset, y: (sy + ty) / 2 };
  if (route === "snake-v") return { x: (sx + tx) / 2, y: (sy + ty) / 2 + midpointOffset };
  if (route === "below")   return { x: (sx + tx) / 2, y: Math.max(sy, ty) + 80 + midpointOffset };
  return { x: (sx + tx) / 2, y: (sy + ty) / 2 };
}
