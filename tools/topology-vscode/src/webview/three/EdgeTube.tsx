// EdgeTube.tsx — real 3D edge render matching the JSON path's SingleEdgeTube,
// plus the EdgeTubes buffer-poll wrapper that rebuilds
// per-edge TubeGeometry only when a coordinate actually changes. Split out of
// buffer-scene.tsx: pure buffer→GPU render, no state authority beyond that geometry-cache
// React state.

import React, { useRef, useState, useMemo, useEffect } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { getLatestSnapshot } from "../snapshot-buffer";
import { decodeSnapshot } from "./buffer-decode";
import {
  SHADING_PARAM_NODE_FADE_OPACITY,
  SHADING_PARAM_TUBE_COLOR,
  SHADING_PARAM_TUBE_EMISSIVE,
  SHADING_PARAM_TUBE_EMISSIVE_INTENSITY,
} from "../../schema/shading-params";
import {
  readEdgeSX, readEdgeSY, readEdgeSZ, readEdgeEX, readEdgeEY, readEdgeEZ,
  readEdgeSelected, readEdgeFaded,
} from "../../schema/buffer-layout";
import { BUFFER_EDGE_TAG, DIRECTION_ZERO_EPS } from "./buffer-scene-shared";

// Arrowhead cone dims for the core tube — mirror scene-graph.tsx.
const ARROWHEAD_LENGTH = 6;
const ARROWHEAD_RADIUS = 3;
// Edge selection/pick halo radius (world units) — the pre-branch SingleEdgeTube halo
// (TubeGeometry(curve,1,5,6)). This wide concentric tube is ALWAYS mounted per edge as the
// raycast pick target (opacity 0 when unselected but still hittable) and painted orange
// (#ff5a00, opacity 0.6) on the Go-selected edge.
// Exported so other buffer-driven overlays (e.g. SelectedEquationGuides) that draw the
// SAME selected-edge halo look can share this single source of truth instead of keeping
// a duplicate local copy.
export const EDGE_HALO_RADIUS = 5;
export const EDGE_HALO_COLOR = "#ff5a00";
export const EDGE_HALO_SELECTED_OPACITY = 0.6;
const TUBE_EMISSIVE_COLOR = new THREE.Color(SHADING_PARAM_TUBE_EMISSIVE);

interface EdgeSeg { sx: number; sy: number; sz: number; ex: number; ey: number; ez: number; }

/**
 * Builds an arrow descriptor: a cone whose apex sits at `apex`, pointing in `dir`
 * (normalized, toward the apex). ConeGeometry apex is at +Y; we rotate +Y onto `dir`.
 * center places the cone so its apex lands at `apex`. Mirrors scene-graph.tsx buildArrow.
 */
function buildArrow(apex: THREE.Vector3, dir: THREE.Vector3, height: number): {
  center: THREE.Vector3; q: THREE.Quaternion;
} {
  const q = new THREE.Quaternion().setFromUnitVectors(new THREE.Vector3(0, 1, 0), dir);
  const center = apex.clone().addScaledVector(dir, -height / 2);
  return { center, q };
}

// One edge's core tube (radius 1.5) + destination arrowhead, mirroring SingleEdgeTube.
// `row` is this edge's buffer EDGE-ROW index — stamped on the wide halo's
// userData[BUFFER_EDGE_TAG] as the pickable edge target (mirrors the pre-branch
// SingleEdgeTube halo, which doubled as the pick tube). `selected` paints that halo orange
// (opacity 0.6) when Go marks this edge selected; otherwise the halo stays opacity 0 but
// remains raycast-hittable.
function EdgeTube({ seg, row, selected, faded }: { seg: EdgeSeg; row: number; selected: boolean; faded: boolean }) {
  // Faded edge: dim the tube (mirror pre-branch SingleEdgeTube `faded ? FADE_OPACITY : …`).
  // The traveling bead is suppressed Go-side (a faded edge's bead rows stream Live=0), so no
  // bead-hiding is needed here.
  const tubeTransparent = faded;
  const tubeOpacity = faded ? SHADING_PARAM_NODE_FADE_OPACITY : 1;
  const matKey = faded ? "faded" : "solid";
  const { tubeGeo, haloGeo, arrow } = useMemo(() => {
    const start = new THREE.Vector3(seg.sx, seg.sy, seg.sz);
    const end = new THREE.Vector3(seg.ex, seg.ey, seg.ez);
    const curve = new THREE.LineCurve3(start, end);
    const _tubeGeo = new THREE.TubeGeometry(curve, 1, 1.5, 6, false);
    // Wide concentric halo on the same segment — the pre-branch pick radius (5).
    const _haloGeo = new THREE.TubeGeometry(curve, 1, EDGE_HALO_RADIUS, 6, false);
    const dir = end.clone().sub(start);
    let _arrow: { center: THREE.Vector3; q: THREE.Quaternion } | null = null;
    if (dir.length() >= DIRECTION_ZERO_EPS) {
      dir.normalize();
      _arrow = buildArrow(end, dir, ARROWHEAD_LENGTH);
    }
    return { tubeGeo: _tubeGeo, haloGeo: _haloGeo, arrow: _arrow };
  }, [seg.sx, seg.sy, seg.sz, seg.ex, seg.ey, seg.ez]);

  // R3F does not auto-dispose an imperatively-passed geometry={...}; dispose on rebuild/unmount.
  useEffect(() => () => { tubeGeo.dispose(); haloGeo.dispose(); }, [tubeGeo, haloGeo]);

  return (
    <>
      <mesh geometry={tubeGeo} raycast={() => null} frustumCulled={false}>
        <meshStandardMaterial
          key={matKey}
          color={SHADING_PARAM_TUBE_COLOR}
          emissive={TUBE_EMISSIVE_COLOR}
          emissiveIntensity={SHADING_PARAM_TUBE_EMISSIVE_INTENSITY}
          transparent={tubeTransparent}
          opacity={tubeOpacity}
        />
      </mesh>
      {/* Selection halo doubles as the wide pick target (pre-branch SingleEdgeTube). Always
          mounted so the raycaster can hit anywhere within the halo radius; painted only when
          selected (opacity 0 otherwise — an opacity-0 mesh is still raycast-hittable). */}
      <mesh geometry={haloGeo} userData={{ [BUFFER_EDGE_TAG]: row }} frustumCulled={false}>
        <meshBasicMaterial
          color={EDGE_HALO_COLOR}
          transparent
          opacity={selected ? EDGE_HALO_SELECTED_OPACITY : 0}
          side={THREE.DoubleSide}
          depthWrite={false}
        />
      </mesh>
      {arrow && (
        <mesh
          position={[arrow.center.x, arrow.center.y, arrow.center.z]}
          quaternion={[arrow.q.x, arrow.q.y, arrow.q.z, arrow.q.w]}
          raycast={() => null}
          frustumCulled={false}
        >
          <coneGeometry args={[ARROWHEAD_RADIUS, ARROWHEAD_LENGTH, 16]} />
          <meshStandardMaterial
            key={matKey}
            color={SHADING_PARAM_TUBE_COLOR}
            emissive={TUBE_EMISSIVE_COLOR}
            emissiveIntensity={SHADING_PARAM_TUBE_EMISSIVE_INTENSITY}
            transparent={tubeTransparent}
            opacity={tubeOpacity}
          />
        </mesh>
      )}
    </>
  );
}

