// SelectedEquationGuides.tsx — render-only guides for the FOCUSED polar equation
// (rule-builder.ts usePolarLocks().selectedLockIndex), scoped to ONLY that equation's own
// terms (its Center/A/B nodes, θ/φ angle arcs between them, and — for a `port ∈ torus`
// lock — the port marker + torus node). Pure REFLECT of the buffer: usePolarLocks decodes
// the PolarLock block (Go-owned, locks.go), and this component resolves the lock's node
// rows to world centers/radii via decodeNavNodes (the same buffer-nav path NavGuides
// uses). No TS state authority, no bridge sends, independent of the Overlays master gate
// (an equation selection should show its own guides regardless of overlay toggle state).

import * as THREE from "three";
import { useFrame } from "@react-three/fiber";
import { useRef, useState } from "react";
import { getLatestSnapshot } from "../snapshot-buffer";
import { decodeSnapshot } from "./buffer-decode";
import { decodeNavNodes, type NavNode } from "./buffer-nav";
import {
  readPortPX,
  readPortPY,
  readPortPZ,
  readEdgeSrcNodeRow,
  readEdgeDstNodeRow,
  readEdgeSX,
  readEdgeSY,
  readEdgeSZ,
  readEdgeEX,
  readEdgeEY,
  readEdgeEZ,
} from "../../schema/buffer-layout";
import { ThetaArc, PhiArc } from "./NavGuides";
import { usePolarLocks, POLAR_LOCK_KIND_PORT_TORUS } from "./rule-builder";

const ROW_NONE = -1;

// Selected-edge highlight look, copied verbatim from buffer-scene.tsx EdgeTube's halo mesh
// (EDGE_HALO_RADIUS/COLOR/SELECTED_OPACITY) so an equation's edges read identically to a
// hand-selected edge. Kept as a local copy because those constants are module-private there.
const EDGE_HALO_RADIUS = 5;
const EDGE_HALO_COLOR = "#ff5a00";
const EDGE_HALO_SELECTED_OPACITY = 0.6;

// EdgeHalo — the same wide orange halo tube EdgeTube draws for a selected edge, over the
// straight segment between two baked edge endpoints. Decorative — no raycast.
function EdgeHalo({ s, e }: { s: THREE.Vector3; e: THREE.Vector3 }) {
  const curve = new THREE.LineCurve3(s, e);
  return (
    <mesh raycast={() => null} frustumCulled={false}>
      <tubeGeometry args={[curve, 1, EDGE_HALO_RADIUS, 6, false]} />
      <meshBasicMaterial
        color={EDGE_HALO_COLOR}
        transparent
        opacity={EDGE_HALO_SELECTED_OPACITY}
        side={THREE.DoubleSide}
        depthWrite={false}
      />
    </mesh>
  );
}

// A single torus ring (the "torus" half of a `port ∈ torus` lock), drawn at a node's own
// world radius in the sphere's equatorial plane (torusGeometry lies in XY → normal +Z; rotate
// -90° about X so its normal is +Y, matching the Y-pole sphere frame). Decorative — no raycast.
function TorusRing({ node, color }: { node: NavNode; color: string }) {
  const r = node.sphereR ?? node.radius ?? 1;
  return (
    <mesh
      position={[node.center.x, node.center.y, node.center.z]}
      rotation={[-Math.PI / 2, 0, 0]}
      scale={r}
      raycast={() => null}
      frustumCulled={false}
    >
      <torusGeometry args={[1, 0.03, 8, 64]} />
      <meshStandardMaterial color={color} emissive={color} emissiveIntensity={0.4} />
    </mesh>
  );
}

// Node highlight (ring + halo) styled like buffer-scene.tsx SelectionHighlight, scaled to
// the node's own world radius. Decorative — raycast disabled.
function NodeHighlight({ node, color }: { node: NavNode; color: string }) {
  const r = node.radius || 1;
  return (
    <group position={[node.center.x, node.center.y, node.center.z]} scale={r}>
      <mesh raycast={() => null} frustumCulled={false}>
        <torusGeometry args={[1, 0.14, 8, 32]} />
        <meshStandardMaterial color={color} emissive={color} emissiveIntensity={0.3} />
      </mesh>
      <mesh raycast={() => null} frustumCulled={false}>
        <sphereGeometry args={[1.45, 16, 16]} />
        <meshBasicMaterial color={color} transparent opacity={0.35} side={THREE.DoubleSide} depthWrite={false} />
      </mesh>
    </group>
  );
}

