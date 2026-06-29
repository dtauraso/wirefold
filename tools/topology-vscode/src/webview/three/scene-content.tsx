// scene-content.tsx — 3D scene render components for ThreeView.
// Scene (orchestrator) and RaycasterHelper.

import React, { useEffect, useRef } from "react";
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

// Port hit tolerance (pixels): a port wins over a node-body hit only if its
// ray distance is at most this many units closer than the nearest body hit.
const PORT_HIT_TOL = 8;

// ---------------------------------------------------------------------------
// RaycasterHelper: performs pick on demand via ref callback.
// ---------------------------------------------------------------------------

export function RaycasterHelper({
  nodes,
  onPickRequest,
}: {
  nodes: RFNode<NodeData>[];
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
      const meshes: THREE.Mesh[] = [];
      scene.traverse((obj) => {
        if ((obj as THREE.Mesh).isMesh) meshes.push(obj as THREE.Mesh);
      });
      const hits = raycaster.current.intersectObjects(meshes, false);
      if (hits.length === 0) return null;

      if (opts?.handholdOnly) {
        // A handhold grab only needs detection (which disk to rotate on is decided by
        // the first two drag points, not which handhold). Return a sentinel on the
        // nearest handhold hit so the caller can switch into constrained rotation.
        for (const hit of hits) {
          if ((hit.object as THREE.Mesh).userData?.handhold === true) return "handhold";
        }
        return null;
      }

      if (opts?.ringOnly) {
        for (const hit of hits) {
          const hitObj = hit.object as THREE.Mesh;
          if (hitObj.userData?.ring === true) {
            return (hitObj.userData.nodeId as string) ?? null;
          }
        }
        return null;
      }

      if (opts?.portOnly) {
        // A port only wins the pointer when the click lands on its EXPOSED region,
        // not merely anywhere the ray grazes a port sphere. Port spheres sit on the
        // node's ring (radius ~= node radius), so the ray into a node body almost
        // always also clips a connected port; returning that port unconditionally
        // (the old behavior) hijacked every node-body grab into a port-slide and is
        // the real reason nodes were "not draggable". Resolve with the SAME body-
        // proximity precedence the default pick uses: a port wins only if its hit is
        // at/closer than the nearest node-body hit, within PORT_HIT_TOL. Otherwise
        // the body is the intended target → return null so the caller falls through
        // to the node-drag path. (Reducing lattice spacing only enlarged the relative
        // body target enough to sometimes clear ports — a symptom mask, not the fix.)
        let portHit: { id: string; dist: number } | null = null;
        let nodeHitDist: number | null = null;
        for (const hit of hits) {
          const hitObj = hit.object as THREE.Mesh;
          if (!portHit && hitObj.userData?.portId) {
            portHit = { id: hitObj.userData.portId as string, dist: hit.distance };
            continue;
          }
          if (nodeHitDist === null && hitObj.userData?.body === true) {
            // Nearest node-body hit distance, by the body tag — z-aware and not
            // confusable with overlay meshes (the old x/y parent-proximity match
            // counted e.g. a handhold parented at the origin as a "node" here).
            nodeHitDist = hit.distance;
          }
        }
        if (portHit && (nodeHitDist === null || portHit.dist <= nodeHitDist + PORT_HIT_TOL)) {
          return portHit.id;
        }
        return null;
      }

      if (opts?.nodesOnly) {
        // Iterate all hits to find the first node that isn't excluded.
        // A port sphere hit resolves to its node (via userData.nodeId).
        for (const hit of hits) {
          const hitObj = hit.object as THREE.Mesh;
          if (hitObj.userData?.handhold) continue; // grab affordance, never a node
          if (hitObj.userData?.edgeId) continue; // skip edges
          if (hitObj.userData?.port) {
            // Port sphere — resolve to its owning node.
            const nId = hitObj.userData.nodeId as string;
            if (opts.excludeId && nId === opts.excludeId) continue;
            return nId;
          }
          if (hitObj.userData?.nodeId) {
            // body sphere, ring torus, or any node-attached mesh — resolved by the
            // explicit nodeId tag, not by z-blind/type-blind x/y parent proximity.
            const nId = hitObj.userData.nodeId as string;
            if (opts.excludeId && nId === opts.excludeId) continue;
            return nId;
          }
        }
        return null;
      }

      // Default path: scan ALL hits nearest-first, capture nearest of each category.
      let portHit: { id: string; dist: number } | null = null;
      let edgeHit: { id: string; dist: number } | null = null;
      let nodeHit: { id: string; dist: number } | null = null;

      for (const hit of hits) {
        const hitObj = hit.object as THREE.Mesh;
        // Handholds are a grab affordance handled by the handholdOnly pick path; never
        // a node/port/edge. Skip here so the x/y-proximity fallback below can't
        // misattribute one (parent at origin) to the node at the origin.
        if (hitObj.userData?.handhold) continue;
        if (!portHit && hitObj.userData?.portId) {
          portHit = { id: hitObj.userData.portId as string, dist: hit.distance };
          continue;
        }
        if (!edgeHit && hitObj.userData?.edgeId) {
          edgeHit = { id: hitObj.userData.edgeId as string, dist: hit.distance };
          continue;
        }
        if (!nodeHit && hitObj.userData?.nodeId) {
          // Resolve a node hit to its node id by the explicit userData.nodeId tag —
          // carried by the body sphere AND the ring torus AND port spheres. Z-aware
          // (the NEAREST tagged mesh wins). This replaces the old x,y parent-proximity
          // fallback, which was z-blind and type-blind: it matched ANY untagged mesh
          // whose parent sat near a node's x,y (e.g. a handhold parented at the origin
          // → the origin node), and could pick a node BEHIND another at the same x,y.
          nodeHit = { id: hitObj.userData.nodeId as string, dist: hit.distance };
        }
      }

      // Node-favoring margin: the wide invisible edge selection-halo runs node→node,
      // so over a node the halo surface is at/closer than the node sphere. Without a
      // bias the edge would win and node-drag would silently break. An edge only wins
      // when it is clearly closer than the node by this margin (world units).
      const EDGE_BIAS = 2;

      // Precedence: port wins over node if within tolerance (covers embedded half of port sphere).
      let picked: string | null = null;
      if (portHit && (!nodeHit || portHit.dist <= nodeHit.dist + PORT_HIT_TOL)) {
        picked = portHit.id;
      } else if (edgeHit && nodeHit) {
        picked = edgeHit.dist < nodeHit.dist - EDGE_BIAS ? edgeHit.id : nodeHit.id;
      } else if (edgeHit) {
        picked = edgeHit.id;
      } else if (nodeHit) {
        picked = nodeHit.id;
      }

      return picked;
    };
  }, [camera, scene, nodes]);

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
  const nodeMap = new Map(nodes.map((n) => [n.id, n]));
  // Sphere owners depend on the click kind (sphereMode):
  //   "surface" (single click): the spheres the node sits ON the surface of — every
  //     sphere whose center outputs to it (its incoming-edge sources; one node can be
  //     on several, e.g. node 5 on both 4 and 6).
  //   "own" (two-finger click): just the node's OWN sphere (where it is the center).
  // For each owner we draw its sphere and highlight the owner (center) plus all nodes
  // on its surface (the owner's children). Owners that center no sphere (no outgoing
  // edge) draw nothing but still contribute their own highlight.
  const sphereOwners = !selectedId
    ? []
    : sphereMode === "own"
      ? [selectedId]
      : Array.from(
          new Set<string>(
            edges
              .filter((e) => e.target === selectedId && e.source)
              .map((e) => e.source as string),
          ),
        );
  const surfaceIds = new Set<string>();
  for (const ownerId of sphereOwners) {
    surfaceIds.add(ownerId);
    for (const e of edges) {
      if (e.source === ownerId && e.target) surfaceIds.add(e.target);
    }
  }
  // cameraPolar takes precedence; if present, skip camera3d restore and suppress auto-fit.
  const hasRestoredCamera = initialCameraPolar !== undefined || initialCamera3d !== undefined;
  return (
    <ProceduralEnvProvider>
      <CameraFitter nodes={nodes} hasRestoredCamera={hasRestoredCamera} />
      <CameraRefBridge cameraRef={cameraRef} initialCamera3d={initialCameraPolar === undefined ? initialCamera3d : undefined} />
      {initialCameraPolar !== undefined && <PolarCameraRestorer initialCameraPolar={initialCameraPolar} />}
      <CameraFromStore />
      <RaycasterHelper nodes={nodes} onPickRequest={onPickRequest} />
      <LabelProjector nodes={nodes} onPositions={onPositions} />
      <CameraSettleDetector onSettle={onCameraSettle} />
      <ambientLight intensity={SHADING_PARAM_SCENE_AMBIENT_INTENSITY} />
      <directionalLight position={[0, 0, 10]} intensity={SHADING_PARAM_SCENE_DIR_INTENSITY} />
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
    </ProceduralEnvProvider>
  );
}