function sameSegs(a: EdgeSeg[], b: EdgeSeg[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    const x = a[i]!;
    const y = b[i]!;
    if (
      x.sx !== y.sx || x.sy !== y.sy || x.sz !== y.sz ||
      x.ex !== y.ex || x.ey !== y.ey || x.ez !== y.ez
    ) {
      return false;
    }
  }
  return true;
}

function sameFaded(a: boolean[], b: boolean[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

export function EdgeTubes({ capacity }: { capacity: number }) {
  const [segs, setSegs] = useState<EdgeSeg[]>([]);
  // The Go-selected edge's buffer row (-1 = none). Tracked separately from the segment set
  // so a selection change (which does NOT move any endpoint) toggles the halo without
  // rebuilding the tube geometries. Go OWNS the selection (Edge block Selected column).
  const [selRow, setSelRow] = useState(-1);
  // Faded edge rows (Go-owned fade fixpoint, Edge Faded column). Tracked separately from the
  // segment set — a fade toggle does NOT move any endpoint, so it dims the tube without
  // rebuilding geometry (mirrors selRow).
  const [fadedRows, setFadedRows] = useState<boolean[]>([]);
  const fadedPrevRef = useRef<boolean[]>([]);
  const prevRef = useRef<{ segs: EdgeSeg[] }>({ segs: [] });

  useFrame(() => {
    const snap = getLatestSnapshot();
    if (!snap) return;
    const decoded = decodeSnapshot(snap);
    if (!decoded) return;
    const { edgeCount, edgeView } = decoded;

    const n = Math.min(edgeCount, capacity);
    const next: EdgeSeg[] = new Array<EdgeSeg>(n);
    const fadedNext: boolean[] = new Array<boolean>(n);
    let sel = -1;
    for (let i = 0; i < n; i++) {
      const s: EdgeSeg = {
        sx: readEdgeSX(edgeView, i), sy: readEdgeSY(edgeView, i), sz: readEdgeSZ(edgeView, i),
        ex: readEdgeEX(edgeView, i), ey: readEdgeEY(edgeView, i), ez: readEdgeEZ(edgeView, i),
      };
      next[i] = s;
      if (sel < 0 && readEdgeSelected(edgeView, i)) sel = i;
      const f = !!readEdgeFaded(edgeView, i);
      fadedNext[i] = f;
    }
    // Rebuild the segment set (and thus the tube geometries) only when something moved —
    // not every frame.
    if (!sameSegs(prevRef.current.segs, next)) {
      prevRef.current = { segs: next };
      setSegs(next);
    }
    // Selection toggles cheaply (no geometry rebuild) — update only when the row changes.
    if (sel !== selRow) setSelRow(sel);
    // Fade toggles cheaply too (opacity only, no geometry rebuild).
    if (!sameFaded(fadedPrevRef.current, fadedNext)) {
      fadedPrevRef.current = fadedNext;
      setFadedRows(fadedNext);
    }
  });

  return (
    <>
      {segs.map((s, i) => (
        <React.Fragment key={`edge-row-${i}`}>
          <EdgeTube seg={s} row={i} selected={i === selRow} faded={!!fadedRows[i]} />
        </React.Fragment>
      ))}
    </>
  );
}
