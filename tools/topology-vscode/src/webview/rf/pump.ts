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
//     → held-values.ts:setHeldValue  (destination node shows badge while pulse travels)
//     → pulse-state.ts:setPulse  writes { value, simStep } into PulseCtx
//
//  2. RAF loop in edges/use-pulse-animation.ts animates pulse 0→1
//     → on t=1: posts "delivered" message so Go's PacedWire unblocks Recv
//     → clears pulseT (dot disappears); held-value badge stays visible
//
//  3. Go emits "done" (node, port)
//     → pump.ts  filters ALL RF edges by target+targetHandle (fan-in)
//     → pulse-state.ts:clearPulse  removes animation data from PulseCtx
//     → held value is intentionally NOT cleared — badge is sticky
// ────────────────────────────────────────────────────────────────────────────

import type { TraceEvent, SlotEvent, SlotMap } from "../../messages";
import type { TraceEventKind } from "./trace-kinds";
import { rfGetEdges } from "./rf-imperative";
import { postLog } from "../log/post";
import { setHeldValue } from "./held-values";
import { setPulse, clearPulse } from "./pulse-state";
import { setLastFire } from "./fire-flash-state";
import { setSlots } from "./slots-state";

// Local accumulator: tracks the merged SlotMap per node across slot events.
// setSlots always receives the full merged map; this holds the previous state.
const _currentSlots = new Map<string, SlotMap>();

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
    case "slot": {
      const { nodeId, port, phase, value } = event as SlotEvent;
      const prevSlots = (_currentSlots.get(nodeId) ?? {}) as SlotMap;
      const nextSlots: SlotMap = {
        ...prevSlots,
        [port]: phase === "filled"
          ? { phase: "filled", value: value ?? 0 }
          : { phase: "empty" },
      };
      _currentSlots.set(nodeId, nextSlots);
      setSlots(nodeId, nextSlots);
      return;
    }
    case "recv":
      return;
    case "fire": {
      const { node } = event as Extract<TraceEvent, { kind: "fire" }>;
      setLastFire(node, step);
      return;
    }
    case "send": {
      // Match ALL edges by source node id + sourceHandle (fan-out).
      // RF edges store source/sourceHandle; trace send events carry node/port.
      const { node, port, value } = event as Extract<TraceEvent, { kind: "send" }>;
      const edges = rfGetEdges();
      const matched = edges.filter(
        (e) => e.source === node && e.sourceHandle === port,
      );
      for (const edge of matched) {
        postLog("phase4.pump", { layer: "pump", step, node, port: port ?? null, edgeId: edge.id });
        // Eagerly record the held value at the destination port so the node can
        // show it while the pulse animates and until Go signals Done.
        if (edge.target && edge.targetHandle) {
          setHeldValue(edge.target, edge.targetHandle, value ?? 0);
        }
        setPulse(edge.id, { value: value ?? 0, simStep: step });
      }
      return;
    }
    case "done": {
      // Match ALL edges by target node id + targetHandle (fan-in).
      // RF edges store target/targetHandle; trace done events carry node/port.
      const { node, port } = event as Extract<TraceEvent, { kind: "done" }>;
      const edges = rfGetEdges();
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
