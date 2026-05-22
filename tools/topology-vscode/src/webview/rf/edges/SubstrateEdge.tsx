// RF custom edge — static path + optional pulse circle animation.
// Pulse is driven by edge.data.pulse set by pump.ts on trace "send" events.
// Animation runs via requestAnimationFrame for 600ms then stops.
// No substrate logic — component renders whatever data says.
//
// Route selection: pickShape auto-picks snake/snake-v/below/line based on
// handle sides + positions. A draggable midpoint shifts the dogleg via midpointOffset.

import { useCallback, useEffect, useRef, useState } from "react";
import { postLog } from "../../log/post";
import { BaseEdge, EdgeLabelRenderer, type EdgeProps } from "reactflow";
import { useReactFlow } from "reactflow";
import { KIND_COLORS } from "../../../schema";
import type { EdgeKind } from "../../../schema/types";
import type { EdgeData } from "../types";
import { ANIMATION_FIELDS } from "../animation-fields";
import { useEdgeActions } from "../app/_edge-actions-ctx";
import { markerEndUrl } from "../MarkerDefs";

const PULSE_SPEED_PX_PER_MS = 0.08;

// Marker head lengths (refX of the filled markers in MarkerDefs).
const MD_HEAD_PX = 8;
const SM_HEAD_PX = 5;

// ── Types ───────────────────────────────────────────────────────────

export type EdgeRoute = "line" | "snake" | "snake-v" | "below";
export type SideName = "left" | "right" | "top" | "bottom";

// ── Route picker ────────────────────────────────────────────────────

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

// ── Path helpers ────────────────────────────────────────────────────

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

