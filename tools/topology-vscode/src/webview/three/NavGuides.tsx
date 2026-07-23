// NavGuides.tsx — decorative 3D navigation overlays:
//   PolarSphere: two perpendicular tori centered on mid-depth focus point,
//     radius scaled to ARCBALL_FILL * camera-to-focus distance, updated each frame.
// Purely decorative: raycast disabled, depthWrite false, transparent.

import React, { useMemo, useState, useEffect, useRef } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { useOverlayFlags } from "./overlay-flags";
import { getNodeFrameOrFallback } from "./node-stream-blocks";
import { getViewBlocks } from "./view-blocks";
import {
  type NavNode, decodeNavNodes, sceneSphereFromSnapshot,
} from "./buffer-nav";

// HANDHOLD_TERM_TAG — userData key stamped on the octant angle handhold meshes and the
// pole-crossing radius handholds with their term-id (+θ=0, +φ=1, -θ=2, -φ=3, r=4; see
// nodes/Wiring/gesture.go). Mirrors
// BUFFER_EDGE_TAG (buffer-scene.tsx) as the pattern for a numeric pick-payload tag.
export const HANDHOLD_TERM_TAG = "handholdTerm";

// navSignature — coarse fingerprint of the buffer-derived nav nodes (rounded
// positions/radii/sphereR/selection). NavGuides bumps a render tick only when this
// changes, so the tori/frames rebuild on real position/selection changes (a drag)
// without per-frame churn — re-rendering on node-geometry
// store events. Not used on the flag-off path.
function navSignature(nav: NavNode[]): string {
  let s = "";
  for (const n of nav) {
    s += `${n.row}:${Math.round(n.center.x)},${Math.round(n.center.y)},${Math.round(n.center.z)},${Math.round(n.radius)},${Math.round(n.sphereR ?? 0)},${n.selected ? 1 : 0},${n.latchedSel ? 1 : 0};`;
  }
  return s;
}

// ---------------------------------------------------------------------------
// PolarSphere — two perpendicular tori tracking the polar rotation-sphere center.
// Major radius = ARCBALL_FILL * camera-to-focus distance; updated every frame.
// ---------------------------------------------------------------------------

