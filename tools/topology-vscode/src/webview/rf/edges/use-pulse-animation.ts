// use-pulse-animation.ts — RAF-driven pulse animation for SubstrateEdge.
// Returns {pulseT, pathRef, pulseValueRef}: pulseT is position 0–1 or null when idle.
// On completion posts "delivered" to the extension host so Go's PacedWire unblocks.

import { useEffect, useRef, useState } from "react";
import { useReactFlow } from "reactflow";
import { postLog } from "../../log/post";
import { vscode } from "../../vscode-api";
import { ANIMATION_FIELDS } from "../animation-fields";
import { useRunStatusCtx } from "../run-status-ctx";
import type { EdgeData } from "../types";

const PULSE_SPEED_PX_PER_MS = 0.08;

export function usePulseAnimation(id: string, data: EdgeData | undefined) {
  const [pulseT, setPulseT] = useState<number | null>(null);
  const pathRef = useRef<SVGPathElement | null>(null);
  const pulseValueRef = useRef<unknown>(undefined);
  const idRef = useRef(id);
  idRef.current = id;
  const runStatus = useRunStatusCtx();
  const pausedRef = useRef(false);
  pausedRef.current = runStatus.state === "paused";
  const rf = useReactFlow();

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
          rf.setEdges(edges => edges.map(e => e.id === idRef.current
            ? { ...e, data: { ...e.data, [ANIMATION_FIELDS.pulse.name]: undefined } }
            : e));
          postLog("pulse.deliver", { edge: idRef.current });
          vscode.postMessage({ type: "delivered", edge: idRef.current });
        }
      } else {
        lastFrameTime = null;
        raf = requestAnimationFrame(tick);
      }
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [data?.[ANIMATION_FIELDS.pulse.name]]);

  return { pulseT, pathRef, pulseValueRef };
}
