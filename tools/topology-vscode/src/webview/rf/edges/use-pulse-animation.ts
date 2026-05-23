// use-pulse-animation.ts — RAF-driven pulse animation for SubstrateEdge.
// Returns {pulseT, pathRef, pulseValueRef}: pulseT is position 0–1 or null when idle.
//
// Full send→pulse→delivered→done lifecycle is documented in pump.ts (top-of-file
// comment block). This file owns step 2 only: RAF loop driving pulse 0→1 and
// posting "delivered" when complete.

import { useEffect, useRef, useState } from "react";
import { postLog } from "../../log/post";
import { vscode } from "../../vscode-api";
import { usePulseCtx } from "../pulse-state";
import { useRunStatusCtx } from "../run-status";

const PULSE_SPEED_PX_PER_MS = 0.08;

export function usePulseAnimation(id: string) {
  const [pulseT, setPulseT] = useState<number | null>(null);
  const pathRef = useRef<SVGPathElement | null>(null);
  const pulseValueRef = useRef<unknown>(undefined);
  const idRef = useRef(id);
  idRef.current = id;
  const runStatus = useRunStatusCtx();
  const pausedRef = useRef(false);
  pausedRef.current = runStatus.state === "paused";

  const pulseMap = usePulseCtx();
  const pulse = pulseMap.get(id);

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
          // Pulse arrived at destination. Post "delivered" so Go's PacedWire unblocks
          // Recv, then clear the pulse dot immediately. The held value is now shown
          // inside the destination node component until Go signals Done.
          vscode.postMessage({ type: "delivered", edge: idRef.current });
          setPulseT(null);
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
