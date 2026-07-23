// buffer-nav.ts — buffer-driven nav-overlay data source (buffer-only path).
//
// The binary snapshot's node block carries per-node cx/cy/cz/radius/sphereR/selected AND the
// per-node label (LabelOff/LabelLen into the trailing label section — see buffer-decode
// nodeLabel). Identity in this system is the buffer NODE-ROW INDEX: Go resolves a row back to
// its node id (nodes/Wiring's MoveDispatch.LookupNodeRow) for any topology edit, so the
// webview needs no node-id strings at all. This module is the pure decode that turns the
// numeric node rows (paired with their decoded labels) into NavNode records so NavGuides /
// label-pills can run entirely off the buffer, keyed by row.
//
// ── Ordering guarantee (row i ↔ buffer node row i) ─────────────────────────────
// Row order is a LOAD-TIME CONSTANT (nodes/Wiring's md.nodeSeeds, spec order — see
// MoveDispatch's row-identity table doc comment): each node's own nodeMover writes the Node
// block + label section into its own dedicated stream frame at that same row. decodeNavNodes
// walks the aggregated node block in row order, so NavNode i is buffer node row i by
// construction — no id table, no sidecar.
//
// This is a RENDERING RESOURCE keyed by row, NOT a domain store of positions or topology:
// positions/radii/sphereR/selection/label all come from the buffer via decodeNavNodes.

import * as THREE from "three";
import { type DecodedSnapshot, nodeLabel } from "./buffer-decode";
import {
  readNodeCX, readNodeCY, readNodeCZ,
  readNodeRadius, readNodeSphereR, readNodeSelected, readNodeLatchedSel,
  readSceneCX, readSceneCY, readSceneCZ, readSceneRadius,
} from "../../schema/buffer-layout";

/** One node's nav-overlay geometry, decoded from the buffer. Identity is `row` (its buffer
 *  node-row index); `label` is the human label decoded from the buffer's label section. */
export interface NavNode {
  /** Buffer node-row index — the identity Go resolves back to a node id (LookupNodeRow). */
  row: number;
  /** Human label decoded from the buffer's label section ("" when the node has no label). */
  label: string;
  center: THREE.Vector3;
  radius: number;
  /** Go's per-node sphere radius. 0 means "not yet populated" (pre-first-geometry). */
  sphereR: number | undefined;
  selected: boolean;
  /** Go-owned: 1 marks the LAST node that was click-selected, persisting through a
   *  deselect (see LatchedSel in Buffer/layout.go; set by that node's own nodeMover). */
  latchedSel: boolean;
}

// ── Pure decode ───────────────────────────────────────────────────────────────

/**
 * Decode the buffer's node block into NavNode records. NavNode i is buffer node row i (the
 * ordering invariant above); its label is decoded from the buffer's label section. Pure — no
 * store reads/writes.
 */
export function decodeNavNodes(decoded: DecodedSnapshot): NavNode[] {
  const { nodeCount, nodeView } = decoded;
  const out: NavNode[] = [];
  for (let i = 0; i < nodeCount; i++) {
    const sphereR = readNodeSphereR(nodeView, i);
    out.push({
      row: i,
      label: nodeLabel(decoded, i),
      center: new THREE.Vector3(
        readNodeCX(nodeView, i),
        readNodeCY(nodeView, i),
        readNodeCZ(nodeView, i),
      ),
      radius: readNodeRadius(nodeView, i),
      // Preserve the old-path "sphereR may be missing" semantics: 0 → treated as
      // absent by callers (sel-poles `sphereR || radius`).
      sphereR: sphereR || undefined,
      selected: readNodeSelected(nodeView, i) !== 0,
      latchedSel: readNodeLatchedSel(nodeView, i) !== 0,
    });
  }
  return out;
}

/**
 * The scene sphere — Go's persisted, first-class world anchor (nodes/Wiring/sphere_layout.go
 * sceneSphere), read from the buffer's Scene block (see readSceneCX../readSceneRadius,
 * KindSceneSphere). Established ONCE at load and never moves; this is NOT a derived
 * content-sphere centroid recomputed from live node positions (that sphere moves with the
 * nodes and is circular — Go's own comment on sceneSphere calls this out explicitly).
 * Falls back to (0,0,0)/100 before the one-time startup event has landed (radius 0 in the
 * buffer, mirroring the sphereR "0 = not yet populated" convention).
 */
export function sceneSphereFromSnapshot(decoded: DecodedSnapshot): { center: THREE.Vector3; radius: number } {
  const radius = readSceneRadius(decoded.sceneView);
  if (radius <= 0) return { center: new THREE.Vector3(), radius: 100 };
  return {
    center: new THREE.Vector3(
      readSceneCX(decoded.sceneView),
      readSceneCY(decoded.sceneView),
      readSceneCZ(decoded.sceneView),
    ),
    radius,
  };
}
