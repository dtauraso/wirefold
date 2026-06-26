// NavGuides.tsx — decorative 3D navigation overlays:
//   PolarSphere: two perpendicular tori centered on mid-depth focus point,
//     radius scaled to ARCBALL_FILL * camera-to-focus distance, updated each frame.
// Purely decorative: raycast disabled, depthWrite false, transparent.

import React, { useMemo } from "react";
import * as THREE from "three";
import type { RFNode, NodeData } from "../types";
import { nodeRadius, nodeWorldPos } from "./geometry-helpers";
import { useNodeGeometryStore } from "./node-geometry";
import { computeContentSphere } from "./interaction-controls";
import { useCameraStore } from "./camera-store";

// ---------------------------------------------------------------------------
// PolarSphere — two perpendicular tori tracking the polar rotation-sphere center.
// Major radius = ARCBALL_FILL * camera-to-focus distance; updated every frame.
// ---------------------------------------------------------------------------

// AxisLabel — canvas-texture Sprite billboard; always faces the camera, no font asset needed.
function AxisLabel({ text, color, position, size }: {
  text: string; color: string; position: [number, number, number]; size: number;
}) {
  const texture = useMemo(() => {
    const c = document.createElement("canvas");
    c.width = 256; c.height = 64;
    const ctx = c.getContext("2d")!;
    ctx.font = "bold 44px sans-serif";
    ctx.textAlign = "center"; ctx.textBaseline = "middle";
    ctx.fillStyle = color;
    ctx.fillText(text, 128, 32);
    const t = new THREE.CanvasTexture(c);
    t.needsUpdate = true;
    return t;
  }, [text, color]);
  return (
    <sprite position={position} scale={[size * 4, size, 1]} raycast={() => null}>
      <spriteMaterial map={texture} transparent depthWrite={false} depthTest={false} />
    </sprite>
  );
}