// Term coordinate codes (mirror gesture.go ruleTermCode / RuleEquationPanel ANGLE_CHIPS):
// 0=θ, 1=φ, 2=−θ, 3=−φ, 4=r. An equation term constrains exactly ONE coordinate, so its guide
// shows only that coordinate — a θ term draws a θ arc, an r term draws a radial line, etc.
const CODE_THETA = 0;
const CODE_PHI = 1;
const CODE_NEG_THETA = 2;
const CODE_NEG_PHI = 3;
const CODE_R = 4;

// TermGuide — draws the single coordinate guide a term constrains, chosen by the term's code:
// a θ arc, a φ arc, or (for a radius term) NOTHING here — the radius is shown by the orange
// center→term edge halo (see the edge-halo block below), so a separate radial cylinder would
// double up with it. Never draws the other coordinates.
function TermGuide({
  center,
  node,
  code,
  color,
  tube,
}: {
  center: NavNode;
  node: NavNode;
  code: number;
  color: string;
  tube: number;
}) {
  if (code === CODE_THETA || code === CODE_NEG_THETA) {
    return <ThetaArc center={center.center} sample={node.center} color={color} tube={tube} />;
  }
  if (code === CODE_PHI || code === CODE_NEG_PHI) {
    return <PhiArc center={center.center} sample={node.center} color={color} tube={tube} />;
  }
  // CODE_R draws nothing here — the radius is the center→term edge halo below.
  return null;
}

// Small port marker (mirrors PortInstances' sphere look) at a single world position —
// used to call out the `port` half of a `port ∈ torus` lock.
function PortMarker({ position, color }: { position: THREE.Vector3; color: string }) {
  return (
    <mesh position={position} raycast={() => null}>
      <sphereGeometry args={[4, 12, 12]} />
      <meshStandardMaterial color={color} emissive={color} emissiveIntensity={0.6} />
    </mesh>
  );
}

