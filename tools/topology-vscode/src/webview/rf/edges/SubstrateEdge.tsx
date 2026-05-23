// RF custom edge — static path + optional pulse circle animation.
// Pulse is driven by edge.data.pulse set by pump.ts on trace "send" events.
// Animation logic lives in use-pulse-animation.ts.
// No substrate logic — component renders whatever data says.
//
// Route selection: pickShape auto-picks snake/snake-v/below/line based on
// handle sides + positions. A draggable midpoint shifts the dogleg via midpointOffset.

import { BaseEdge, EdgeLabelRenderer, type EdgeProps } from "reactflow";
import { KIND_COLORS } from "../../../schema";
import type { EdgeKind } from "../../../schema/types";
import type { EdgeData } from "../types";
import { markerEndUrl } from "../MarkerDefs";
import {
  type SideName,
  pickShape,
  buildEdgePathD,
  edgeMidpoint,
  finalSegmentLength,
} from "./edge-geometry";
import { MidpointDragHandle } from "./MidpointDragHandle";
import { usePulseAnimation } from "./use-pulse-animation";

export type { EdgeRoute, SideName } from "./edge-geometry";
export { pickShape, buildEdgePathD, edgeMidpoint } from "./edge-geometry";

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

  const { pulseT, pathRef, pulseValueRef } = usePulseAnimation(id, data);

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
