// RF custom edge — static path + optional pulse circle animation.
// Pulse is driven by edge.data.pulse set by pump.ts on trace "send" events.
// Animation runs via requestAnimationFrame for 600ms then stops.
// No substrate logic — component renders whatever data says.

import { useEffect, useRef, useState } from "react";
import { postLog } from "../../log/post";
import { BaseEdge, EdgeLabelRenderer, getBezierPath, type EdgeProps } from "reactflow";
import { KIND_COLORS } from "../../../schema";
import type { EdgeKind } from "../../../schema/types";
import type { EdgeData } from "../types";
import { ANIMATION_FIELDS } from "../animation-fields";

const PULSE_DURATION_MS = 600;

function dashForKind(kind: EdgeKind | undefined): string | undefined {
  return kind === "pointer" ? "4 3" : undefined;
}

export function SubstrateEdge({
  id,
  sourceX, sourceY, targetX, targetY,
  sourcePosition, targetPosition,
  data,
  markerEnd,
}: EdgeProps<EdgeData>) {
  const [edgePath, labelX, labelY] = getBezierPath({
    sourceX, sourceY, sourcePosition,
    targetX, targetY, targetPosition,
  });

  const kind: EdgeKind = data?.kind ?? "any";
  const stroke = KIND_COLORS[kind] ?? "#888";
  const dash = dashForKind(kind);
  const displayLabel = data?.valueLabel;

  // Pulse animation state: position along path (0–1) or null when idle.
  const [pulseT, setPulseT] = useState<number | null>(null);
  const pathRef = useRef<SVGPathElement | null>(null);
  // Track which pulse we last animated to avoid re-triggering the same event.
  const lastPulseStep = useRef<number | undefined>(undefined);

  useEffect(() => {
    const pulse = data?.[ANIMATION_FIELDS.pulse.name];
    if (!pulse) return;
    if (pulse.simStep === lastPulseStep.current) return;
    console.log(`[edge] pulse start id=${id} step=${pulse.simStep} value=${pulse.value}`);
    postLog("phase4.edge", { layer: "edge", id, step: pulse.simStep, value: pulse.value });
    lastPulseStep.current = pulse.simStep;

    const start = performance.now();
    let raf: number;
    const tick = (now: number) => {
      const t = Math.min((now - start) / PULSE_DURATION_MS, 1);
      setPulseT(t);
      if (t < 1) {
        raf = requestAnimationFrame(tick);
      } else {
        setPulseT(null);
      }
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [data?.[ANIMATION_FIELDS.pulse.name]]);

  // Compute circle position along the SVG path.
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
        markerEnd={markerEnd}
        style={{ stroke, strokeDasharray: dash, strokeWidth: 1.5 }}
      />
      {circleX !== undefined && circleY !== undefined && (
        <circle
          cx={circleX}
          cy={circleY}
          r={4}
          fill={stroke}
          style={{ pointerEvents: "none" }}
        />
      )}
      {displayLabel && (
        <EdgeLabelRenderer>
          <div
            style={{
              position: "absolute",
              transform: `translate(-50%, -50%) translate(${labelX}px,${labelY}px)`,
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
