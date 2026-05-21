// pump.ts — trace-event translator. Pure mapping: Go trace events → RF state.
// No state machine, no phase tracking, no substrate logic.
// Each call to handleTraceEvent reads the current RF state, patches the
// relevant node/edge data field, and writes it back via rfSetNodes/rfSetEdges.

import type { TraceEvent } from "../../messages";
import { rfSetNodes, rfSetEdges, rfGetEdges } from "./rf-imperative";
import { postLog } from "../log/post";
import { ANIMATION_FIELDS } from "./animation-fields";

export function handleTraceEvent(event: TraceEvent): void {
  const { step, kind, node, port, value } = event;
  switch (kind) {
    case "recv":
      return;
    case "fire":
      rfSetNodes((nodes) =>
        nodes.map((n) =>
          n.id === node
            ? { ...n, data: { ...n.data, [ANIMATION_FIELDS.lastFire.name]: step } }
            : n,
        ),
      );
      return;
    case "send": {
      // Match the edge by source node id + sourceHandle (output port name).
      // RF edges store source/sourceHandle; trace send events carry node/port.
      const edges = rfGetEdges();
      const edgeId = edges.find(
        (e) => e.source === node && e.sourceHandle === port,
      )?.id;
      console.log(`[pump] send step=${step} node=${node} port=${port} edgeId=${edgeId ?? "NO-MATCH"} edges=[${edges.map(e => `${e.source}:${e.sourceHandle}`).join(",")}]`);
      postLog("phase4.pump", { layer: "pump", step, node, port: port ?? null, edgeId: edgeId ?? null });
      if (!edgeId) return; // no matching edge — topology mismatch, skip silently
      rfSetEdges((es) =>
        es.map((e) =>
          e.id === edgeId
            ? { ...e, data: { ...e.data, [ANIMATION_FIELDS.pulse.name]: { value: value ?? 0, simStep: step } } }
            : e,
        ),
      );
      return;
    }
  }
}
