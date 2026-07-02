// raw-input.ts — RAW-INPUT forwarding.
//
// The editor forwards RAW pointer/wheel events plus the stateless three.js raycast hit to
// Go, fire-and-forget; Go's gesture state machine (nodes/Wiring/gesture.go) decides what the
// input MEANS. TS holds NO gesture state.
//
// The raycast + hit classification (three.js hit-testing) live HERE, not in the polar-only
// interaction-*.ts files, so the polar-nav guard is unaffected.

import * as THREE from "three";
import { vscode } from "../vscode-api";
import type { RawInputEvent, RawHit, RawPointerKind } from "../../messages";
import type { PickOptions } from "./interaction-controls";
import { pixelToNDC } from "./geometry-helpers";

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
function classifyHit(pickRequest: PickRef, ndcX: number, ndcY: number): { kind: RawHit["kind"]; id: string; isInput: boolean; portRow: number; edgeRow: number } {
  // A port hit carries ONLY its numeric buffer PORT-ROW index (pickBufferPort returns the row
  // as a string). No name/side string — Go resolves the row → (node, port, isInput) via its
  // own port-row table, so id stays empty and isInput is irrelevant here. Priority: port,
  // then edge, then node.
  const portStr = pickRequest.current?.(ndcX, ndcY, { portOnly: true }) ?? null;
  if (portStr !== null) return { kind: "port", id: "", isInput: false, portRow: Number(portStr), edgeRow: -1 };
  // An edge hit carries ONLY its numeric buffer EDGE-ROW index (pickBufferEdge returns the
  // row as a string). Go resolves the row → its edge via its own edge-row table, so id stays
  // empty here.
  const edgeStr = pickRequest.current?.(ndcX, ndcY, { edgeOnly: true }) ?? null;
  if (edgeStr !== null) return { kind: "edge", id: "", isInput: false, portRow: -1, edgeRow: Number(edgeStr) };
  const node = pickRequest.current?.(ndcX, ndcY) ?? null;
  if (node !== null) return { kind: "node", id: node, isInput: false, portRow: -1, edgeRow: -1 };
  return { kind: "empty", id: "", isInput: false, portRow: -1, edgeRow: -1 };
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
  const hit: RawHit = { kind: c.kind, id: c.id, isInput: c.isInput, portRow: c.portRow, edgeRow: c.edgeRow, x: p.x, y: p.y, z: p.z };
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

/** Build a "home" (fit-to-content) command event. Carries ONLY the render context Go needs
 *  to size the fit — camera fov + viewport aspect (encoded as rectWidth/rectHeight so Go's
 *  rect.aspect() reads width/height = aspect). No pose is computed here; Go frames the scene
 *  from its own node geometry. Pointer/hit fields are inert (unused for a home command). */
export function buildHomeRaw(fov: number, aspect: number): RawInputEvent {
  const hit: RawHit = { kind: "empty", id: "", isInput: false, portRow: -1, edgeRow: -1, x: 0, y: 0, z: 0 };
  return {
    kind: "home",
    x: 0, y: 0,
    rectLeft: 0, rectTop: 0, rectWidth: aspect, rectHeight: 1,
    button: -1,
    ctrl: false, shift: false, alt: false, meta: false,
    deltaX: 0, deltaY: 0,
    fov,
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
  const hit: RawHit = { kind: c.kind, id: c.id, isInput: c.isInput, portRow: c.portRow, edgeRow: c.edgeRow, x: p.x, y: p.y, z: p.z };
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
