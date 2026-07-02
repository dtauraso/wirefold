// scene-content.tsx — 3D scene render components for ThreeView.
// Scene (orchestrator) and RaycasterHelper.

import React, { useEffect, useMemo, useRef } from "react";
import { useThree } from "@react-three/fiber";
import * as THREE from "three";
import type { RFNode, RFEdge, NodeData, EdgeData } from "../types";
import type { Camera3D } from "../state/viewer/types";
import type { PolarCamera } from "./camera-store";
import type { PickOptions } from "./interaction-controls";
import {
  SHADING_PARAM_SCENE_AMBIENT_INTENSITY,
  SHADING_PARAM_SCENE_DIR_INTENSITY,
} from "../../schema/shading-params";
import { ProceduralEnvProvider } from "./scene-env";
import { CameraFitter, CameraRefBridge, LabelProjector, CameraSettleDetector, PolarCameraRestorer } from "./scene-camera";
import { CameraFromStore } from "./CameraFromStore";
import { GraphNode, GraphEdges, SphereRing } from "./scene-graph";
import { MissedBeadMarkers } from "./scene-beads";
import { USE_NEW_SYSTEM } from "../new-system";

// Port hit tolerance (pixels): a port wins over a node-body hit only if its
// ray distance is at most this many units closer than the nearest body hit.
const PORT_HIT_TOL = 8;

/**
 * Scan an already-sorted (nearest-first) intersections list and return the
 * nearest hit for each requested userData key.  Each intersection is matched
 * against at most one key (first-wins per hit, mirroring the original
 * `continue` after portHit / edgeHit so that a port mesh — which carries
 * both `portId` and `nodeId` — is never double-counted as both a port and a
 * node hit).  Hits whose `skip` predicate returns true are skipped entirely.
 */
function findNearestByTag(
  hits: THREE.Intersection[],
  keys: string[],
  skip?: (obj: THREE.Mesh) => boolean
): ({ id: string; dist: number } | null)[] {
  const results: ({ id: string; dist: number } | null)[] = keys.map(() => null);
  for (const hit of hits) {
    const obj = hit.object as THREE.Mesh;
    if (skip?.(obj)) continue;
    for (let i = 0; i < keys.length; i++) {
      if (results[i] !== null) continue;
      // THREE.Object3D.userData is typed Record<string, any>; capture as unknown
      // and narrow rather than letting `any` propagate.
      const val: unknown = obj.userData?.[keys[i]!]; // i < keys.length
      if (val) {
        results[i] = { id: val as string, dist: hit.distance };
        break; // one category per hit — preserves original `continue` semantics
      }
    }
  }
  return results;
}

// ---------------------------------------------------------------------------
// RaycasterHelper: per-mode pick helpers + main pick function.
// ---------------------------------------------------------------------------

/** handholdOnly: return "handhold" sentinel on the nearest handhold hit. */
function pickHandhold(hits: THREE.Intersection[]): string | null {
  // Detection only — which disk to rotate on is decided by the first two drag
  // points, not which handhold was grabbed.
  for (const hit of hits) {
    if ((hit.object as THREE.Mesh).userData?.handhold === true) return "handhold";
  }
  return null;
}

/** ringOnly: return the nodeId of the nearest ring mesh hit. */
function pickRing(hits: THREE.Intersection[]): string | null {
  for (const hit of hits) {
    const obj = hit.object as THREE.Mesh;
    if (obj.userData?.ring === true) return (obj.userData.nodeId as string) ?? null;
  }
  return null;
}

/**
 * portOnly: a port wins only if its hit is at/closer than the nearest node-body
 * hit within PORT_HIT_TOL. Otherwise return null so the caller falls through to
 * the node-drag path.
 *
 * Uses findNearestByTag for portHit (one-category-per-hit semantics). nodeHitDist
 * is found in a separate pass because no mesh carries both `portId` and `body:true`.
 */
function pickPort(hits: THREE.Intersection[]): string | null {
  const [portHit] = findNearestByTag(hits, ["portId"]);
  let nodeHitDist: number | null = null;
  for (const hit of hits) {
    if ((hit.object as THREE.Mesh).userData?.body === true) {
      // Nearest node-body hit distance, by the body tag — z-aware and not
      // confusable with overlay meshes.
      nodeHitDist = hit.distance;
      break;
    }
  }
  if (portHit && (nodeHitDist === null || portHit.dist <= nodeHitDist + PORT_HIT_TOL)) {
    return portHit.id;
  }
  return null;
}

