// scene-content.tsx — 3D scene orchestrator + RaycasterHelper.
//
// Render is Go-owned: BufferScene (buffer-scene.tsx) draws all geometry from the binary
// content buffer. This file provides the scene lighting/env, the camera-settle detector,
// and the buffer-backed hit-testing (RaycasterHelper) that forwards picks to Go.

import React, { useEffect, useRef } from "react";
import { useThree } from "@react-three/fiber";
import * as THREE from "three";
import type { PickOptions } from "./interaction-controls";
import {
  SHADING_PARAM_SCENE_AMBIENT_INTENSITY,
  SHADING_PARAM_SCENE_DIR_INTENSITY,
} from "../../schema/shading-params";
import { ProceduralEnvProvider } from "./scene-env";
import { CameraSettleDetector } from "./scene-camera";
import { BUFFER_NODE_TAG, BUFFER_PORT_TAG, BUFFER_EDGE_TAG, BUFFER_RING_TAG } from "./buffer-scene";
import { HANDHOLD_TERM_TAG } from "./NavGuides";

// ---------------------------------------------------------------------------
// Buffer-backed pick helpers. Nodes/ports/edges are InstancedMesh / halo meshes
// rendered by buffer-scene.tsx in buffer-row order; the pick resolves the hit to a numeric
// buffer ROW (node / port / edge), forwarded to Go — Go resolves the row back to its entity.
// ---------------------------------------------------------------------------

/**
 * PORT pick: buffer-rendered ports are an InstancedMesh (buffer-scene.tsx PortInstances)
 * tagged with BUFFER_PORT_TAG, where instanceId IS the buffer PORT-ROW index. Returns that
 * row as a decimal STRING so classifyHit can forward the numeric row to Go — which resolves
 * it back to a (node, port).
 */
function pickBufferPort(hits: THREE.Intersection[]): string | null {
  for (const hit of hits) {
    if ((hit.object as THREE.Mesh).userData?.[BUFFER_PORT_TAG] !== true) continue;
    if (hit.instanceId === undefined) continue;
    return String(hit.instanceId);
  }
  return null;
}

/**
 * EDGE pick: buffer-rendered edges each carry a wide pick-halo mesh (buffer-scene.tsx EdgeTube)
 * whose userData[BUFFER_EDGE_TAG] holds its buffer EDGE-ROW index. Returns that row as a decimal
 * STRING so classifyHit can forward the numeric row to Go — which resolves it back to its edge.
 */
function pickBufferEdge(hits: THREE.Intersection[]): string | null {
  for (const hit of hits) {
    const row: unknown = (hit.object as THREE.Mesh).userData?.[BUFFER_EDGE_TAG];
    if (typeof row !== "number") continue;
    return String(row);
  }
  return null;
}

/**
 * HANDHOLD pick: octant θ/φ angle handhold meshes (NavGuides.tsx PolarFrame) carry
 * userData[HANDHOLD_TERM_TAG] with their term-id (+θ=0, +φ=1, -θ=2, -φ=3). Returns the
 * nearest hit's term-id as a decimal STRING so classifyHit can forward it to Go.
 */
function pickBufferHandhold(hits: THREE.Intersection[]): string | null {
  for (const hit of hits) {
    const term: unknown = (hit.object as THREE.Mesh).userData?.[HANDHOLD_TERM_TAG];
    if (typeof term !== "number") continue;
    return String(term);
  }
  return null;
}

/**
 * RING (torus) pick: buffer-rendered node border rings are an InstancedMesh
 * (buffer-scene.tsx NodeInstances ringRef) tagged with BUFFER_RING_TAG, where instanceId IS
 * the buffer NODE-ROW index (rings are drawn in the same per-node loop as the body mesh).
 * Returns that row as a decimal STRING so classifyHit can forward it to Go as a `torus` hit —
 * Go resolves it back to the owning node id.
 */
function pickBufferRing(hits: THREE.Intersection[]): string | null {
  for (const hit of hits) {
    if ((hit.object as THREE.Mesh).userData?.[BUFFER_RING_TAG] !== true) continue;
    if (hit.instanceId === undefined) continue;
    return String(hit.instanceId);
  }
  return null;
}

/**
 * NODE pick: buffer-rendered nodes are an InstancedMesh (buffer-scene.tsx NodeInstances)
 * tagged with BUFFER_NODE_TAG, where instanceId IS the buffer NODE-ROW index. Returns that row
 * as a decimal STRING so classifyHit can forward the numeric row to Go — which resolves it back
 * to its node id. excludeRow (decimal string) skips a specific row (nodesOnly re-pick).
 */
function pickBufferNode(hits: THREE.Intersection[], excludeRow?: string): string | null {
  for (const hit of hits) {
    if ((hit.object as THREE.Mesh).userData?.[BUFFER_NODE_TAG] !== true) continue;
    if (hit.instanceId === undefined) continue;
    const row = String(hit.instanceId);
    if (excludeRow && row === excludeRow) continue;
    return row;
  }
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
      const allHits = raycaster.current.intersectObject(scene, true);
      const hits =
        allHits.length === 0
          ? allHits
          : allHits.filter((h) => (h.object as THREE.Mesh).isMesh);
      if (hits.length === 0) return null;

      // Nodes are the buffer InstancedMesh; ports/edges/rings are buffer meshes carrying
      // their row index. Handholds (NavGuides.tsx octant θ/φ angle grips) carry a term-id;
      // default/nodesOnly resolve the nearest buffer NODE (body mesh); ringOnly resolves the
      // nearest buffer border RING (torus, `port ∈ torus` lock capture).
      if (opts?.handholdOnly) return pickBufferHandhold(hits);
      if (opts?.portOnly) return pickBufferPort(hits);
      if (opts?.edgeOnly) return pickBufferEdge(hits);
      if (opts?.ringOnly) return pickBufferRing(hits);
      return pickBufferNode(hits, opts?.nodesOnly ? opts.excludeId : undefined);
    };
    // No `nodes` dep: the pick callback reads the live scene via useThree(), not a prop,
    // so it must not reinstall every geometry frame.
  }, [camera, scene, onPickRequest]);

  return null;
}

// ---------------------------------------------------------------------------
// Scene
// ---------------------------------------------------------------------------

export function Scene({
  onPickRequest,
  onCameraSettle,
}: {
  onPickRequest: React.MutableRefObject<
    ((ndcX: number, ndcY: number, opts?: PickOptions) => string | null) | null
  >;
  onCameraSettle: () => void;
}) {
  return (
    <ProceduralEnvProvider>
      <RaycasterHelper onPickRequest={onPickRequest} />
      <CameraSettleDetector onSettle={onCameraSettle} />
      <ambientLight intensity={SHADING_PARAM_SCENE_AMBIENT_INTENSITY} />
      <directionalLight position={[0, 0, 10]} intensity={SHADING_PARAM_SCENE_DIR_INTENSITY} />
    </ProceduralEnvProvider>
  );
}
