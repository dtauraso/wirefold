// pump.ts — trace-event translator. Pure mapping: Go trace events → dedicated state stores.
// No state machine, no phase tracking, no Go logic.
// pump.ts is render-only; Go poll loops live in Go. See MODEL.md §Driver.
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
//  2. Go emits "edge-bead" (node, port, x, y, z) every ~16 ms while in flight, and
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
import { useCameraStore } from "./camera-store";
import { useThreeStore } from "./store";
import { patchViewerState } from "../state/viewer-state";
import { scheduleViewSave } from "../save";
import { postLog } from "../log/post";
import { setPulsePos, clearPulse } from "./pulse-state";
import { setInteriorBead } from "./interior-bead-state";
import { useEdgeGeometryStore } from "./edge-geometry";
import { useNodeGeometryStore } from "./node-geometry";

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
      // A fire starts a clock-paced train: ONE send fires but N beads ride the
      // wire, each minted (and keyed) by Go's per-bead gen. The per-bead slot is
      // therefore established by the edge-bead (position) stream, not the send —
      // the send no longer carries a bead id and creates no slot. Logged only.
      const { node, port } = event as Extract<TraceEvent, { kind: "send" }>;
      postLog("phase4.pump", { layer: "pump", step, node, port: port ?? null });
      return;
    }
    case "edge-bead": {
      // Go's per-frame bead position (Phase 2). Match ALL edges by source node id
      // + sourceHandle (fan-out), same key as send, and set the bead's world
      // position directly — TS plots, computes no geometry.
      const { node, port, value, x, y, z, f, bead } = event as Extract<TraceEvent, { kind: "edge-bead" }>;
      const edges = useThreeStore.getState().edges;
      const matched = edges.filter(
        (e) => e.source === node && e.sourceHandle === port,
      );
      for (const edge of matched) {
        // x/y/z is Go's fallback world position; f is the bead's fractional progress.
        // PulseBead places the bead at lerp(liveStart, liveEnd, f) on the editor's
        // LOCAL node port positions so it rides the live wire during a drag. bead
        // keys this position to the right in-flight bead (N beads per wire); this
        // position event establishes the bead's slot the first time it is seen.
        setPulsePos(edge.id, bead ?? 0, value ?? 0, x, y, z, f);
      }
      return;
    }
    case "geometry": {
      // Go's authoritative edge segment (Phase 3). Keyed by edge id (== Go edge
      // label); store the endpoints so SingleEdgeTube draws the wire tube as a
      // LineCurve3 from Start to End. TS computes no geometry.
      const { edge, sx, sy, sz, ex, ey, ez } = event as Extract<TraceEvent, { kind: "geometry" }>;
      useEdgeGeometryStore.getState().setEdgeSegment(edge, {
        start: { x: sx, y: sy, z: sz },
        end: { x: ex, y: ey, z: ez },
      });
      return;
    }
    case "pulse-cancelled": {
      // Go dropped an in-flight bead (edge deleted mid-flight, Phase 3). Match ALL
      // edges by source node id + sourceHandle (fan-out, same key as send/position)
      // and remove the bead sprite. The edge itself may already be gone from the
      // store (deleteEdge removes it locally); clearPulse is a safe no-op then.
      const { node, port, bead } = event as Extract<TraceEvent, { kind: "pulse-cancelled" }>;
      const edges = useThreeStore.getState().edges;
      const matched = edges.filter(
        (e) => e.source === node && e.sourceHandle === port,
      );
      for (const edge of matched) {
        clearPulse(edge.id, bead ?? 0);
      }
      return;
    }
    case "node-geometry": {
      // Each node's goroutine emits its node center + per-port world positions/dirs
      // (Three y-up frame; Go mirrors geometry-helpers.ts). Pure store-write — no
      // geometry math here (drift rule). The geometry helpers read this store.
      const e = event as Extract<TraceEvent, { kind: "node-geometry" }>;
      useNodeGeometryStore.getState().setNodeGeometry(
        e.node,
        { x: e.nx, y: e.ny, z: e.nz },
        e.radius,
        e.sphereR ?? e.radius,
        e.vrx, e.vry, e.vrz,
        e.frx, e.fry, e.frz,
        e.ports.map((p) => ({
          name: p.name,
          isInput: p.isInput,
          pos: { x: p.px, y: p.py, z: p.pz },
          dir: { x: p.dx, y: p.dy, z: p.dz },
        })),
      );
      return;
    }
    case "arrive": {
      // Bead COMPLETED its traversal — delivered into the dest slot (f reached the
      // end). Match ALL edges by source node id + sourceHandle (fan-out, same key
      // as send/position/pulse-cancelled) and clear the transit pulse: the in-flight
      // bead vanishes the instant it arrives, NOT when the node later consumes the
      // held value (that's "done"). deliverLocked fires arrive exactly once per bead.
      const { node, port, bead } = event as Extract<TraceEvent, { kind: "arrive" }>;
      const edges = useThreeStore.getState().edges;
      const matched = edges.filter(
        (e) => e.source === node && e.sourceHandle === port,
      );
      for (const edge of matched) {
        clearPulse(edge.id, bead ?? 0);
      }
      return;
    }
    case "node-bead": {
      // One slot of node 1's 2x2 interior buffer (Go-computed slot position + present
      // flag, keyed by node + row/col). Go emits a 4-slot snapshot per array change;
      // each event writes one slot into the interior-bead store. present=false marks
      // the slot empty so a popped bead disappears. Pure store-write — pump computes
      // no geometry (Go owns the slot positions); InteriorBeads renders from the store.
      const { node, row, col, present, value, x, y, z } = event as Extract<TraceEvent, { kind: "node-bead" }>;
      setInteriorBead(node, row, col, present, value ?? 0, { x, y, z });
      return;
    }
    // PUMP_DONE_HANDLER
    case "done": {
      // The consumer finished USING the held value (node's firing rule ran). Held
      // value/badge is intentionally NOT cleared here — badges are sticky and show
      // the last value received per input port until overwritten by a new send.
      // The transit pulse is NOT cleared here either: it already vanished on
      // "arrive" (traversal-complete). Clearing on done made the bead LINGER at the
      // dest port until consume, which in a ring can lag arrival noticeably.
      const { node, port } = event as Extract<TraceEvent, { kind: "done" }>;
      postLog("phase4.pump.done", { layer: "pump.done", step, node, port: port ?? null });
      return;
    }
    case "camera": {
      const e = event as Extract<TraceEvent, { kind: "camera" }>;
      const polar = {
        pivot: [e.px, e.py, e.pz] as [number, number, number],
        r: e.r,
        pos: [e.posTheta, e.posPhi] as [number, number],
        up: [e.upTheta, e.upPhi] as [number, number],
      };
      useCameraStore.getState().set(polar);
      patchViewerState((v) => { v.cameraPolar = polar; });
      scheduleViewSave();
      return;
    }
    case "scene-tori": {
      const e = event as Extract<TraceEvent, { kind: "scene-tori" }>;
      postLog("guide-recv", { kind: "scene-tori", visible: e.visible });
      useCameraStore.getState().setSceneToriVisible(e.visible);
      patchViewerState((v) => { v.sceneToriVisible = e.visible === false ? false : undefined; });
      scheduleViewSave();
      return;
    }
    case "scene-poles": {
      const e = event as Extract<TraceEvent, { kind: "scene-poles" }>;
      postLog("guide-recv", { kind: "scene-poles", visible: e.visible });
      useCameraStore.getState().setScenePolesVisible(e.visible);
      patchViewerState((v) => { v.scenePolesVisible = e.visible === false ? false : undefined; });
      scheduleViewSave();
      return;
    }
    case "node-poles": {
      const e = event as Extract<TraceEvent, { kind: "node-poles" }>;
      postLog("guide-recv", { kind: "node-poles", visible: e.visible });
      useCameraStore.getState().setNodePolesVisible(e.visible);
      patchViewerState((v) => { v.nodePolesVisible = e.visible === false ? false : undefined; });
      scheduleViewSave();
      return;
    }
    case "angle-labels": {
      const e = event as Extract<TraceEvent, { kind: "angle-labels" }>;
      postLog("guide-recv", { kind: "angle-labels", visible: e.visible });
      useCameraStore.getState().setAngleLabelsVisible(e.visible);
      patchViewerState((v) => { v.angleLabelsVisible = e.visible === false ? false : undefined; });
      scheduleViewSave();
      return;
    }
    case "sel-sphere-poles": {
      const e = event as Extract<TraceEvent, { kind: "sel-sphere-poles" }>;
      postLog("guide-recv", { kind: "sel-sphere-poles", visible: e.visible });
      useCameraStore.getState().setSelSpherePolesVisible(e.visible);
      patchViewerState((v) => { v.selSpherePolesVisible = e.visible === false ? false : undefined; });
      scheduleViewSave();
      return;
    }
    case "handholds": {
      const e = event as Extract<TraceEvent, { kind: "handholds" }>;
      postLog("guide-recv", { kind: "handholds", visible: e.visible });
      useCameraStore.getState().setHandholdsVisible(e.visible);
      patchViewerState((v) => { v.handholdsVisible = e.visible === false ? false : undefined; });
      scheduleViewSave();
      return;
    }
    case "labels-global": {
      // Note: visible sense → hidden sense flip at the render boundary.
      // Go emits visible=true when labels should show; store holds hidden=false.
      const e = event as Extract<TraceEvent, { kind: "labels-global" }>;
      useCameraStore.getState().setLabelsGlobalHidden(!e.visible);
      patchViewerState((v) => { v.labelsGlobalHidden = !e.visible || undefined; });
      scheduleViewSave();
      return;
    }
    default:
      assertNever(k);
  }
}
