// pump.ts — trace-event translator. Pure mapping: Go trace events → dedicated state stores.
// No state machine, no phase tracking, no substrate logic.
// pump.ts is render-only; substrate poll loops live in Go. See MODEL.md §Driver.
// Each call to handleTraceEvent updates the relevant state store directly.
//
// ── Send→position→done lifecycle (Phase 2) ──────────────────────────────────
// Contract source: nodes/Wiring/paced_wire.go Send / startDeliveryLocked / Done.
// Go owns the clock, computes every bead position, and times its own delivery;
// TS plots only and never tells Go when a bead arrived.
//
//  1. Go emits "send" (node, port, value)
//     → pump.ts  filters ALL RF edges by source+sourceHandle (fan-out)
//     → pulse-state.ts:setPulse  records { value, simStep, target, targetHandle }
//       (pos starts null — bead hidden until the first position arrives)
//
//  2. Go emits "position" (node, port, x, y, z) every ~16 ms while in flight, and
//     once more at t==1 just before delivery
//     → pump.ts  filters ALL RF edges by source+sourceHandle (fan-out)
//     → pulse-state.ts:setPulsePos  sets the bead's Go-computed world position;
//       PulseBead (scene-content.tsx) plots pulse.pos directly — no curve sampling
//
//  3. Go emits "done" (node, port)
//     → pump.ts  filters ALL RF edges by target+targetHandle (fan-in)
//     → pulse-state.ts:clearPulse  removes the pulse (bead disappears)
// ────────────────────────────────────────────────────────────────────────────

import type { TraceEvent } from "../../messages";
import type { TraceEventKind } from "./trace-kinds";
import { useThreeStore } from "./store";
import { postLog } from "../log/post";
import { setPulse, setPulsePos, clearPulse } from "./pulse-state";
import { useEdgeGeometryStore } from "./edge-geometry";

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
    case "recv":
      return;
    case "fire":
      return;
    case "send": {
      // Match ALL edges by source node id + sourceHandle (fan-out).
      // RF edges store source/sourceHandle; trace send events carry node/port.
      // The send event ESTABLISHES the in-flight bead (value + routing identity);
      // its world position is filled in by the position stream (Go-computed). TS
      // no longer derives any timing/geometry from the send event.
      const { node, port, value, target: goTarget, targetHandle: goTargetHandle } = event as Extract<TraceEvent, { kind: "send" }>;
      const edges = useThreeStore.getState().edges;
      const matched = edges.filter(
        (e) => e.source === node && e.sourceHandle === port,
      );
      for (const edge of matched) {
        postLog("phase4.pump", { layer: "pump", step, node, port: port ?? null, edgeId: edge.id });
        // Prefer Go-provided slot identity (target, targetHandle) — these are
        // authoritative from PacedWire and are not derived from edge data, which
        // can be empty for live-re-added edges.
        setPulse(edge.id, {
          value: value ?? 0,
          simStep: step,
          target: (goTarget != null && goTarget !== "") ? goTarget : (edge.target ?? ""),
          targetHandle: (goTargetHandle != null && goTargetHandle !== "") ? goTargetHandle : (edge.targetHandle ?? ""),
        });
      }
      return;
    }
    case "position": {
      // Go's per-frame bead position (Phase 2). Match ALL edges by source node id
      // + sourceHandle (fan-out), same key as send, and set the bead's world
      // position directly — TS plots, computes no geometry.
      const { node, port, x, y, z } = event as Extract<TraceEvent, { kind: "position" }>;
      const edges = useThreeStore.getState().edges;
      const matched = edges.filter(
        (e) => e.source === node && e.sourceHandle === port,
      );
      for (const edge of matched) {
        setPulsePos(edge.id, x, y, z);
      }
      return;
    }
    case "geometry": {
      // Go's authoritative edge curve (Phase 3). Keyed by edge id (== Go edge
      // label); store the control points so SingleEdgeTube draws the wire tube from
      // them. TS computes no geometry — this is the sole source of tube shape.
      const { edge, p0x, p0y, p0z, p1x, p1y, p1z, p2x, p2y, p2z } = event as Extract<TraceEvent, { kind: "geometry" }>;
      useEdgeGeometryStore.getState().setEdgeCurve(edge, {
        p0: { x: p0x, y: p0y, z: p0z },
        p1: { x: p1x, y: p1y, z: p1z },
        p2: { x: p2x, y: p2y, z: p2z },
      });
      return;
    }
    case "pulse-cancelled": {
      // Go dropped an in-flight bead (edge deleted mid-flight, Phase 3). Match ALL
      // edges by source node id + sourceHandle (fan-out, same key as send/position)
      // and remove the bead sprite. The edge itself may already be gone from the
      // store (deleteEdge removes it locally); clearPulse is a safe no-op then.
      const { node, port } = event as Extract<TraceEvent, { kind: "pulse-cancelled" }>;
      const edges = useThreeStore.getState().edges;
      const matched = edges.filter(
        (e) => e.source === node && e.sourceHandle === port,
      );
      for (const edge of matched) {
        clearPulse(edge.id);
      }
      return;
    }
    case "node-geometry":
      // TODO(item1-ts): consume — each node's goroutine emits its node+port world
      // positions/dirs on startup. Pure no-op this commit; the TS consume lands next.
      return;
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
