// buffer-nav.ts — buffer-driven nav-overlay data source (new-system path).
//
// The binary snapshot's node block carries per-node cx/cy/cz/radius/sphereR/selected
// (see buffer-layout.ts node readers) but is NUMERIC — it has no node id/label
// strings. This module supplies the missing label resource and the pure decode that
// pairs the numeric node rows with their ids, so NavGuides / label-pills / occlusion
// badges can run entirely off the buffer under USE_NEW_SYSTEM.
//
// ── Ordering guarantee (id table index i ↔ buffer node row i) ──────────────────
// Go's Buffer.SnapshotState assigns each node its row on the FIRST KindNodeGeometry
// event it sees for that id (insertion order; re-emits on a move do not reorder — see
// Buffer/snapshot.go onNodeGeometry). The webview receives the SAME node-geometry
// trace-event stream, in the SAME emission order (sequential JSONL on stdout →
// ordered postMessage). recordNavNodeId is called from pump.ts's node-geometry case
// with the identical first-seen rule (append on first sight, ignore repeats), so the
// id table is built by the same rule over the same stream — index i therefore maps to
// buffer node row i by construction. Both sides start empty per run (Go re-spawns
// fresh; the webview clears this table at the run-start boundary next to
// clearAllNodeGeometry), keeping the two orderings aligned across edit/reload cycles.
//
// This is a RENDERING RESOURCE keyed by row, NOT a domain store of positions or
// topology: it holds only the ordered ids. Positions/radii/sphereR/selection all come
// from the buffer via decodeNavNodes.

import * as THREE from "three";
import type { DecodedSnapshot } from "./buffer-decode";
import {
  readNodeCX, readNodeCY, readNodeCZ,
  readNodeRadius, readNodeSphereR, readNodeSelected,
} from "../../schema/buffer-layout";

/** One node's nav-overlay geometry, decoded from the buffer + paired with its id. */
export interface NavNode {
  id: string;
  center: THREE.Vector3;
  radius: number;
  /** Go's per-node sphere radius. 0 means "not yet populated" (pre-first-geometry). */
  sphereR: number | undefined;
  selected: boolean;
}

// ── Ordered id table (label resource keyed by buffer row) ─────────────────────
let navNodeIds: string[] = [];
const navNodeIdSet = new Set<string>();

/**
 * Record a node id in first-seen order. Called from pump.ts on every node-geometry
 * trace event; repeats (node re-emits on move) are ignored so the row order matches
 * Go's SnapshotState insertion order exactly.
 */
export function recordNavNodeId(id: string): void {
  if (navNodeIdSet.has(id)) return;
  navNodeIdSet.add(id);
  navNodeIds.push(id);
}

/** Wipe the id table at the run-start boundary (symmetric with clearAllNodeGeometry). */
export function clearNavNodeIds(): void {
  navNodeIds = [];
  navNodeIdSet.clear();
}

/** Current ordered id table; index i ↔ buffer node row i. */
export function getNavNodeIds(): string[] {
  return navNodeIds;
}

// ── Pure decode ───────────────────────────────────────────────────────────────

/**
 * Decode the buffer's node block into NavNode records, pairing row i with ids[i].
 * Pure — no store reads/writes. Rows past the id table's length fall back to a
 * synthetic `#i` id (only possible transiently if a geometry event's row landed in
 * the buffer before its trace event reached the webview).
 */
export function decodeNavNodes(decoded: DecodedSnapshot, ids: string[]): NavNode[] {
  const { nodeCount, nodeView } = decoded;
  const out: NavNode[] = [];
  for (let i = 0; i < nodeCount; i++) {
    const sphereR = readNodeSphereR(nodeView, i);
    out.push({
      id: ids[i] ?? `#${i}`,
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