// PolarFrame — the camera-independent pole-frame markers for ONE center: the three
// axis sticks (+y pole green, +x φ0 red, +z φ90 blue) plus the θ (magenta) and φ
// (yellow) angle arcs, all anchored at `center` with the pole = world +y. `scale`
// sizes the frame (≈ the radius it should reach). `tag` suffixes the axis labels so
// the scene frame and a node's frame are distinguishable. Decorative (raycast off),
// not affected by the scene-tori toggle. Same drawing for every center, so node 2's
// frame matches the scene's exactly.
function PolarFrame({ center, scale, tag }: {
  center: THREE.Vector3; scale: number; tag?: string;
}) {
  const radiusKey = Math.max(Math.round(scale), 1);
  const poleLen = radiusKey * 1.3;
  const poleRadius = Math.max(radiusKey * 0.01, 1);
  const coneH = radiusKey * 0.12;
  const coneBaseR = radiusKey * 0.05;
  const arcR = poleLen * 0.68;
  const arcTube = Math.max(radiusKey * 0.012, 1.2);
  const arcMid = arcR * 1.12 * Math.SQRT1_2;
  const sfx = tag ? ` ${tag}` : "";
  return (
    <group position={[center.x, center.y, center.z]}>
      {/* +Y pole (green). */}
      <mesh position={[0, poleLen / 2, 0]} raycast={() => null}>
        <cylinderGeometry args={[poleRadius, poleRadius, poleLen, 12]} />
        <meshBasicMaterial color="#22dd55" depthWrite={false} />
      </mesh>
      <mesh position={[0, poleLen + coneH / 2, 0]} raycast={() => null}>
        <coneGeometry args={[coneBaseR, coneH, 12]} />
        <meshBasicMaterial color="#22dd55" depthWrite={false} />
      </mesh>
      {/* +X equatorial reference (φ=0, red). */}
      <mesh position={[poleLen / 2, 0, 0]} rotation={[0, 0, -Math.PI / 2]} raycast={() => null}>
        <cylinderGeometry args={[poleRadius, poleRadius, poleLen, 12]} />
        <meshBasicMaterial color="#dd3333" depthWrite={false} />
      </mesh>
      <mesh position={[poleLen + coneH / 2, 0, 0]} rotation={[0, 0, -Math.PI / 2]} raycast={() => null}>
        <coneGeometry args={[coneBaseR, coneH, 12]} />
        <meshBasicMaterial color="#dd3333" depthWrite={false} />
      </mesh>
      {/* +Z equatorial reference (φ=90°, blue). */}
      <mesh position={[0, 0, poleLen / 2]} rotation={[Math.PI / 2, 0, 0]} raycast={() => null}>
        <cylinderGeometry args={[poleRadius, poleRadius, poleLen, 12]} />
        <meshBasicMaterial color="#3366dd" depthWrite={false} />
      </mesh>
      <mesh position={[0, 0, poleLen + coneH / 2]} rotation={[Math.PI / 2, 0, 0]} raycast={() => null}>
        <coneGeometry args={[coneBaseR, coneH, 12]} />
        <meshBasicMaterial color="#3366dd" depthWrite={false} />
      </mesh>
      {/* θ angle arc (magenta): quarter-sweep from +Y pole to +X, X-Y meridian plane. */}
      <mesh raycast={() => null}>
        <torusGeometry args={[arcR, arcTube, 8, 48, Math.PI / 2]} />
        <meshBasicMaterial color="#dd33cc" depthWrite={false} />
      </mesh>
      {/* φ angle arc (yellow): quarter-sweep in equatorial X-Z plane, +X (φ0)→+Z (φ90). */}
      <mesh rotation={[Math.PI / 2, 0, 0]} raycast={() => null}>
        <torusGeometry args={[arcR, arcTube, 8, 48, Math.PI / 2]} />
        <meshBasicMaterial color="#dddd22" depthWrite={false} />
      </mesh>
      {/* Labels — billboard sprites, always face the camera. */}
      <AxisLabel text={`+Y pole${sfx}`} color="#22dd55" position={[0, poleLen + coneH * 2, 0]} size={poleLen * 0.12} />
      <AxisLabel text={`+X φ0${sfx}`} color="#dd3333" position={[poleLen + coneH * 2, 0, 0]} size={poleLen * 0.12} />
      <AxisLabel text={`+Z φ90${sfx}`} color="#3366dd" position={[0, 0, poleLen + coneH * 2]} size={poleLen * 0.12} />
      <AxisLabel text="θ" color="#dd33cc" position={[arcMid, arcMid, 0]} size={poleLen * 0.14} />
      <AxisLabel text="φ" color="#dddd22" position={[arcMid, 0, arcMid]} size={poleLen * 0.14} />
    </group>
  );
}

// ThetaArc — a VERTICAL meridian arc from `center`'s +y pole down to `sample`, in the
// node's own meridian plane. Its angular sweep IS the node's θ (colatitude); the arc
// tip touches the node. θ traces vertically (pole→down), so equal-θ shows as two arcs
// of equal sweep (just rotated apart in φ); different θ shows as different sweep length.
// Built canonically in the local X-Y plane, then: inner Z-rotation (π/2−θ) seats the
// arc's far end at +Y, outer Y-rotation (−φ) swings the meridian plane to the node's φ.
function ThetaArc({ center, sample, color, tube }: {
  center: THREE.Vector3; sample: THREE.Vector3; color: string; tube: number;
}) {
  const dx = sample.x - center.x;
  const dy = sample.y - center.y;
  const dz = sample.z - center.z;
  const r = Math.sqrt(dx * dx + dy * dy + dz * dz);
  if (r < 1e-6) return null;
  const theta = Math.acos(Math.max(-1, Math.min(1, dy / r)));
  const phi = Math.atan2(dz, dx);
  return (
    <group position={[center.x, center.y, center.z]} rotation={[0, -phi, 0]}>
      <group rotation={[0, 0, Math.PI / 2 - theta]}>
        <mesh raycast={() => null}>
          <torusGeometry args={[r, tube, 8, 64, theta]} />
          <meshBasicMaterial color={color} transparent opacity={0.85} depthWrite={false} />
        </mesh>
        {/* Live θ value at the arc's midpoint (param θ/2 in this local frame). */}
        <AxisLabel
          text={`θ=${((theta * 180) / Math.PI).toFixed(1)}°`}
          color={color}
          position={[Math.cos(theta / 2) * r * 1.16, Math.sin(theta / 2) * r * 1.16, 0]}
          size={r * 0.13}
        />
      </group>
    </group>
  );
}