/**
 * nodesOnly: iterate all hits nearest-first, resolve port-sphere hits to their
 * owning node, skip handholds and edges. Respects opts.excludeId.
 */
function pickNodesOnly(hits: THREE.Intersection[], excludeId?: string): string | null {
  for (const hit of hits) {
    const obj = hit.object as THREE.Mesh;
    if (obj.userData?.handhold) continue;
    if (obj.userData?.edgeId) continue;
    if (obj.userData?.port) {
      const nId = obj.userData.nodeId as string;
      if (excludeId && nId === excludeId) continue;
      return nId;
    }
    if (obj.userData?.nodeId) {
      const nId = obj.userData.nodeId as string;
      if (excludeId && nId === excludeId) continue;
      return nId;
    }
  }
  return null;
}

// Node-favoring margin: the wide invisible edge selection-halo runs node→node,
// so over a node the halo surface is at/closer than the node sphere. Without a
// bias the edge would win and node-drag would silently break. An edge only wins
// when it is clearly closer than the node by this margin (world units).
const EDGE_BIAS = 2;

/**
 * Default pick: port → edge → node priority (nearest-first, one-category-per-hit).
 * Handholds are skipped (grab affordance handled by the handholdOnly path).
 */
function pickDefault(hits: THREE.Intersection[]): string | null {
  const [portHit, edgeHit, nodeHit] = findNearestByTag(
    hits,
    ["portId", "edgeId", "nodeId"],
    (obj) => !!obj.userData?.handhold,
  );
  if (portHit && (!nodeHit || portHit.dist <= nodeHit.dist + PORT_HIT_TOL)) return portHit.id;
  if (edgeHit && nodeHit) return edgeHit.dist < nodeHit.dist - EDGE_BIAS ? edgeHit.id : nodeHit.id;
  if (edgeHit) return edgeHit.id;
  if (nodeHit) return nodeHit.id;
  return null;
}

// ---------------------------------------------------------------------------
// RaycasterHelper: performs pick on demand via ref callback.
// ---------------------------------------------------------------------------

function RaycasterHelper({
  onPickRequest,
}: {
  onPickRequest: React.MutableRefObject<
    ((ndcX: number, ndcY: number, opts?: PickOptions) => string | null) | null
  >;
}) {
  const { camera, scene } = useThree();
  const raycaster = useRef(new THREE.Raycaster());

  useEffect(() => {
    onPickRequest.current = (ndcX: number, ndcY: number, opts?: PickOptions): string | null => {
      const ndc = new THREE.Vector2(ndcX, ndcY);
      raycaster.current.setFromCamera(ndc, camera);
      // Recursive intersect against the live scene, then keep only mesh hits.
      // Equivalent to the old "traverse → collect every isMesh → intersect"
      // (three sorts nearest-first and skips objects whose raycast yields
      // nothing), but without allocating a fresh meshes[] buffer and traverse
      // closure on every pointer-move pick. Non-mesh hits (lines/sprites) are
      // filtered out to preserve the mesh-only candidate semantics exactly.
      const allHits = raycaster.current.intersectObject(scene, true);
      const hits =
        allHits.length === 0
          ? allHits
          : allHits.filter((h) => (h.object as THREE.Mesh).isMesh);
      if (hits.length === 0) return null;

      if (opts?.handholdOnly) return pickHandhold(hits);
      if (opts?.ringOnly) return pickRing(hits);
      if (opts?.portOnly) return pickPort(hits);
      if (opts?.nodesOnly) return pickNodesOnly(hits, opts.excludeId);
      return pickDefault(hits);
    };
    // No `nodes` dep: the pick callback reads the live scene via useThree()/traverse,
    // not the `nodes` prop, so it must not reinstall every geometry frame.
  }, [camera, scene, onPickRequest]);

  return null;
}

// ---------------------------------------------------------------------------
// Scene
// ---------------------------------------------------------------------------

