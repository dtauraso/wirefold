// NavGuides.tsx — decorative 3D navigation overlays:
//   PolarSphere: two perpendicular tori centered on mid-depth focus point,
//     radius scaled to ARCBALL_FILL * camera-to-focus distance, updated each frame.
// Purely decorative: raycast disabled, depthWrite false, transparent.

import React, { useMemo, useState, useEffect } from "react";
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

// The 8 octants of the polar sphere — a sign triple (±x,±y,±z), a distinct color, and a
// compact label. When octants={true} the θ/φ angle arcs are reflected (group scale) into
// each octant and colored from here, so every octant gets its own angle-arc pair.
const OCTANTS: { s: [number, number, number]; color: string; tag: string }[] = [
  { s: [1, 1, 1], color: "#ffffff", tag: "+x+y+z" },
  { s: [1, 1, -1], color: "#ff8c00", tag: "+x+y−z" },
  { s: [1, -1, 1], color: "#00ced1", tag: "+x−y+z" },
  { s: [1, -1, -1], color: "#9370db", tag: "+x−y−z" },
  { s: [-1, 1, 1], color: "#ff69b4", tag: "−x+y+z" },
  { s: [-1, 1, -1], color: "#9acd32", tag: "−x+y−z" },
  { s: [-1, -1, 1], color: "#00bfff", tag: "−x−y+z" },
  { s: [-1, -1, -1], color: "#cd853f", tag: "−x−y−z" },
];