// Length of the final segment of the edge — the leg the arrow sits on.
// Marker-shrink decisions use this, not total path length: a long dogleg
// route can still terminate in a tiny entry leg where a full-size arrow
// visually dominates. For the bezier "line" route there is no distinct
// final segment, so fall back to the chord length.
function finalSegmentLength(
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

// ── MidpointDragHandle ──────────────────────────────────────────────

const HOVER_CLEARANCE_PX = 16;

interface MidpointDragHandleProps {
  edgeId: string;
  route: EdgeRoute;
  pathD: string;
  mid: { x: number; y: number };
  midpointOffset: number;
  stroke: string;
}

function MidpointDragHandle({ edgeId, route, pathD, mid, midpointOffset, stroke }: MidpointDragHandleProps) {
  const actions = useEdgeActions();
  const rf = useReactFlow();
  const dragRef = useRef<{ startScreenX: number; startScreenY: number; startOffset: number } | null>(null);
  const [hovered, setHovered] = useState(false);
  const [dragging, setDragging] = useState(false);

  const onMouseDown = useCallback((ev: React.MouseEvent<SVGCircleElement>) => {
    if (!actions) return;
    ev.stopPropagation();
    ev.preventDefault();
    dragRef.current = { startScreenX: ev.clientX, startScreenY: ev.clientY, startOffset: midpointOffset };
    setDragging(true);

    const onMove = (me: MouseEvent) => {
      if (!dragRef.current || !actions) return;
      const origin = rf.screenToFlowPosition({ x: dragRef.current.startScreenX, y: dragRef.current.startScreenY });
      const current = rf.screenToFlowPosition({ x: me.clientX, y: me.clientY });
      const delta = route === "snake"
        ? current.x - origin.x
        : current.y - origin.y;
      actions.setEdgeMidpointOffset(edgeId, dragRef.current.startOffset + delta);
    };

    const onUp = () => {
      dragRef.current = null;
      setDragging(false);
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };

    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  }, [actions, edgeId, midpointOffset, rf, route]);

  if (!actions) return null;
  const visible = hovered || dragging;

  return (
    <g onMouseEnter={() => setHovered(true)} onMouseLeave={() => setHovered(false)}>
      <path
        d={pathD}
        fill="none"
        stroke="transparent"
        strokeWidth={HOVER_CLEARANCE_PX}
        style={{ pointerEvents: "stroke" }}
      />
      {visible && (
        <circle
          cx={mid.x}
          cy={mid.y}
          r={5}
          fill={stroke}
          fillOpacity={0.5}
          stroke={stroke}
          strokeWidth={1}
          style={{ cursor: route === "snake" ? "ew-resize" : "ns-resize", pointerEvents: "all" }}
          onMouseDown={onMouseDown}
        />
      )}
    </g>
  );
}

// ── SubstrateEdge ───────────────────────────────────────────────────

function dashForKind(kind: EdgeKind | undefined): string | undefined {
  return kind === "pointer" ? "4 3" : undefined;
}

export function SubstrateEdge({
  id,
  sourceX, sourceY, targetX, targetY,
  sourcePosition, targetPosition,
  data,
}: EdgeProps<EdgeData>) {
  const kind: EdgeKind = data?.kind ?? "any";
  const stroke = KIND_COLORS[kind] ?? "#888";
  const dash = dashForKind(kind);
  const displayLabel = data?.valueLabel;
  const midpointOffset = data?.midpointOffset ?? 0;

  const route = pickShape(
    sourceX, sourceY, sourcePosition as SideName,
    targetX, targetY, targetPosition as SideName,
  );

  const legLen = finalSegmentLength(route, sourceX, sourceY, targetX, targetY, midpointOffset);
  const markerSize: "sm" | "md" = legLen < MD_HEAD_PX ? "sm" : "md";
  const arrowStyle = data?.arrowStyle ?? "filled";
  const computedMarkerEnd = legLen < SM_HEAD_PX ? undefined : markerEndUrl(kind, arrowStyle, markerSize);

  const edgePath = buildEdgePathD(
    route,
    sourceX, sourceY, sourcePosition,
    targetX, targetY, targetPosition,
    midpointOffset,
  );

  const mid = edgeMidpoint(route, sourceX, sourceY, targetX, targetY, midpointOffset);

  const rf = useReactFlow();

  // Pulse animation state: position along path (0–1) or null when idle.
  const [pulseT, setPulseT] = useState<number | null>(null);
  const pathRef = useRef<SVGPathElement | null>(null);
  const pulseValueRef = useRef<unknown>(undefined);

  useEffect(() => {
    const pulse = data?.[ANIMATION_FIELDS.pulse.name];
    if (!pulse) return;
    console.log(`[edge] pulse start id=${id} step=${pulse.simStep} value=${pulse.value}`);
    postLog("phase4.edge", { layer: "edge", id, step: pulse.simStep, value: pulse.value });
    pulseValueRef.current = pulse.value;

    const pathLength = pathRef.current?.getTotalLength() ?? null;
    const duration = pathLength !== null ? pathLength / PULSE_SPEED_PX_PER_MS : 1000;
    const start = performance.now();
    let raf: number;
    const tick = (now: number) => {
      const t = Math.min((now - start) / duration, 1);
      setPulseT(t);
      if (t < 1) {
        raf = requestAnimationFrame(tick);
      } else {
        setPulseT(null);
        rf.setEdges(edges => edges.map(e => e.id === id ? { ...e, data: { ...e.data, [ANIMATION_FIELDS.pulse.name]: undefined } } : e));
      }
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [data?.[ANIMATION_FIELDS.pulse.name]]);

  // Compute pulse circle position along the SVG path.
  let circleX: number | undefined;
  let circleY: number | undefined;
  if (pulseT !== null && pathRef.current) {
    const len = pathRef.current.getTotalLength();
    const pt = pathRef.current.getPointAtLength(len * pulseT);
    circleX = pt.x;
    circleY = pt.y;
  }

  return (
    <>
      <path
        ref={pathRef}
        id={`${id}-measure`}
        d={edgePath}
        fill="none"
        stroke="transparent"
        strokeWidth={1}
        style={{ pointerEvents: "none" }}
      />
      <BaseEdge
        id={id}
        path={edgePath}
        markerEnd={computedMarkerEnd}
        style={{ stroke, strokeDasharray: dash, strokeWidth: 1.5 }}
      />
      <MidpointDragHandle
        edgeId={id}
        route={route}
        pathD={edgePath}
        mid={mid}
        midpointOffset={midpointOffset}
        stroke={stroke}
      />
      {circleX !== undefined && circleY !== undefined && (
        <>
          <circle
            cx={circleX}
            cy={circleY}
            r={4}
            fill={stroke}
            style={{ pointerEvents: "none" }}
          />
          <text
            x={circleX}
            y={circleY - 10}
            textAnchor="middle"
            fontSize={10}
            fontFamily="monospace"
            fill={stroke}
            style={{ pointerEvents: "none" }}
          >{String(pulseValueRef.current ?? "")}</text>
        </>
      )}
      {displayLabel && (
        <EdgeLabelRenderer>
          <div
            style={{
              position: "absolute",
              transform: `translate(-50%, -50%) translate(${mid.x}px,${mid.y}px)`,
              fontSize: 10,
              fontFamily: "monospace",
              color: stroke,
              background: "#0d1117",
              padding: "1px 4px",
              borderRadius: 2,
              pointerEvents: "none",
            }}
            className="nodrag nopan"
          >
            {displayLabel}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}
