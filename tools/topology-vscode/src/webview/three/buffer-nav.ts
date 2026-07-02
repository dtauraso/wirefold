// buffer-nav.ts — buffer-driven nav-overlay data source (new-system path).
//
// The binary snapshot's node block carries per-node cx/cy/cz/radius/sphereR/selected
// (see buffer-layout.ts node readers) but is NUMERIC — it has no node id/label
// strings. This module supplies the missing label resource and the pure decode that
// pairs the numeric node rows with their ids, so NavGuides / label-pills / occlusion
// badges can run entirely off the buffer.
//
// ── Ordering guarantee (id table index i ↔ buffer node row i) ──────────────────
// Go's Buffer.SnapshotState assigns each node its row on the FIRST KindNodeGeometry
// event it sees for that id (insertion order; re-emits on a move do not reorder — see
// Buffer/snapshot.go onNodeGeometry). The webview builds this id table from the SAME
// node-geometry stream by the SAME first-seen rule, so index i maps to buffer node
// row i by construction. Two writers, both first-seen dedup:
//   • The new system: the node-label host→webview sidecar. The host
//     derives one {id,label} per node id — once, in first-seen node-geometry order —
//     from the geometry stream and forwards it independent of pump.ts; main.tsx routes
//     it to recordNavNodeLabel. This is the sole builder when the flag is on (pump is
//     gated off), and it also carries the human label so pills need no spec store.
//   • OLD system (flag off): recordNavNodeId, called from pump.ts's node-geometry case.
// Both sides start empty per run (Go re-spawns fresh; the host clears its per-run dedup
// on the spec/new-run boundary; the webview clears this table at the load boundary next
// to clearAllNodeGeometry), keeping the two orderings aligned across edit/reload cycles.
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

// ── Ordered id table + human-label map (label resource keyed by buffer row) ────
// TWO writers feed navNodeIds, both with the identical first-seen dedup, so the row
// order is the same either way:
//   • recordNavNodeId  — the OLD path (pump.ts node-geometry case). Unused when the
//     new system is on (pump is gated off in main.tsx).
//   • recordNavNodeLabel — the NEW path (the node-label host→webview sidecar, routed
//     in main.tsx, independent of pump). This is the sole writer under the flag and
//     ALSO stores the node's human label so the new-path pills need no spec store.
// navNodeIds is read only by the new render path (NavGuides / BufferLabelProjector /
// occlusion), so the redundant flag-off double-write is invisible.
let navNodeIds: string[] = [];
const navNodeIdSet = new Set<string>();
const navNodeLabels = new Map<string, string>();
// Node's Go KIND (PascalCase, e.g. "Hold") keyed by id — the render path looks it up in
// NODE_DEFS for per-node fill/stroke color. Carried on the same node-label sidecar as the
// human label; empty/undefined until the sidecar message arrives (render falls back then).
const navNodeKinds = new Map<string, string>();

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

/**
 * Record a node id + its human label from the node-label sidecar (main.tsx). Appends
 * the id in first-seen order (same dedup/order as recordNavNodeId) AND stores the
 * label, so the new render path resolves pill text without the old spec store. The
 * label is always set (even on a repeat id) so a label change within a run is picked up.
 */
export function recordNavNodeLabel(id: string, label: string, kind?: string): void {
  if (!navNodeIdSet.has(id)) {
    navNodeIdSet.add(id);
    navNodeIds.push(id);
  }
  navNodeLabels.set(id, label);
  if (kind) navNodeKinds.set(id, kind);
}

/** Human label for a node id (undefined until its sidecar message arrives). */
export function getNavNodeLabel(id: string): string | undefined {
  return navNodeLabels.get(id);
}

/** Go KIND (PascalCase) for a node id (undefined until its sidecar message arrives). */
export function getNavNodeKind(id: string): string | undefined {
  return navNodeKinds.get(id);
}

/** Wipe the id table + label map at the run-start boundary (symmetric with clearAllNodeGeometry). */
export function clearNavNodeIds(): void {
  navNodeIds = [];
  navNodeIdSet.clear();
  navNodeLabels.clear();
  navNodeKinds.clear();
}

/** Current ordered id table; index i ↔ buffer node row i. */
export function getNavNodeIds(): string[] {
  return navNodeIds;
}

/**
 * Resolve a buffer InstancedMesh `instanceId` to its node id. The NodeInstances
 * mesh (buffer-scene.tsx) draws one instance per buffer node row in row order, and
 * getNavNodeIds()[i] is the id of buffer node row i (the ordering invariant above),
 * so instanceId is exactly the row index. Returns null for an out-of-range instanceId
 * (id table not yet populated for that row). Pure — the id table is passed in so this
 * is unit-testable without the module-level table.
 */
export function instanceIdToNodeId(instanceId: number, ids: string[]): string | null {
  return ids[instanceId] ?? null;
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