// PhiArc — a HORIZONTAL arc around `center`'s +y pole from the +x reference (φ=0) to
// `sample`'s azimuth, at the node's own height. Its sweep IS the node's φ (longitude);
// the arc tip sits under/at the node. φ traces horizontally (around the pole). Built
// canonically in the local X-Y plane sweeping +X→+Y over |φ|, then rotated about X by
// ±90° so it lies flat in world X-Z and sweeps toward +z (φ>0) or −z (φ<0).
function PhiArc({ center, sample, color, tube }: {
  center: THREE.Vector3; sample: THREE.Vector3; color: string; tube: number;
}) {
  const dx = sample.x - center.x;
  const dz = sample.z - center.z;
  const ringR = Math.sqrt(dx * dx + dz * dz); // horizontal distance from the pole axis
  if (ringR < 1e-6) return null;
  const phi = Math.atan2(dz, dx);
  const span = Math.abs(phi);
  const xRot = phi >= 0 ? Math.PI / 2 : -Math.PI / 2; // sweep toward +z or −z
  return (
    <group position={[center.x, sample.y, center.z]} rotation={[xRot, 0, 0]}>
      <mesh raycast={() => null}>
        <torusGeometry args={[ringR, tube, 8, 64, span]} />
        <meshBasicMaterial color={color} transparent opacity={0.85} depthWrite={false} />
      </mesh>
      {/* Live φ value at the arc's midpoint (param span/2 in this local frame). */}
      <AxisLabel
        text={`φ=${((phi * 180) / Math.PI).toFixed(1)}°`}
        color={color}
        position={[Math.cos(span / 2) * ringR * 1.16, Math.sin(span / 2) * ringR * 1.16, 0]}
        size={ringR * 0.13}
      />
    </group>
  );
}

