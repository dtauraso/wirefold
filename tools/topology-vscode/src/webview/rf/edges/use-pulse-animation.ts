// use-pulse-animation.ts — RAF-driven pulse animation for SubstrateEdge.
// Returns {pulseT, pathRef, pulseValueRef}: pulseT is position 0–1 or null when idle.
//
// Lifecycle:
//   1. "send" trace event → pump sets pulse data → effect fires → RAF animates 0→1.
//   2. On RAF completion (t=1): post "delivered" so Go's PacedWire unblocks Recv.
//      Pulse stays pinned at t=1 (held at destination handle).
//   3. "done" trace event → pump clears pulse data → effect sees no pulse → setPulseT(null).

import { useEffect, useRef, useState } from "react";
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

  const pulse = data?.[ANIMATION_FIELDS.pulse.name];

  // Clear pulseT when pulse data is removed (Done event cleared it in pump).
  useEffect(() => {
    if (!pulse) {
      setPulseT(null);
    }
  }, [pulse]);

  // Drive RAF animation while pulse data is present.
  useEffect(() => {
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
          // Pulse arrived at destination. Stay pinned at t=1 until Go signals Done.
          // Post "delivered" so Go's PacedWire unblocks Recv; do NOT clear pulse data.
          vscode.postMessage({ type: "delivered", edge: idRef.current });
        }
      } else {
        lastFrameTime = null;
        raf = requestAnimationFrame(tick);
      }
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [pulse]);

  return { pulseT, pathRef, pulseValueRef };
}