export function Scene({
  nodes,
  edges,
  selectedId,
  sphereMode,
  hoveredId,
  cameraRef,
  initialCamera3d,
  initialCameraPolar,
  onPickRequest,
  onPositions,
  onCameraSettle,
}: {
  nodes: RFNode<NodeData>[];
  edges: RFEdge<EdgeData>[];
  selectedId: string | null;
  sphereMode: "surface" | "own";
  hoveredId: string | null;
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>;
  initialCamera3d?: Camera3D;
  initialCameraPolar?: PolarCamera;
  onPickRequest: React.MutableRefObject<
    ((ndcX: number, ndcY: number, opts?: PickOptions) => string | null) | null
  >;
  onPositions: (positions: { id: string; px: number; py: number; cx: number; cy: number }[]) => void;
  onCameraSettle: () => void;
}) {
  const nodeMap = useMemo(() => new Map(nodes.map((n) => [n.id, n])), [nodes]);
  // Sphere owners depend on the click kind (sphereMode):
  //   "surface" (single click): the spheres the node sits ON the surface of — every
  //     sphere whose center outputs to it (its incoming-edge sources; one node can be
  //     on several, e.g. node 5 on both 4 and 6).
  //   "own" (two-finger click): just the node's OWN sphere (where it is the center).
  // For each owner we draw its sphere and highlight the owner (center) plus all nodes
  // on its surface (the owner's children). Owners that center no sphere (no outgoing
  // edge) draw nothing but still contribute their own highlight.
  // Memoized on [selectedId, sphereMode, edges]: recompute the owner list + surface
  // set only when the selection or topology changes, not on every scene render.
  const { sphereOwners, surfaceIds } = useMemo(() => {
    const owners = !selectedId
      ? []
      : sphereMode === "own"
        ? [selectedId]
        : Array.from(
            new Set<string>(
              edges
                .filter((e) => e.target === selectedId && e.source)
                .map((e) => e.source),
            ),
          );
    const ids = new Set<string>();
    for (const ownerId of owners) {
      ids.add(ownerId);
      for (const e of edges) {
        if (e.source === ownerId && e.target) ids.add(e.target);
      }
    }
    return { sphereOwners: owners, surfaceIds: ids };
  }, [selectedId, sphereMode, edges]);
  // cameraPolar takes precedence; if present, skip camera3d restore and suppress auto-fit.
  const hasRestoredCamera = initialCameraPolar !== undefined || initialCamera3d !== undefined;
  return (
    <ProceduralEnvProvider>
      <CameraFitter nodes={nodes} hasRestoredCamera={hasRestoredCamera} />
      <CameraRefBridge cameraRef={cameraRef} initialCamera3d={initialCameraPolar === undefined ? initialCamera3d : undefined} />
      {initialCameraPolar !== undefined && <PolarCameraRestorer initialCameraPolar={initialCameraPolar} />}
      <CameraFromStore />
      <RaycasterHelper onPickRequest={onPickRequest} />
      <LabelProjector nodes={nodes} onPositions={onPositions} />
      <CameraSettleDetector onSettle={onCameraSettle} />
      <ambientLight intensity={SHADING_PARAM_SCENE_AMBIENT_INTENSITY} />
      <directionalLight position={[0, 0, 10]} intensity={SHADING_PARAM_SCENE_DIR_INTENSITY} />
      {/* Geometry (nodes + interior beads/pulses, edge tubes + edge beads/pulses,
          selection sphere-rings, and missed-bead markers) is gated OFF under the
          new-system flag: with USE_NEW_SYSTEM on, BufferScene renders all geometry
          from the binary buffer, so rendering it again here would double it (and
          double the selection highlight). Camera, overlays, picking, labels, and
          lighting above stay unconditional so the old path keeps hosting them. */}
      {!USE_NEW_SYSTEM && (
        <>
          {nodes.map((n) => (
            <GraphNode
              key={n.id}
              node={n}
              selected={n.id === selectedId}
              hovered={n.id === hoveredId}
              faded={!!n.data?.faded}
              selectedId={selectedId}
              hoveredId={hoveredId}
              onSphereSurface={surfaceIds.has(n.id)}
            />
          ))}
          {/* Interior beads are now mounted INSIDE each GraphNode group (at Go-given
              node-local offsets) so they ride the node on move — no top-level mount. */}
          <GraphEdges edges={edges} nodeMap={nodeMap} selectedId={selectedId} />
          {sphereOwners.map((oid) => (
            <SphereRing key={oid} nodes={nodes} edges={edges} ownerId={oid} />
          ))}
          {/* Missed-bead markers: rendered at Go-supplied WORLD positions just outside a
              node while Go reports a firing error (node-status torusRed). Scene-level
              (not a node child) since the position is world-space. */}
          <MissedBeadMarkers />
        </>
      )}
    </ProceduralEnvProvider>
  );
}
