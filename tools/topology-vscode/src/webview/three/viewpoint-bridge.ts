// viewpoint-bridge.ts — fire-and-forget helpers for sending polar-camera viewpoint
// edits to Go via the TS→Go bridge. No await, no Promise, no delivery signal.
// See CLAUDE.md §Bridge surface.

import { vscode } from "../vscode-api";

/** Tell Go to set camera state (all fields optional; omitted fields are unchanged). */
export function sendViewpointSet(
  pivot: [number, number, number],
  r: number,
  pos: [number, number],
  up: [number, number],
): void {
  const [pivotX, pivotY, pivotZ] = pivot;
  const [posTheta, posPhi] = pos;
  const [upTheta, upPhi] = up;
  vscode.postMessage({
    type: "edit",
    op: "viewpoint",
    viewpoint: { kind: "set", pivotX, pivotY, pivotZ, r, posTheta, posPhi, upTheta, upPhi },
  });
}

/** Tell Go to orbit from one spherical position to another. */
export function sendViewpointOrbit(
  from: [number, number],
  to: [number, number],
): void {
  const [fromTheta, fromPhi] = from;
  const [toTheta, toPhi] = to;
  vscode.postMessage({
    type: "edit",
    op: "viewpoint",
    viewpoint: { kind: "orbit", fromTheta, fromPhi, toTheta, toPhi },
  });
}

/** Tell Go to zoom by a multiplicative factor. */
export function sendViewpointZoom(factor: number): void {
  vscode.postMessage({
    type: "edit",
    op: "viewpoint",
    viewpoint: { kind: "zoom", factor },
  });
}

/** Tell Go to pan the pivot point by a world-space delta. */
export function sendViewpointPan(dx: number, dy: number, dz: number): void {
  vscode.postMessage({
    type: "edit",
    op: "viewpoint",
    viewpoint: { kind: "pan", dx, dy, dz },
  });
}
