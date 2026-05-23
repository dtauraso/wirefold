// pump.ts — trace-event translator. Pure mapping: Go trace events → RF state.
// No state machine, no phase tracking, no substrate logic.
// pump.ts is render-only; substrate poll loops live in Go. See MODEL.md §Driver.
// Each call to handleTraceEvent reads the current RF state, patches the
// relevant node/edge data field, and writes it back via rfSetNodes/rfSetEdges.

import type { TraceEvent, SlotEvent, SlotMap } from "../../messages";
import type { TraceEventKind } from "./trace-kinds";
import { rfSetNodes, rfSetEdges, rfGetEdges } from "./rf-imperative";
import { postLog } from "../log/post";
import { ANIMATION_FIELDS } from "./animation-fields";
import { setHeldValue } from "./held-values";

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
      rfSetNodes((nodes) =>
        nodes.map((n) => {
          if (n.id !== nodeId) return n;
          const prev: SlotMap = (n.data as { slots?: SlotMap }).slots ?? {};
          const next: SlotMap = {
            ...prev,
            [port]: phase === "filled"
              ? { phase: "filled", value: value ?? 0 }
              : { phase: "empty" },
          };
          return { ...n, data: { ...n.data, slots: next } };
        }),
      );
      return;
    }
    case "recv":
      return;
    case "fire": {
      const { node } = event as Extract<TraceEvent, { kind: "fire" }>;
      rfSetNodes((nodes) =>
        nodes.map((n) =>
          n.id === node
            ? { ...n, data: { ...n.data, [ANIMATION_FIELDS.lastFire.name]: step } }
            : n,
        ),
      );
      return;
    }
    case "send": {
      // Match the edge by source node id + sourceHandle (output port name).
      // RF edges store source/sourceHandle; trace send events carry node/port.
      const { node, port, value } = event as Extract<TraceEvent, { kind: "send" }>;
      const edges = rfGetEdges();
      const edgeId = edges.find(
        (e) => e.source === node && e.sourceHandle === port,
      )?.id;
      console.log(`[pump] send step=${step} node=${node} port=${port} edgeId=${edgeId ?? "NO-MATCH"} edges=[${edges.map(e => `${e.source}:${e.sourceHandle}`).join(",")}]`);
      postLog("phase4.pump", { layer: "pump", step, node, port: port ?? null, edgeId: edgeId ?? null });
      if (!edgeId) return; // no matching edge — topology mismatch, skip silently
      // Eagerly record the held value at the destination port so the node can
      // show it while the pulse animates and until Go signals Done.
      const edge = edges.find((e) => e.id === edgeId);
      if (edge?.target && edge.targetHandle) {
        setHeldValue(edge.target, edge.targetHandle, value ?? 0);
      }
      rfSetEdges((es) =>
        es.map((e) =>
          e.id === edgeId
            ? { ...e, data: { ...e.data, [ANIMATION_FIELDS.pulse.name]: { value: value ?? 0, simStep: step } } }
            : e,
        ),
      );
      return;
    }
    case "done": {
      // Match the edge by target node id + targetHandle (input port name).
      // RF edges store target/targetHandle; trace done events carry node/port.
      const { node, port } = event as Extract<TraceEvent, { kind: "done" }>;
      const edges = rfGetEdges();
      const edgeId = edges.find(
        (e) => e.target === node && e.targetHandle === port,
      )?.id;
      if (!edgeId) return; // no matching edge — topology mismatch, skip silently
      // Held value is intentionally NOT cleared here — badges are sticky and
      // show the last value received per input port until overwritten by a new send.
      rfSetEdges((es) =>
        es.map((e) =>
          e.id === edgeId
            ? { ...e, data: { ...e.data, [ANIMATION_FIELDS.pulse.name]: undefined } }
            : e,
        ),
      );
      return;
    }
    default:
      assertNever(k);
  }
}
