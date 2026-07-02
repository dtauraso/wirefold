// raw-input.ts — Phase 6 RAW-INPUT forwarding (OFF by default behind USE_RAW_INPUT).
//
// When enabled, the editor forwards RAW pointer/wheel events plus the stateless three.js
// raycast hit to Go, fire-and-forget; Go's gesture state machine (nodes/Wiring/gesture.go)
// decides what the input MEANS. TS holds NO gesture state. When DISABLED (the default), this
// module is inert and the current interaction handlers run exactly as before.
//
// This is the SAME dev-flag family as buffer-scene.ts's USE_BUFFER_RENDER: additive,
// off-by-default scaffolding for the buffer/agnostic-content-buffer flip. The raycast + hit
// classification (three.js hit-testing) live HERE, not in the polar-only interaction-*.ts
// files, so the polar-nav guard is unaffected.

import * as THREE from "three";
import { vscode } from "../vscode-api";
import type { RawInputEvent, RawHit, RawPointerKind } from "../../messages";
import type { PickOptions } from "./interaction-controls";
import { pixelToNDC } from "./geometry-helpers";
import { USE_NEW_SYSTEM } from "../new-system";

/** Follows the ONE master switch (USE_NEW_SYSTEM). ON = forward raw input to Go's gesture
 *  FSM; OFF (default) = the current interaction handlers run byte-for-byte as today. */
export const USE_RAW_INPUT = USE_NEW_SYSTEM;

type PickRef = React.MutableRefObject<
  ((ndcX: number, ndcY: number, opts?: PickOptions) => string | null) | null
>;
type CamRef = React.MutableRefObject<THREE.PerspectiveCamera | null>;

/** Fire-and-forget: place a raw-input event on the TS→Go bridge. No await, no response. */
export function sendRawInput(event: RawInputEvent): void {
  vscode.postMessage({ type: "raw-input", event });
}

/** Classify the rendered entity under the pointer via the existing pick callback (three.js
 *  raycast). Port ids are "nodeId:in|out:portName"; isInput is read off that id. Topology
 *  facts (connected?) are NOT decided here — Go's FSM owns those. */
function classifyHit(pickRequest: PickRef, ndcX: number, ndcY: number): { kind: RawHit["kind"]; id: string; isInput: boolean } {
  const port = pickRequest.current?.(ndcX, ndcY, { portOnly: true }) ?? null;
  if (port !== null) {
    const i = port.indexOf(":");
    const rest = port.slice(i + 1);
    const dir = rest.slice(0, rest.indexOf(":"));
    return { kind: "port", id: port, isInput: dir === "in" };
  }
  const handhold = pickRequest.current?.(ndcX, ndcY, { handholdOnly: true }) ?? null;
  if (handhold !== null) return { kind: "handhold", id: handhold, isInput: false };
  const node = pickRequest.current?.(ndcX, ndcY) ?? null;
  if (node !== null) return { kind: "node", id: node, isInput: false };
  return { kind: "empty", id: "", isInput: false };
}

/** World point under the pointer: unproject the pointer ray onto the z=0 plane. This is the
 *  raycast "hit point" three.js contributes (Go decides what it means). */
function hitWorldPoint(cam: THREE.PerspectiveCamera, ndcX: number, ndcY: number): { x: number; y: number; z: number } {
  const raycaster = new THREE.Raycaster();
  raycaster.setFromCamera(new THREE.Vector2(ndcX, ndcY), cam);
  const plane = new THREE.Plane(new THREE.Vector3(0, 0, 1), 0);
  const target = new THREE.Vector3();
  const hit = raycaster.ray.intersectPlane(plane, target);
  return hit ? { x: target.x, y: target.y, z: target.z } : { x: 0, y: 0, z: 0 };
}

/** Build a RawInputEvent from a React pointer event + the raycast hit. */
export function buildPointerRaw(
  e: React.PointerEvent<HTMLDivElement>,
  kind: RawPointerKind,
  cameraRef: CamRef,
  pickRequest: PickRef,
): RawInputEvent | null {
  const cam = cameraRef.current;
  if (!cam) return null;
  const rect = e.currentTarget.getBoundingClientRect();
  const { ndcX, ndcY } = pixelToNDC(e.clientX, e.clientY, rect);
  const c = classifyHit(pickRequest, ndcX, ndcY);
  const p = hitWorldPoint(cam, ndcX, ndcY);
  const hit: RawHit = { kind: c.kind, id: c.id, isInput: c.isInput, x: p.x, y: p.y, z: p.z };
  return {
    kind,
    x: e.clientX, y: e.clientY,
    rectLeft: rect.left, rectTop: rect.top, rectWidth: rect.width, rectHeight: rect.height,
    button: e.button,
    ctrl: e.ctrlKey, shift: e.shiftKey, alt: e.altKey, meta: e.metaKey,
    deltaX: 0, deltaY: 0,
    fov: cam.fov,
    hit,
  };
}

/** Build a RawInputEvent from a native wheel event + the raycast hit. */
export function buildWheelRaw(
  e: WheelEvent,
  cameraRef: CamRef,
  pickRequest: PickRef,
): RawInputEvent | null {
  const cam = cameraRef.current;
  if (!cam) return null;
  const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
  const { ndcX, ndcY } = pixelToNDC(e.clientX, e.clientY, rect);
  const c = classifyHit(pickRequest, ndcX, ndcY);
  const p = hitWorldPoint(cam, ndcX, ndcY);
  const hit: RawHit = { kind: c.kind, id: c.id, isInput: c.isInput, x: p.x, y: p.y, z: p.z };
  return {
    kind: "wheel",
    x: e.clientX, y: e.clientY,
    rectLeft: rect.left, rectTop: rect.top, rectWidth: rect.width, rectHeight: rect.height,
    button: -1,
    ctrl: e.ctrlKey, shift: e.shiftKey, alt: e.altKey, meta: e.metaKey,
    deltaX: e.deltaX, deltaY: e.deltaY,
    fov: cam.fov,
    hit,
  };
}
