// use-pulse-animation.ts — RAF-driven pulse animation for SubstrateEdge.
// Returns {pulseT, pathRef, pulseValueRef}: pulseT is position 0–1 or null when idle.
//
// Full send→pulse→delivered→done lifecycle is documented in pump.ts (top-of-file
// comment block). This file owns step 2 only: RAF loop driving pulse 0→1 and
// posting "delivered" when complete.
//
// startTime is read from pulse-state (set at setPulse call time) so that a
// remounted component resumes at the correct animation offset rather than
// restarting from t=0.

import { useEffect, useRef, useState } from "react";
import { postLog } from "../../log/post";
import { vscode } from "../../vscode-api";
import { claimDelivered, usePulseCtx } from "../pulse-state";
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

    const { startTime } = pulse;

    // Poll until pathRef is attached and getTotalLength() > 0 (layout may not
    // be complete on the first frame after remount, causing getTotalLength()
    // to return 0 and collapsing duration to 0, which makes tNow = Infinity).
    let raf: number;
    let pollFrames = 0;
    const MAX_POLL_FRAMES = 60;

    const waitForPath = () => {
      const pl = pathRef.current?.getTotalLength() ?? 0;
      if (pl <= 0) {
        if (++pollFrames >= MAX_POLL_FRAMES) {
          console.warn("[pulse] pathLength never became positive for edge " + id);
          if (claimDelivered(idRef.current, startTime)) {
            vscode.postMessage({ type: "delivered", edge: idRef.current });
          }
          setPulseT(null);
          return;
        }
        raf = requestAnimationFrame(waitForPath);
        return;
      }

      const duration = pl / PULSE_SPEED_PX_PER_MS;

      // Compute t from the shared anchor so remounts resume at the right offset.
      const tNow = Math.min((performance.now() - startTime) / duration, 1);
      if (tNow >= 1) {
        // Already finished by the time this component mounted.
        if (claimDelivered(idRef.current, startTime)) {
          vscode.postMessage({ type: "delivered", edge: idRef.current });
        }
        setPulseT(null);
        return;
      }

      setPulseT(tNow);

      const tick = (now: number) => {
        if (!pausedRef.current) {
          const t = Math.min((now - startTime) / duration, 1);
          setPulseT(t);
          if (t < 1) {
            raf = requestAnimationFrame(tick);
          } else {
            // Pulse arrived at destination. Post "delivered" so Go's PacedWire
            // unblocks Recv, then clear the pulse dot immediately.
            if (claimDelivered(idRef.current, startTime)) {
              vscode.postMessage({ type: "delivered", edge: idRef.current });
            }
            setPulseT(null);
          }
        } else {
          raf = requestAnimationFrame(tick);
        }
      };
      raf = requestAnimationFrame(tick);
    };

    raf = requestAnimationFrame(waitForPath);

    return () => cancelAnimationFrame(raf);
  }, [pulse]);

  return { pulseT, pathRef, pulseValueRef };
}