// PolarFrame — the camera-independent pole-frame markers for ONE center: the three
// axis sticks (+y pole green, +x φ0 red, +z φ90 blue) plus the θ (magenta) and φ
// (yellow) angle arcs, all anchored at `center` with the pole = world +y. `scale`
// sizes the frame (≈ the radius it should reach). `tag` suffixes the axis labels so
// the scene frame and a node's frame are distinguishable. Decorative (raycast off),
// not affected by the scene-tori toggle. Same drawing for every center, so node 2's
// frame matches the scene's exactly.
function PolarFrame({ center, scale, tag, octants }: {
  center: THREE.Vector3; scale: number; tag?: string; octants?: boolean;
}) {
  const radiusKey = Math.max(Math.round(scale), 1);
  const poleLen = radiusKey * 1.3;
  const poleRadius = Math.max(radiusKey * 0.01, 1);
  const coneH = radiusKey * 0.12;
  const coneBaseR = radiusKey * 0.05;
  const arcR = poleLen * 0.68;
  const arcTube = Math.max(radiusKey * 0.012, 1.2);
  const arcMid = arcR * 1.12 * Math.SQRT1_2;
  const hhR = Math.max(radiusKey * 0.04, 3);   // handhold sphere radius (matches the tori handholds)
  const arcHH = arcR * Math.SQRT1_2;           // a quarter-arc's midpoint radius (45° in its plane)
  const arcOff = arcR * 0.05;                  // nudge each octant's arcs off the shared plane so
                                               // they don't coincide (coincident transparent arcs
                                               // sort by camera and flip colors on rotate/pan/zoom)
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
      {/* Negative spokes (octant mode): the other halves of each axis (−Y/−X/−Z), so the
          full ±X ±Y ±Z cross frames all 8 octants. Same colors as the positive halves. */}
      {octants && (<>
        <mesh position={[0, -poleLen / 2, 0]} raycast={() => null}>
          <cylinderGeometry args={[poleRadius, poleRadius, poleLen, 12]} />
          <meshBasicMaterial color="#22dd55" depthWrite={false} />
        </mesh>
        <mesh position={[0, -(poleLen + coneH / 2), 0]} rotation={[Math.PI, 0, 0]} raycast={() => null}>
          <coneGeometry args={[coneBaseR, coneH, 12]} />
          <meshBasicMaterial color="#22dd55" depthWrite={false} />
        </mesh>
        <mesh position={[-poleLen / 2, 0, 0]} rotation={[0, 0, Math.PI / 2]} raycast={() => null}>
          <cylinderGeometry args={[poleRadius, poleRadius, poleLen, 12]} />
          <meshBasicMaterial color="#dd3333" depthWrite={false} />
        </mesh>
        <mesh position={[-(poleLen + coneH / 2), 0, 0]} rotation={[0, 0, Math.PI / 2]} raycast={() => null}>
          <coneGeometry args={[coneBaseR, coneH, 12]} />
          <meshBasicMaterial color="#dd3333" depthWrite={false} />
        </mesh>
        <mesh position={[0, 0, -poleLen / 2]} rotation={[Math.PI / 2, 0, 0]} raycast={() => null}>
          <cylinderGeometry args={[poleRadius, poleRadius, poleLen, 12]} />
          <meshBasicMaterial color="#3366dd" depthWrite={false} />
        </mesh>
        <mesh position={[0, 0, -(poleLen + coneH / 2)]} rotation={[-Math.PI / 2, 0, 0]} raycast={() => null}>
          <coneGeometry args={[coneBaseR, coneH, 12]} />
          <meshBasicMaterial color="#3366dd" depthWrite={false} />
        </mesh>
      </>)}
      {!octants && (<>
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
      </>)}
      {octants && OCTANTS.map((o) => (
        <group key={`oarc-${o.tag}`} scale={[o.s[0], o.s[1], o.s[2]]}>
          {/* θ arc nudged off z=0, φ arc off y=0, into this octant — so adjacent octants'
              arcs don't coincide. Opaque (not transparent) so the visible color is
              render-order-stable, not camera-sorted. */}
          <mesh position={[0, 0, arcOff]} raycast={() => null}>
            <torusGeometry args={[arcR, arcTube, 8, 48, Math.PI / 2]} />
            <meshBasicMaterial color={o.color} depthWrite={false} />
          </mesh>
          <mesh position={[0, arcOff, 0]} rotation={[Math.PI / 2, 0, 0]} raycast={() => null}>
            <torusGeometry args={[arcR, arcTube, 8, 48, Math.PI / 2]} />
            <meshBasicMaterial color={o.color} depthWrite={false} />
          </mesh>
        </group>
      ))}
      {/* Labels — billboard sprites, always face the camera. */}
      <AxisLabel text={`+Y pole${sfx}`} color="#22dd55" position={[0, poleLen + coneH * 2, 0]} size={poleLen * 0.12} />
      <AxisLabel text={`+X φ0${sfx}`} color="#dd3333" position={[poleLen + coneH * 2, 0, 0]} size={poleLen * 0.12} />
      <AxisLabel text={`+Z φ90${sfx}`} color="#3366dd" position={[0, 0, poleLen + coneH * 2]} size={poleLen * 0.12} />
      {octants && (<>
        <AxisLabel text={`−Y${sfx}`} color="#22dd55" position={[0, -(poleLen + coneH * 2), 0]} size={poleLen * 0.12} />
        <AxisLabel text={`−X φ180${sfx}`} color="#dd3333" position={[-(poleLen + coneH * 2), 0, 0]} size={poleLen * 0.12} />
        <AxisLabel text={`−Z φ270${sfx}`} color="#3366dd" position={[0, 0, -(poleLen + coneH * 2)]} size={poleLen * 0.12} />
      </>)}
      {!octants && (<>
      <AxisLabel text="θ" color="#dd33cc" position={[arcMid, arcMid, 0]} size={poleLen * 0.14} />
      <AxisLabel text="φ" color="#dddd22" position={[arcMid, 0, arcMid]} size={poleLen * 0.14} />
      </>)}
      {octants && OCTANTS.map((o) => (
        <React.Fragment key={`olbl-${o.tag}`}>
          {/* θ label at this octant's θ-arc midpoint (X-Y plane), φ at its φ-arc midpoint
              (X-Z plane), reflected by the octant sign and colored per octant. */}
          {/* Sign of θ = pole-axis (y) side; sign of φ = z side (φ measured from +X φ0). */}
          <AxisLabel text={`${o.s[1] > 0 ? "+" : "−"}θ`} color={o.color} position={[o.s[0] * arcMid, o.s[1] * arcMid, 0]} size={poleLen * 0.11} />
          <AxisLabel text={`${o.s[2] > 0 ? "+" : "−"}φ`} color={o.color} position={[o.s[0] * arcMid, 0, o.s[2] * arcMid]} size={poleLen * 0.11} />
        </React.Fragment>
      ))}
      {octants && (<>
        {/* Decorative handholds (NO pick / NO behavior): an orange grab-sphere at each
            pole's MIDPOINT and each quarter-arc midpoint. raycast off, no
            userData.handhold. Opaque so the color is render-order-stable on camera move. */}
        {([[poleLen / 2, 0, 0], [-poleLen / 2, 0, 0], [0, poleLen / 2, 0], [0, -poleLen / 2, 0], [0, 0, poleLen / 2], [0, 0, -poleLen / 2]] as [number, number, number][]).map((p, i) => (
          <mesh key={`hhp-${i}`} position={p} raycast={() => null}>
            <sphereGeometry args={[hhR, 12, 12]} />
            <meshStandardMaterial color="#cc8844" emissive="#cc8844" emissiveIntensity={0.6} />
          </mesh>
        ))}
        {OCTANTS.map((o) => (
          <React.Fragment key={`hha-${o.tag}`}>
            <mesh position={[o.s[0] * arcHH, o.s[1] * arcHH, 0]} raycast={() => null}>
              <sphereGeometry args={[hhR, 12, 12]} />
              <meshStandardMaterial color="#cc8844" emissive="#cc8844" emissiveIntensity={0.6} />
            </mesh>
            <mesh position={[o.s[0] * arcHH, 0, o.s[2] * arcHH]} raycast={() => null}>
              <sphereGeometry args={[hhR, 12, 12]} />
              <meshStandardMaterial color="#cc8844" emissive="#cc8844" emissiveIntensity={0.6} />
            </mesh>
          </React.Fragment>
        ))}
      </>)}
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

function PolarSphere({ nodes, selectedId }: { nodes: RFNode<NodeData>[]; selectedId?: string | null }) {
  // Re-derive when Go streams node geometry (positions change → content sphere moves).
  const geoms = useNodeGeometryStore((s) => s.geoms);
  const sceneToriVisible = useCameraStore((s) => s.sceneToriVisible);
  const scenePolesVisible = useCameraStore((s) => s.scenePolesVisible);
  const nodePolesVisible = useCameraStore((s) => s.nodePolesVisible);
  const selSpherePolesVisible = useCameraStore((s) => s.selSpherePolesVisible);
  const angleLabelsVisible = useCameraStore((s) => s.angleLabelsVisible);
  const handholdsVisible = useCameraStore((s) => s.handholdsVisible);
  const overlaysVisible = useCameraStore((s) => s.overlaysVisible);

  // "Overlays" master gate (Go-owned): when false, ALL polar guides are suppressed (the
  // toolbar also hides their individual buttons). It does NOT touch each guide's own
  // Go-owned visibility, so reactivating restores every guide to its prior on/off state.
  const g = overlaysVisible;
  const showTori = g && sceneToriVisible !== false;
  const showScenePoles = g && scenePolesVisible !== false;
  const showNodePoles = g && nodePolesVisible !== false;
  const showSelPoles = g && selSpherePolesVisible !== false;
  const showAngles = g && angleLabelsVisible !== false;

  // Latch the last node the user selected. Selection only DECIDES which sphere the
  // sel-highlight frames; it does not have to stay selected to keep the frame shown.
  // So DEselecting the node (clicking empty space) leaves the latched sphere framed —
  // only selecting a different node replaces it. The sel toggle still gates visibility.
  const [latchedSel, setLatchedSel] = useState<string | null>(selectedId ?? null);
  useEffect(() => {
    if (selectedId) setLatchedSel(selectedId);
  }, [selectedId]);

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
  // Node ids remapped by the node rename: the old node 2 (HoldNewSendOld) is now node
  // 5, and its two children old 3/7 are now 7/8. The variable names keep the original
  // meaning (the parent and its two children) under the new ids.
  const node2 = nodes.find((n) => n.id === "5");
  const node2Center = node2 ? nodeWorldPos(node2) : null;
  const node2Scale = radiusKey * 0.5;

  // θ-lock check: vertical θ meridian arcs from the parent's pole down to its two
  // children, from LIVE positions. θ traces vertically; equal θ ⇒ equal arc sweep
  // (rotated apart in φ), different θ ⇒ different sweep length. (See ThetaArc.)
  const node3 = nodes.find((n) => n.id === "7");
  const node7 = nodes.find((n) => n.id === "8");
  const thetaTube = Math.max(node2Scale * 0.014, 1.4);

  // Selected-sphere poles (separate, additional feature — gated by selSpherePolesVisible,
  // independent of the per-node poles below). The LATCHED selection decides which node's
  // sphere to frame (persists through deselect), and we draw THAT node's own sphere pole
  // frame at full SPHERE scale (its Go-streamed sphereR). Every node has a sphere, so this
  // works for leaf nodes (3, 5) too — no parent remapping. Never selected ⇒ no frame.
  const sphereCenters = latchedSel ? nodes.filter((n) => n.id === latchedSel) : [];

  // WORLD-FIXED tori: the pole is the diagram's own top axis (world Y), so the horizontal torus
  // (geoB, normal world Y) is the diagram's equator — the polar frame is anchored to the
  // diagram, not the camera.
  const pos: [number, number, number] = [cs.center.x, cs.center.y, cs.center.z];
  return (
    <>
      <group position={pos}>
        {showTori && (
          <>
            <mesh geometry={geoA} raycast={() => null}>
              <meshBasicMaterial color="#cc8844" transparent opacity={0.4} depthWrite={false} />
            </mesh>
            <mesh geometry={geoB} rotation={rotB} raycast={() => null}>
              <meshBasicMaterial color="#cc8844" transparent opacity={0.4} depthWrite={false} />
            </mesh>
          </>
        )}
        {/* Grab handholds (4 per torus, 90° apart) — the pickable part of the overlay. Gated by both overlaysVisible (master) and handholdsVisible (per-overlay). */}
        {g && handholdsVisible !== false && handholds()}
        {g && handholdsVisible !== false && handholds(rotB)}
      </group>
      {/* Scene pole frame at the content-sphere center. */}
      {showScenePoles && <PolarFrame center={cs.center} scale={radiusKey} />}
      {/* Per-node pole frames — one PolarFrame per node, gated behind nodePolesVisible. */}
      {showNodePoles && nodes.map((node) => (
        <PolarFrame
          key={node.id}
          center={nodeWorldPos(node)}
          scale={nodeRadius(node)}
          tag={`(${node.id})`}
        />
      ))}
      {/* Selected-sphere poles (additional feature) — the center(s) of the sphere(s) the
          SELECTED node sits on, drawn at SPHERE scale. Independent of the per-node poles. */}
      {showSelPoles && sphereCenters.map((center) => (
        <PolarFrame
          key={`sel-${center.id}`}
          center={nodeWorldPos(center)}
          scale={geoms[center.id]?.sphereR ?? nodeRadius(center)}
          tag={`(${center.id})`}
          octants
        />
      ))}
      {/* Vertical θ arcs from node 2's pole to node 3 (orange) and node 7 (cyan): equal sweep ⇒ equal θ. */}
      {showAngles && node2Center && node3 && (
        <ThetaArc center={node2Center} sample={nodeWorldPos(node3)} color="#ff8800" tube={thetaTube} />
      )}
      {showAngles && node2Center && node7 && (
        <ThetaArc center={node2Center} sample={nodeWorldPos(node7)} color="#00ccff" tube={thetaTube} />
      )}
      {/* Horizontal φ arcs from +x reference to node 3 (orange) and node 7 (cyan). */}
      {showAngles && node2Center && node3 && (
        <PhiArc center={node2Center} sample={nodeWorldPos(node3)} color="#ff8800" tube={thetaTube} />
      )}
      {showAngles && node2Center && node7 && (
        <PhiArc center={node2Center} sample={nodeWorldPos(node7)} color="#00ccff" tube={thetaTube} />
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// NavGuides — combined export
// ---------------------------------------------------------------------------

export function NavGuides({ nodes, selectedId }: { nodes: RFNode<NodeData>[]; selectedId?: string | null }) {
  return (
    <>
      <PolarSphere nodes={nodes} selectedId={selectedId} />
    </>
  );
}
