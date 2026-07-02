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
import { getNavNodeIds, instanceIdToNodeId } from "./buffer-nav";
import { BUFFER_NODE_TAG, BUFFER_PORT_TAG, BUFFER_EDGE_TAG } from "./buffer-scene";

// ---------------------------------------------------------------------------
// Buffer-backed pick helpers. Nodes/ports/edges are InstancedMesh / halo meshes
// rendered by buffer-scene.tsx in buffer-row order; the pick resolves the hit back
// to a node id (via the nav id table) or a numeric buffer row (forwarded to Go).
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

function pickBufferNode(hits: THREE.Intersection[], excludeId?: string): string | null {
  const ids = getNavNodeIds();
  for (const hit of hits) {
    if ((hit.object as THREE.Mesh).userData?.[BUFFER_NODE_TAG] !== true) continue;
    if (hit.instanceId === undefined) continue;
    const id = instanceIdToNodeId(hit.instanceId, ids);
    if (id === null) continue;
    if (excludeId && id === excludeId) continue;
    return id;
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

      // Nodes are the buffer InstancedMesh; ports/edges are buffer meshes carrying their
      // row index. Handholds are not pickable meshes in the buffer, so that mode returns null;
      // node-oriented modes (default / nodesOnly / ringOnly) resolve the nearest buffer node.
      if (opts?.handholdOnly) return null;
      if (opts?.portOnly) return pickBufferPort(hits);
      if (opts?.edgeOnly) return pickBufferEdge(hits);
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
