// RF custom edge — static path + optional pulse circle animation.
// Pulse is driven by edge.data.pulse set by pump.ts on trace "send" events.
// Animation runs via requestAnimationFrame for 600ms then stops.
// No substrate logic — component renders whatever data says.
//
// Route selection: pickShape auto-picks snake/snake-v/below/line based on
// handle sides + positions. A draggable midpoint shifts the dogleg via midpointOffset.

import { useEffect, useRef, useState } from "react";
import { postLog } from "../../log/post";
import { BaseEdge, EdgeLabelRenderer, type EdgeProps } from "reactflow";
import { useReactFlow } from "reactflow";
import { KIND_COLORS } from "../../../schema";
import type { EdgeKind } from "../../../schema/types";
import type { EdgeData } from "../types";
import { ANIMATION_FIELDS } from "../animation-fields";
import { markerEndUrl } from "../MarkerDefs";
import { useRunStatusCtx } from "../run-status-ctx";
import {
  type SideName,
  pickShape,
  buildEdgePathD,
  edgeMidpoint,
  finalSegmentLength,
} from "./edge-geometry";
import { MidpointDragHandle } from "./MidpointDragHandle";

export type { EdgeRoute, SideName } from "./edge-geometry";
export { pickShape, buildEdgePathD, edgeMidpoint } from "./edge-geometry";

const PULSE_SPEED_PX_PER_MS = 0.08;

// Marker head lengths (refX of the filled markers in MarkerDefs).
const MD_HEAD_PX = 8;
const SM_HEAD_PX = 5;

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
  const runStatus = useRunStatusCtx();
  // Ref so the tick closure always reads the latest paused state without
  // needing to re-create the RAF effect on every status change.
  const pausedRef = useRef(false);
  pausedRef.current = runStatus.state === "paused";

  useEffect(() => {
    const pulse = data?.[ANIMATION_FIELDS.pulse.name];
    if (!pulse) return;
    console.log(`[edge] pulse start id=${id} step=${pulse.simStep} value=${pulse.value}`);
    postLog("phase4.edge", { layer: "edge", id, step: pulse.simStep, value: pulse.value });
    pulseValueRef.current = pulse.value;

    const pathLength = pathRef.current?.getTotalLength() ?? null;
    const duration = pathLength !== null ? pathLength / PULSE_SPEED_PX_PER_MS : 1000;
    let elapsed = 0;
    let lastFrameTime: number | null = null;
    let raf: number;
    const tick = (now: number) => {
      if (!pausedRef.current) {
        if (lastFrameTime !== null) elapsed += now - lastFrameTime;
        lastFrameTime = now;
        const t = Math.min(elapsed / duration, 1);
        setPulseT(t);
        if (t < 1) {
          raf = requestAnimationFrame(tick);
        } else {
          setPulseT(null);
          rf.setEdges(edges => edges.map(e => e.id === id
            ? { ...e, data: { ...e.data, [ANIMATION_FIELDS.pulse.name]: undefined } }
            : e));
        }
      } else {
        lastFrameTime = null;
        raf = requestAnimationFrame(tick);
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
