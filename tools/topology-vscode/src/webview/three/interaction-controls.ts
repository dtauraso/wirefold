// interaction-controls.ts — useInteractionControls hook + PickOptions.
//
// All interaction is RAW-INPUT forwarding: the pointer/wheel handlers forward the raw event
// plus the stateless three.js raycast hit to Go's gesture FSM (raw-input.ts) and hold NO
// state locally. Go owns every gesture's meaning (select, node-drag, edge-create, orbit,
// pan, dolly, ring-move). TS is forward-only.

import { useCallback } from "react";
import * as THREE from "three";
import { sendRawInput, buildPointerRaw, buildWheelRaw } from "./raw-input";

// ---------------------------------------------------------------------------
// PickOptions (shared with scene-content / raw-input)
// ---------------------------------------------------------------------------

export interface PickOptions {
  excludeId?: string;
  nodesOnly?: boolean;
  ringOnly?: boolean;
  portOnly?: boolean;
  handholdOnly?: boolean;
  /** Restrict the pick to the buffer edge pick-halos (BUFFER_EDGE_TAG), returning the hit
   *  edge's buffer EDGE-ROW index as a decimal string. */
  edgeOnly?: boolean;
}

// ---------------------------------------------------------------------------
// useInteractionControls — raw-input forwarding only
// ---------------------------------------------------------------------------

export function useInteractionControls(
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>,
  pickRequest: React.MutableRefObject<((ndcX: number, ndcY: number, opts?: PickOptions) => string | null) | null>,
) {
  const onPointerDown = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    const ev = buildPointerRaw(e, "pointerdown", cameraRef, pickRequest);
    if (ev) sendRawInput(ev);
    e.currentTarget.setPointerCapture(e.pointerId);
  }, [cameraRef, pickRequest]);

  const onPointerMove = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    const ev = buildPointerRaw(e, "pointermove", cameraRef, pickRequest);
    if (ev) sendRawInput(ev);
  }, [cameraRef, pickRequest]);

  const onPointerUp = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    const ev = buildPointerRaw(e, "pointerup", cameraRef, pickRequest);
    if (ev) sendRawInput(ev);
    e.currentTarget.releasePointerCapture(e.pointerId);
  }, [cameraRef, pickRequest]);

  // A gesture can end via pointercancel (touch interrupted, OS gesture, focus
  // steal) instead of pointerup, and the browser then fires NO pointerup — so
  // without this the Go FSM stays stuck in its drag phase and later buttonless
  // moves keep dragging. Synthesize a pointerup to end the gesture through the
  // normal path. (Do NOT also wire onLostPointerCapture: it fires on a normal
  // pointerup too, which would double-finalize.)
  const onPointerCancel = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    const ev = buildPointerRaw(e, "pointerup", cameraRef, pickRequest);
    if (ev) sendRawInput(ev);
  }, [cameraRef, pickRequest]);

  const onWheelNative = useCallback((e: WheelEvent) => {
    e.preventDefault();
    const ev = buildWheelRaw(e, cameraRef, pickRequest);
    if (ev) sendRawInput(ev);
  }, [cameraRef, pickRequest]);

  return { onPointerDown, onPointerMove, onPointerUp, onPointerCancel, onWheelNative };
}