// SelectedEquationGuides — draws guides for ONLY the currently-focused equation (committed
// PolarLock row), scoped to that equation's own terms. Renders nothing when no equation is
// selected (selectedLockIndex < 0) — this is intentionally independent of the Overlays
// master gate (bufFlags.overlays), so focusing an equation always shows its own guides.
export function SelectedEquationGuides() {
  const { equations, selectedLockIndex } = usePolarLocks();

  // Buffer-driven node sampling — same decode path NavGuides uses (decodeNavNodes), kept
  // fresh via useFrame + a coarse tick so this rebuilds on real position/selection changes
  // rather than every frame.
  const [tick, setTick] = useState(0);
  const navRef = useRef<NavNode[]>([]);
  const sigRef = useRef("");
  useFrame(() => {
    const snap = getLatestSnapshot();
    if (!snap) return;
    const decoded = decodeSnapshot(snap);
    if (!decoded) return;
    navRef.current = decodeNavNodes(decoded);
    let sig = "";
    for (const n of navRef.current) {
      sig += `${n.row}:${Math.round(n.center.x)},${Math.round(n.center.y)},${Math.round(n.center.z)},${Math.round(n.radius)};`;
    }
    if (sig !== sigRef.current) {
      sigRef.current = sig;
      setTick((t) => t + 1);
    }
  });
  // Reading navRef.current directly is fine here: `tick` above is the render trigger (state
  // bump on real change), and this component re-renders whenever it changes.
  const navNodes = navRef.current;

  if (selectedLockIndex < 0) return null;
  const lock = equations.find((e) => e.index === selectedLockIndex);
  if (!lock) return null;

  const byRow = (row: number): NavNode | null => {
    if (row === ROW_NONE) return null;
    return navNodes.find((n) => n.row === row) ?? null;
  };

  const center = byRow(lock.centerRow);
  const a = byRow(lock.a.row);
  const b = byRow(lock.b.row);

  // Angle-arc tube scale, mirroring NavGuides' thetaTube derivation off the center node's
  // own radius (falls back to a small constant if the center hasn't resolved yet).
  const tube = center ? Math.max(center.radius * 0.5 * 0.014, 1.4) : 1.4;

  // For a port-torus lock, resolve the port's live world position (PX/PY/PZ) directly from
  // the buffer (same source PortInstances draws from), and the torus node it belongs to.
  let portPos: THREE.Vector3 | null = null;
  let torusNode: NavNode | null = null;
  if (lock.kind === POLAR_LOCK_KIND_PORT_TORUS) {
    const snap = getLatestSnapshot();
    const decoded = snap ? decodeSnapshot(snap) : null;
    if (decoded && lock.portRow !== ROW_NONE && lock.portRow < decoded.portCount) {
      portPos = new THREE.Vector3(
        readPortPX(decoded.portView, lock.portRow),
        readPortPY(decoded.portView, lock.portRow),
        readPortPZ(decoded.portView, lock.portRow),
      );
    }
    torusNode = byRow(lock.torusRow);
  }

  // Only highlight the center node when it's a DISTINCT reference node (not just an alias of
  // a term) — otherwise a 2-term equation would triple-highlight its own nodes.
  const centerIsTerm =
    !!center && ((a && center.row === a.row) || (b && center.row === b.row));

  // Edges to highlight with the same halo EdgeTube draws for a selected edge — chosen by what
  // each term constrains, NOT a flat node set:
  //   • term↔term: the connection between the two term nodes (a real edge, if any).
  //   • center→term: ONLY for a RADIUS (code r) term — that spoke IS the radius the equation
  //     constrains, so it gets the halo. An ANGLE term draws its arc and NO center spoke
  //     (a center→angle-term spoke read as a spurious "radius" highlight on θ/φ equations).
  // Each wanted pair is an unordered {row,row} key; an edge matches if its endpoints are that
  // pair in either orientation. Uses raw lock rows so it works before NavNodes resolve.
  const pairKey = (x: number, y: number) => (x < y ? `${x}:${y}` : `${y}:${x}`);
  const wantedPairs = new Set<string>();
  if (lock.a.row !== ROW_NONE && lock.b.row !== ROW_NONE) {
    wantedPairs.add(pairKey(lock.a.row, lock.b.row));
  }
  if (lock.a.code === CODE_R && lock.centerRow !== ROW_NONE && lock.a.row !== ROW_NONE) {
    wantedPairs.add(pairKey(lock.centerRow, lock.a.row));
  }
  if (lock.b.code === CODE_R && lock.centerRow !== ROW_NONE && lock.b.row !== ROW_NONE) {
    wantedPairs.add(pairKey(lock.centerRow, lock.b.row));
  }
  const edgeHalos: Array<{ s: THREE.Vector3; e: THREE.Vector3 }> = [];
  if (wantedPairs.size > 0) {
    const snap = getLatestSnapshot();
    const decoded = snap ? decodeSnapshot(snap) : null;
    if (decoded) {
      for (let i = 0; i < decoded.edgeCount; i++) {
        const src = readEdgeSrcNodeRow(decoded.edgeView, i);
        const dst = readEdgeDstNodeRow(decoded.edgeView, i);
        if (wantedPairs.has(pairKey(src, dst))) {
          edgeHalos.push({
            s: new THREE.Vector3(
              readEdgeSX(decoded.edgeView, i),
              readEdgeSY(decoded.edgeView, i),
              readEdgeSZ(decoded.edgeView, i),
            ),
            e: new THREE.Vector3(
              readEdgeEX(decoded.edgeView, i),
              readEdgeEY(decoded.edgeView, i),
              readEdgeEZ(decoded.edgeView, i),
            ),
          });
        }
      }
    }
  }

  return (
    <>
      {/* Per-term coordinate guide — each term draws ONLY the coordinate it constrains
          (θ arc, φ arc, or radial line), chosen by its code. The center is used only as the
          pole/origin to measure from; no frame is drawn there (that read as an overlay). */}
      {center && a && (
        <TermGuide center={center} node={a} code={lock.a.code} color="#ff8800" tube={tube} />
      )}
      {center && b && (
        <TermGuide center={center} node={b} code={lock.b.code} color="#00ccff" tube={tube} />
      )}
      {/* Edges between the equation's own nodes — same halo as a selected edge. */}
      {edgeHalos.map((h, i) => (
        <EdgeHalo key={i} s={h.s} e={h.e} />
      ))}
      {/* Node highlights — this equation's own term nodes only. */}
      {a && <NodeHighlight node={a} color="#ff8800" />}
      {b && <NodeHighlight node={b} color="#00ccff" />}
      {center && !centerIsTerm && <NodeHighlight node={center} color="#ffcc00" />}
      {/* `port ∈ torus` lock ONLY: the port marker and a single torus ring on the torus node.
          Node-node equations have neither, so nothing extra is drawn for them. */}
      {portPos && <PortMarker position={portPos} color="#aaddff" />}
      {torusNode && <TorusRing node={torusNode} color="#aaddff" />}
    </>
  );
}