function PolarSphere({ nodes }: { nodes: RFNode<NodeData>[] }) {
  // Re-derive when Go streams node geometry (positions change → content sphere moves).
  useNodeGeometryStore((s) => s.geoms);
  const sceneToriVisible = useCameraStore((s) => s.sceneToriVisible);
  const scenePolesVisible = useCameraStore((s) => s.scenePolesVisible);
  const nodePolesVisible = useCameraStore((s) => s.nodePolesVisible);

  // WORLD-FIXED content sphere (= the arcball, matching interaction-controls), so it
  // zooms WITH the diagram. Tube thickness matches the node spheres' tori
  // (scene-content SphereRing: max(0.5, nodeRadius·0.08)).
  const cs = computeContentSphere(nodes);
  const tube = nodes.length > 0 ? Math.max(0.5, nodeRadius(nodes[0]) * 0.08) : 1;
  // Build geometry ONLY when the sphere actually changes (rounded radius/tube), not on
  // every render — rebuilding each frame under node-geometry churn made the tori flicker
  // and effectively disappear.
  const radiusKey = Math.round(cs.radius);
  const tubeKey = Math.round(tube * 10);
  const { geoA, geoB } = useMemo(
    () => ({
      geoA: new THREE.TorusGeometry(radiusKey, tubeKey / 10, 12, 96),
      geoB: new THREE.TorusGeometry(radiusKey, tubeKey / 10, 12, 96),
    }),
    [radiusKey, tubeKey],
  );
  const rotB = useMemo(() => new THREE.Euler(Math.PI / 2, 0, 0), []);

  // Handholds: 4 grab points per torus, 90° apart. Grabbing one starts a CONSTRAINED
  // rotation — the first two cursor points lock the rotation disk (see
  // interaction-handlers.ts "handhold-rotating"). These are the only PICKABLE part of
  // the nav overlay (the tori stay raycast-disabled); each carries userData.handhold.
  // Placed in the torus's own local frame: geoA lies in XY (handholds at z=0), geoB is
  // the same ring under rotB, so its handhold group shares that rotation.
  const hhAngles = [0, Math.PI / 2, Math.PI, (3 * Math.PI) / 2];
  const hhRadius = Math.max(radiusKey * 0.04, 3); // grabbable, scales with the sphere
  const handholds = (rotation?: THREE.Euler) => (
    <group rotation={rotation}>
      {hhAngles.map((a, i) => (
        <mesh key={i} position={[radiusKey * Math.cos(a), radiusKey * Math.sin(a), 0]} userData={{ handhold: true }}>
          <sphereGeometry args={[hhRadius, 16, 16]} />
          <meshStandardMaterial color="#cc8844" emissive="#cc8844" emissiveIntensity={0.6} transparent opacity={0.9} />
        </mesh>
      ))}
    </group>
  );

  if (nodes.length < 1) return null;

  // Node 2's own pole frame — same drawing as the scene frame, anchored at node 2's
  // world center (pole = world +y, parallel to the scene's). This is the frame the
  // θ-lock actually measures in: 3 and 7 share θ ABOUT NODE 2, so equal-θ is only
  // visible against node 2's pole, not the scene's. Sized to ~half the scene radius.
  const node2 = nodes.find((n) => n.id === "2");
  const node2Center = node2 ? nodeWorldPos(node2) : null;
  const node2Scale = radiusKey * 0.5;

  // θ-lock check: vertical θ meridian arcs from node 2's pole down to nodes 3 and 7,
  // from LIVE positions. θ traces vertically; equal θ ⇒ equal arc sweep (rotated apart
  // in φ), different θ ⇒ different sweep length. (See ThetaArc.)
  const node3 = nodes.find((n) => n.id === "3");
  const node7 = nodes.find((n) => n.id === "7");
  const thetaTube = Math.max(node2Scale * 0.014, 1.4);

  // WORLD-FIXED tori: the pole is the diagram's own top axis (world Y), so the horizontal torus
  // (geoB, normal world Y) is the diagram's equator — the polar frame is anchored to the
  // diagram, not the camera.
  const pos: [number, number, number] = [cs.center.x, cs.center.y, cs.center.z];
  return (
    <>
      <group position={pos}>
        {sceneToriVisible !== false && (
          <>
            <mesh geometry={geoA} raycast={() => null}>
              <meshBasicMaterial color="#cc8844" transparent opacity={0.4} depthWrite={false} />
            </mesh>
            <mesh geometry={geoB} rotation={rotB} raycast={() => null}>
              <meshBasicMaterial color="#cc8844" transparent opacity={0.4} depthWrite={false} />
            </mesh>
          </>
        )}
        {/* Grab handholds (4 per torus, 90° apart) — the pickable part of the overlay. */}
        {handholds()}
        {handholds(rotB)}
      </group>
      {/* Scene pole frame at the content-sphere center. */}
      {scenePolesVisible !== false && <PolarFrame center={cs.center} scale={radiusKey} />}
      {/* Per-node pole frames — one PolarFrame per node, gated behind nodePolesVisible. */}
      {nodePolesVisible !== false && nodes.map((node) => (
        <PolarFrame
          key={node.id}
          center={nodeWorldPos(node)}
          scale={nodeRadius(node)}
          tag={`(${node.id})`}
        />
      ))}
      {/* Vertical θ arcs from node 2's pole to node 3 (orange) and node 7 (cyan): equal sweep ⇒ equal θ. */}
      {node2Center && node3 && (
        <ThetaArc center={node2Center} sample={nodeWorldPos(node3)} color="#ff8800" tube={thetaTube} />
      )}
      {node2Center && node7 && (
        <ThetaArc center={node2Center} sample={nodeWorldPos(node7)} color="#00ccff" tube={thetaTube} />
      )}
      {/* Horizontal φ arcs from +x reference to node 3 (orange) and node 7 (cyan). */}
      {node2Center && node3 && (
        <PhiArc center={node2Center} sample={nodeWorldPos(node3)} color="#ff8800" tube={thetaTube} />
      )}
      {node2Center && node7 && (
        <PhiArc center={node2Center} sample={nodeWorldPos(node7)} color="#00ccff" tube={thetaTube} />
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// NavGuides — combined export
// ---------------------------------------------------------------------------

export function NavGuides({ nodes }: { nodes: RFNode<NodeData>[]; selectedId?: string | null }) {
  return (
    <>
      <PolarSphere nodes={nodes} />
    </>
  );
}
