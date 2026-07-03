// buffer-nav.ts — buffer-driven nav-overlay data source (buffer-only path).
//
// The binary snapshot's node block carries per-node cx/cy/cz/radius/sphereR/selected AND the
// per-node label (LabelOff/LabelLen into the trailing label section — see buffer-decode
// nodeLabel). Identity in this system is the buffer NODE-ROW INDEX: Go resolves a row back to
// its node id (Buffer.SnapshotState.LookupNodeRow) for any topology edit, so the webview needs
// no node-id strings at all. This module is the pure decode that turns the numeric node rows
// (paired with their decoded labels) into NavNode records so NavGuides / label-pills /
// occlusion badges can run entirely off the buffer, keyed by row.
//
// ── Ordering guarantee (row i ↔ buffer node row i) ─────────────────────────────
// Go's Buffer.SnapshotState assigns each node its row on the FIRST KindNodeGeometry event it
// sees for that id (insertion order; re-emits on a move do not reorder — see
// Buffer/snapshot.go onNodeGeometry) and writes the Node block + label section in that same
// order. decodeNavNodes walks the node block in row order, so NavNode i is buffer node row i
// by construction — no id table, no sidecar.
//
// This is a RENDERING RESOURCE keyed by row, NOT a domain store of positions or topology:
// positions/radii/sphereR/selection/label all come from the buffer via decodeNavNodes.

import * as THREE from "three";
import { type DecodedSnapshot, nodeLabel } from "./buffer-decode";
import {
  readNodeCX, readNodeCY, readNodeCZ,
  readNodeRadius, readNodeSphereR, readNodeSelected,
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
      // absent by callers (lockArc `if(!pr)`, sel-poles `sphereR || radius`).
      sphereR: sphereR || undefined,
      selected: readNodeSelected(nodeView, i) !== 0,
    });
  }
  return out;
}

/**
 * Content sphere (arcball) from precomputed node centers: bounding-box center +
 * farthest-node radius (+10% margin, radius ≥ 1). Mirrors
 * geometry-helpers.contentSphere line-for-line but takes centers directly, so the
 * buffer path and the RFNode path produce identical spheres from identical centers.
 * Returns (0,0,0)/100 when there are no finite centers.
 */
export function contentSphereFromCenters(centers: THREE.Vector3[]): { center: THREE.Vector3; radius: number } {
  const center = new THREE.Vector3();
  if (centers.length === 0) return { center, radius: 100 };
  const min = new THREE.Vector3(Infinity, Infinity, Infinity);
  const max = new THREE.Vector3(-Infinity, -Infinity, -Infinity);
  let any = false;
  for (const p of centers) {
    if (!Number.isFinite(p.x) || !Number.isFinite(p.y) || !Number.isFinite(p.z)) continue;
    min.min(p); max.max(p); any = true;
  }
  if (!any) return { center, radius: 100 };
  center.addVectors(min, max).multiplyScalar(0.5);
  let r = 0;
  for (const p of centers) {
    if (!Number.isFinite(p.x) || !Number.isFinite(p.y) || !Number.isFinite(p.z)) continue;
    r = Math.max(r, p.distanceTo(center));
  }
  return { center, radius: Math.max(r * 1.1, 1) };
}
