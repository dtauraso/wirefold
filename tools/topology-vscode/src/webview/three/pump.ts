// pump.ts — trace-event translator. Pure mapping: Go trace events → dedicated state stores.
// No state machine, no phase tracking, no substrate logic.
// pump.ts is render-only; substrate poll loops live in Go. See MODEL.md §Driver.
// Each call to handleTraceEvent updates the relevant state store directly.
//
// ── Send→pulse→delivered→done lifecycle ─────────────────────────────────────
// Contract source: nodes/Wiring/paced_wire.go Send / NotifyDelivered / Done.
//
//  1. Go emits "send" (node, port, value)
//     → pump.ts  filters ALL RF edges by source+sourceHandle (fan-out)
//     → pulse-state.ts:setPulse  writes { value, simStep, target, targetHandle } into PulseCtx
//
//  2. RAF loop in edges/use-pulse-animation.ts animates pulse 0→1
//     → posts "delivered" message so Go's PacedWire unblocks Recv
//     → clears pulseT (dot disappears)
//
//  3. Go emits "done" (node, port)
//     → pump.ts  filters ALL RF edges by target+targetHandle (fan-in)
//     → pulse-state.ts:clearPulse  removes animation data from PulseCtx
// ────────────────────────────────────────────────────────────────────────────

import type { TraceEvent } from "../../messages";
import type { TraceEventKind } from "./trace-kinds";
import { useThreeStore } from "./store";
import { postLog } from "../log/post";
import { setPulse, clearPulse, getPulseMap, patchPulse } from "./pulse-state";
import { getPauseAdjustedNow } from "../state/run-status";

// assertNever enforces exhaustiveness: if a new TraceEventKind is added in Go
// and trace-kinds.ts is regenerated, tsc will flag the missing branch here.
function assertNever(x: never): never {
  throw new Error(`[pump] unhandled trace event kind: ${String(x)}`);
}

export function handleTraceEvent(event: TraceEvent): void {
  const { step, kind } = event;
  // Cast to the generated enum so tsc checks all branches are covered.
  const k = kind as TraceEventKind;
  switch (k) {
    case "slot":
      return;
    case "recv":
      return;
    case "fire":
      return;
    case "send": {
      // Match ALL edges by source node id + sourceHandle (fan-out).
      // RF edges store source/sourceHandle; trace send events carry node/port.
      const { node, port, value, simLatencyMs } = event as Extract<TraceEvent, { kind: "send" }>;
      // simLatencyMs should always be present after Phase 2; fallback guards
      // against stale Go binaries or future schema gaps.
      const FALLBACK_MS = 500;
      if (simLatencyMs == null) {
        console.warn("[pump] send event missing simLatencyMs — falling back to", FALLBACK_MS, "ms");
      }
      const resolvedLatency = simLatencyMs ?? FALLBACK_MS;
      const edges = useThreeStore.getState().edges;
      const matched = edges.filter(
        (e) => e.source === node && e.sourceHandle === port,
      );
      for (const edge of matched) {
        postLog("phase4.pump", { layer: "pump", step, node, port: port ?? null, edgeId: edge.id });
        // Pass target+targetHandle so use-pulse-animation can write the held-value
        // badge at t=1 (pulse arrival) rather than eagerly at send time.
        setPulse(edge.id, {
          value: value ?? 0,
          simStep: step,
          target: edge.target ?? "",
          targetHandle: edge.targetHandle ?? "",
          simLatencyMs: resolvedLatency,
        });
      }
      return;
    }
    case "latency-changed": {
      // Adjust any in-flight bead on this edge so it finishes at the correct
      // time after the node drag has changed the wire length.
      // Preserve fractional progress t_curr, re-anchor startTime accordingly:
      //   t_curr = (now - oldStartTime) / oldSimLatencyMs
      //   newStartTime = now - t_curr * newSimLatencyMs
      const { edge, simLatencyMs: newLatencyMs } = event as Extract<TraceEvent, { kind: "latency-changed" }>;
      const pulse = getPulseMap().get(edge);
      if (pulse) {
        const now = getPauseAdjustedNow();
        const elapsed = now - pulse.startTime;
        const tCurr = Math.min(1, elapsed / pulse.simLatencyMs);
        const newStartTime = now - tCurr * newLatencyMs;
        patchPulse(edge, newLatencyMs, newStartTime);
      }
      return;
    }
    // PUMP_DONE_HANDLER
    case "done": {
      // Match ALL edges by target node id + targetHandle (fan-in).
      // RF edges store target/targetHandle; trace done events carry node/port.
      const { node, port } = event as Extract<TraceEvent, { kind: "done" }>;
      const edges = useThreeStore.getState().edges;
      const matched = edges.filter(
        (e) => e.target === node && e.targetHandle === port,
      );
      // Held value is intentionally NOT cleared here — badges are sticky and
      // show the last value received per input port until overwritten by a new send.
      for (const edge of matched) {
        postLog("phase4.pump.done", { layer: "pump.done", step, node, port: port ?? null, edgeId: edge.id });
        clearPulse(edge.id);
      }
      return;
    }
    default:
      assertNever(k);
  }
}