// AxisLabel — canvas-texture Sprite billboard; always faces the camera, no font asset needed.
// Exported for reuse by other buffer-driven 3D overlays (e.g. PortLabels' port-name tags).
export function AxisLabel({ text, color, position, size }: {
  text: string; color: string; position: [number, number, number]; size: number;
}) {
  const texture = useMemo(() => {
    const c = document.createElement("canvas");
    c.width = 256; c.height = 64;
    const ctx = c.getContext("2d");
    if (!ctx) return new THREE.CanvasTexture(c);
    ctx.font = "bold 44px sans-serif";
    ctx.textAlign = "center"; ctx.textBaseline = "middle";
    ctx.fillStyle = color;
    ctx.fillText(text, 128, 32);
    const t = new THREE.CanvasTexture(c);
    t.needsUpdate = true;
    return t;
  }, [text, color]);
  // Dispose the previous texture when deps change and on unmount to prevent GPU memory leaks.
  useEffect(() => {
    return () => { texture.dispose(); };
  }, [texture]);
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

// ── ARC NUMBER ↔ COLOR LEGEND ───────────────────────────────────────────────
// Each quarter-arc carries a unique number (θ arcs 1..8, φ arcs 9..16) drawn near
// it, colored by its octant (OCTANTS[i].color). θ# = i+1, φ# = i+9.
//
// Per-octant (number → octant → color):
//    #1 / #9   +x+y+z   white        #ffffff
//    #2 / #10  +x+y−z   orange       #ff8c00
//    #3 / #11  +x−y+z   teal         #00ced1
//    #4 / #12  +x−y−z   purple       #9370db
//    #5 / #13  −x+y+z   pink         #ff69b4
//    #6 / #14  −x+y−z   yellow-green  #9acd32
//    #7 / #15  −x−y+z   sky-blue     #00bfff
//    #8 / #16  −x−y−z   peru/tan     #cd853f
//
// Grouped by shared-position REGION (the two offset circles you see together —
// a→color1, b→color2 — so you can note just the numbers):
//   θ regions (X-Y plane):        φ regions (X-Z plane):
//     +x+y :  1 white  / 2 orange    +x+z :  9 white      / 11 teal
//     +x−y :  3 teal   / 4 purple    +x−z : 10 orange     / 12 purple
//     −x+y :  5 pink   / 6 yel-grn   −x+z : 13 pink       / 15 sky-blue
//     −x−y :  7 sky-blu/ 8 peru      −x−z : 14 yel-grn    / 16 peru
// ────────────────────────────────────────────────────────────────────────────

// User-chosen single circle per region (1 per θ/φ). Each: sign pair, its number, color.
const THETA_CIRCLES: { sx: number; sy: number; n: number; c: string }[] = [
  { sx: 1, sy: 1, n: 2, c: "#ff8c00" },
  { sx: 1, sy: -1, n: 4, c: "#9370db" },
  { sx: -1, sy: 1, n: 6, c: "#9acd32" },
  { sx: -1, sy: -1, n: 8, c: "#cd853f" },
];
const PHI_CIRCLES: { sx: number; sz: number; n: number; c: string }[] = [
  { sx: 1, sz: 1, n: 11, c: "#00ced1" },
  { sx: 1, sz: -1, n: 12, c: "#9370db" },
  { sx: -1, sz: 1, n: 13, c: "#ff69b4" },
  { sx: -1, sz: -1, n: 14, c: "#9acd32" },
];

// PolarFrame — the camera-independent pole-frame markers for ONE center: the three
// axis sticks (+y pole green, +x φ0 red, +z φ90 blue) plus the θ (magenta) and φ
// (yellow) angle arcs, all anchored at `center` with the pole = world +y. `scale`
// sizes the frame (≈ the radius it should reach). `tag` suffixes the axis labels so
// the scene frame and a node's frame are distinguishable. Decorative (raycast off),
// not affected by the scene-tori toggle. Same drawing for every center, so node 2's
// frame matches the scene's exactly.
export function PolarFrame({ center, scale, tag, octants }: {
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
      {octants && THETA_CIRCLES.map((t) => (
        <group key={`tc-${t.n}`} scale={[t.sx, t.sy, 1]}>
          <mesh raycast={() => null}>
            <torusGeometry args={[arcR, arcTube, 8, 48, Math.PI / 2]} />
            <meshBasicMaterial color={t.c} depthWrite={false} />
          </mesh>
        </group>
      ))}
      {octants && PHI_CIRCLES.map((p) => (
        <group key={`pc-${p.n}`} scale={[p.sx, 1, p.sz]}>
          <mesh rotation={[Math.PI / 2, 0, 0]} raycast={() => null}>
            <torusGeometry args={[arcR, arcTube, 8, 48, Math.PI / 2]} />
            <meshBasicMaterial color={p.c} depthWrite={false} />
          </mesh>
        </group>
      ))}
      {/* Labels — billboard sprites, always face the camera. */}
      <AxisLabel text={`+Y pole${sfx}`} color="#22dd55" position={[0, poleLen + coneH * 2, 0]} size={poleLen * 0.12} />
      <AxisLabel text={`+X φ0${sfx}`} color="#dd3333" position={[poleLen + coneH * 2, 0, 0]} size={poleLen * 0.12} />
      <AxisLabel text={`+Z φπ/2${sfx}`} color="#3366dd" position={[0, 0, poleLen + coneH * 2]} size={poleLen * 0.12} />
      {octants && (<>
        <AxisLabel text={`−Y${sfx}`} color="#22dd55" position={[0, -(poleLen + coneH * 2), 0]} size={poleLen * 0.12} />
        <AxisLabel text={`−X φπ${sfx}`} color="#dd3333" position={[-(poleLen + coneH * 2), 0, 0]} size={poleLen * 0.12} />
        <AxisLabel text={`−Z φ3π/2${sfx}`} color="#3366dd" position={[0, 0, -(poleLen + coneH * 2)]} size={poleLen * 0.12} />
      </>)}
      {!octants && (<>
      <AxisLabel text="θ" color="#dd33cc" position={[arcMid, arcMid, 0]} size={poleLen * 0.14} />
      <AxisLabel text="φ" color="#dddd22" position={[arcMid, 0, arcMid]} size={poleLen * 0.14} />
      </>)}
      {octants && THETA_CIRCLES.map((t) => (
        <AxisLabel key={`tl-${t.n}`} text={`${t.sy > 0 ? "+" : "−"}θ`} color={t.c} position={[t.sx * arcMid, t.sy * arcMid, 0]} size={poleLen * 0.11} />
      ))}
      {octants && PHI_CIRCLES.map((p) => (
        <AxisLabel key={`pl-${p.n}`} text={`${p.sz > 0 ? "+" : "−"}φ`} color={p.c} position={[p.sx * arcMid, 0, p.sz * arcMid]} size={poleLen * 0.11} />
      ))}
      {octants && (<>
        {/* Radius (r) handholds: the six pole-crossing grab-spheres (±arcR on each axis).
            All pickable and stamped with the r term-id (code 4, unsigned), so grabbing any
            of them selects the node's RADIUS component for the rule-builder. */}
        {([[arcR, 0, 0], [-arcR, 0, 0], [0, arcR, 0], [0, -arcR, 0], [0, 0, arcR], [0, 0, -arcR]] as [number, number, number][]).map((p, i) => (
          <mesh key={`hhp-${i}`} position={p} userData={{ [HANDHOLD_TERM_TAG]: 4 }}>
            <sphereGeometry args={[hhR, 12, 12]} />
            <meshStandardMaterial color="#cc8844" emissive="#cc8844" emissiveIntensity={0.6} />
          </mesh>
        ))}
        {/* θ/φ angle handholds: pickable, stamped with their term-id so the rule-builder
            (nodes/Wiring/gesture.go) can decode which (comp, sign) term was clicked. */}
        {THETA_CIRCLES.map((t) => (
          <mesh
            key={`th-${t.n}`}
            position={[t.sx * arcHH, t.sy * arcHH, 0]}
            userData={{ [HANDHOLD_TERM_TAG]: (t.sy < 0 ? 2 : 0) + 0 }}
          >
            <sphereGeometry args={[hhR, 12, 12]} />
            <meshStandardMaterial color="#cc8844" emissive="#cc8844" emissiveIntensity={0.6} />
          </mesh>
        ))}
        {PHI_CIRCLES.map((p) => (
          <mesh
            key={`ph-${p.n}`}
            position={[p.sx * arcHH, 0, p.sz * arcHH]}
            userData={{ [HANDHOLD_TERM_TAG]: (p.sz < 0 ? 2 : 0) + 1 }}
          >
            <sphereGeometry args={[hhR, 12, 12]} />
            <meshStandardMaterial color="#cc8844" emissive="#cc8844" emissiveIntensity={0.6} />
          </mesh>
        ))}
      </>)}
    </group>
  );
}

// NavGuides — decorative 3D navigation overlays (the polar-sphere tori, pole
// frames, and θ/φ arcs). Rendered directly as the combined export; there is no
// pass-through wrapper.
export function NavGuides() {
  // Overlay flags are Go-owned and streamed into the buffer's Overlay columns. useOverlayFlags
  // subscribes to snapshot arrivals so a flip re-renders even when the node-position
  // navSignature is unchanged. null until the first snapshot lands (nothing to draw yet).
  const bufFlags = useOverlayFlags();

  // "Overlays" master gate (Go-owned): when false, ALL polar guides are suppressed (the
  // toolbar also hides their individual buttons). It does NOT touch each guide's own
  // Go-owned visibility, so reactivating restores every guide to its prior on/off state.
  const g = bufFlags?.overlays ?? false;
  const showTori = g && !!bufFlags?.tori;
  const showScenePoles = g && !!bufFlags?.scenePoles;
  const showNodePoles = g && !!bufFlags?.nodePoles;
  const showSelPoles = g && !!bufFlags?.selSpherePoles;
  const showHandholds = g && !!bufFlags?.handholds;

  // ── Buffer-driven nav sampling ───────────────────────────────────────────────
  // The overlay geometry derives from the binary buffer (Go-owned node centers/radii/sphereR
  // + Go-owned selection column). Sample the latest snapshot each frame and bump a render tick
  // only when the coarse signature changes, so tori/frames rebuild on real position/selection
  // changes (a drag) — not every frame.
  const [navTick, setNavTick] = useState(0);
  const bufNavRef = useRef<NavNode[]>([]);
  const bufSigRef = useRef("");
  // Scene sphere: Go-owned, established once at load and never moved (see
  // sceneSphereFromSnapshot) — sampled alongside navNodes but not part of navSignature
  // since it is constant after the first snapshot.
  const sceneSphereRef = useRef<{ center: THREE.Vector3; radius: number }>({ center: new THREE.Vector3(), radius: 100 });
  useFrame(() => {
    // Visibility gate FIRST: if none of the guides this component renders is on, skip the
    // per-node decode/allocate work entirely (decodeNavNodes/sceneSphereFromSnapshot/
    // navSignature all allocate per node, per frame). Mirrors the exact flag set the JSX
    // below gates on, read early instead of only at render time.
    if (!showTori && !showScenePoles && !showNodePoles && !showSelPoles && !showHandholds) return;
    const blocks = getViewBlocks();
    const decodedNode = getNodeFrameOrFallback();
    if (!decodedNode || !blocks) return;
    bufNavRef.current = decodeNavNodes(decodedNode);
    sceneSphereRef.current = sceneSphereFromSnapshot(blocks);
    const sig = navSignature(bufNavRef.current);
    if (sig !== bufSigRef.current) {
      bufSigRef.current = sig;
      setNavTick((t) => t + 1);
    }
  });

  // Node records that drive every guide below. Memoized so downstream guide computations
  // recompute only when the node data actually changes (navTick bumps on a real change).
  const navNodes = useMemo<NavNode[]>(
    () => bufNavRef.current,
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [navTick],
  );

  // Latched selection: Go-owned LatchedSel column (see Buffer/layout.go / setSelected in
  // Buffer/snapshot.go). Selection only DECIDES which sphere the sel-highlight frames; it
  // does not have to stay selected to keep the frame shown. So DEselecting the node
  // (clicking empty space) leaves the latched sphere framed — only selecting a different
  // node replaces it. The sel toggle still gates visibility. This is read-only reflection
  // of Go's own latch state — NavGuides authors nothing.
  const latchedSel = navNodes.find((n) => n.latchedSel)?.row ?? null;

  // WORLD-FIXED scene sphere (Go-owned, established once at load — see
  // sceneSphereFromSnapshot), so it zooms WITH the diagram. Tube thickness matches the node
  // spheres' tori (scene-content SphereRing: max(0.5, nodeRadius·0.08)).
  const cs = sceneSphereRef.current;
  const tube = navNodes.length > 0 ? Math.max(0.5, navNodes[0]!.radius * 0.08) : 1;
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
  // Dispose the outgoing GPU geometries when the memo rebuilds (radius/tube change) or
  // on unmount. React runs this cleanup for the PREVIOUS geoA/geoB before creating the
  // next pair, so the still-mounted current pair is never double-disposed. NavGuides
  // re-renders on every node-geometry stream event (incl. drags); without this the
  // replaced TorusGeometry buffers leak.
  useEffect(() => {
    return () => {
      geoA.dispose();
      geoB.dispose();
    };
  }, [geoA, geoB]);
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
      {hhAngles.map((a) => (
        <mesh key={a} position={[radiusKey * Math.cos(a), radiusKey * Math.sin(a), 0]} userData={{ handhold: true }}>
          <sphereGeometry args={[hhRadius, 16, 16]} />
          <meshStandardMaterial color="#cc8844" emissive="#cc8844" emissiveIntensity={0.6} transparent opacity={0.9} />
        </mesh>
      ))}
    </group>
  );

  if (navNodes.length < 1) return null;

  // Selected-sphere poles (separate, additional feature — gated by selSpherePolesVisible,
  // independent of the per-node poles below). The LATCHED selection decides which node's
  // sphere to frame (persists through deselect), and we draw THAT node's own sphere pole
  // frame at full SPHERE scale (its Go-streamed sphereR). Every node has a sphere, so this
  // works for leaf nodes (3, 5) too — no parent remapping. Never selected ⇒ no frame.
  const sphereCenters = latchedSel !== null ? navNodes.filter((n) => n.row === latchedSel) : [];

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
        {showHandholds && handholds()}
        {showHandholds && handholds(rotB)}
      </group>
      {/* Scene pole frame at the content-sphere center. */}
      {showScenePoles && <PolarFrame center={cs.center} scale={radiusKey} />}
      {/* Per-node pole frames — one PolarFrame per node, gated behind nodePolesVisible. */}
      {showNodePoles && navNodes.map((node) => (
        <PolarFrame
          key={node.row}
          center={node.center}
          scale={node.radius}
          tag={`(${node.label})`}
        />
      ))}
      {/* Selected-sphere poles (additional feature) — the center(s) of the sphere(s) the
          SELECTED node sits on, drawn at SPHERE scale. Independent of the per-node poles. */}
      {showSelPoles && sphereCenters.map((center) => (
        <PolarFrame
          key={`sel-${center.row}`}
          center={center.center}
          scale={center.sphereR ?? center.radius}
          tag={`(${center.label})`}
          octants
        />
      ))}
    </>
  );
}

