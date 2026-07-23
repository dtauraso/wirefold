// EdgeTube.tsx — real 3D edge render matching the JSON path's SingleEdgeTube,
// plus the EdgeTubes buffer-poll wrapper.
//
// TIMING CONTRACT (why this file is imperative, not setState-driven):
// NodeInstances/PortInstances update node/port meshes IMPERATIVELY inside their useFrame
// (setMatrixAt + instanceMatrix.needsUpdate), so a moved node/port lands on the SAME frame
// it is decoded. If edge segment coordinates flowed through React state (setSegs ->
// re-render -> useMemo rebuild), the tube+arrow would land ONE FRAME LATER than the ports
// they connect. During a drag that differential shows as the destination arrowhead sliding
// off its port, proportional to drag speed and sign-flipping with lengthen/shorten — a
// render-side lag, not a data bug (the endpoints themselves are read same-tick off the
// Node frame's Port block via SrcPortRow/DstPortRow — the endpoint-tear fix below). So
// per-frame COORDINATES are pushed to each edge slot via an imperative handle
// (EdgeHandle.update), updated in the same useFrame that reads the node/edge streams —
// never through state.
//
// What DOES stay in useState: the mounted SLOT COUNT, the selected row, and the dim flag.
// Those change on edge add/remove and user clicks, never per drag-frame, so a one-frame
// commit latency on them is imperceptible and is not the lag this contract exists to kill.
// Holding them is buffer reflection (count/selection/flags Go owns), not domain authority —
// no segment geometry is cached in state (check-no-webview-state).

import React, {
  useRef, useState, useEffect, forwardRef, useImperativeHandle,
} from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { getViewBlocks } from "./view-blocks";
import { getEdgeStreamAccessor } from "./edge-stream-blocks";
import { getNodeFrame, getLayoutLinks } from "./node-stream-blocks";
import {
  SHADING_PARAM_TUBE_COLOR,
  SHADING_PARAM_TUBE_EMISSIVE,
  SHADING_PARAM_TUBE_EMISSIVE_INTENSITY,
  SHADING_PARAM_LAYOUT_LINK_COLOR,
  SHADING_PARAM_LAYOUT_LINK_EMISSIVE,
  SHADING_PARAM_LAYOUT_LINK_EMISSIVE_INTENSITY,
} from "../../schema/shading-params";
import {
  readLayoutLinkSrcNodeRow, readLayoutLinkDstNodeRow, readLayoutLinkEdgeRow,
  readNodeCX, readNodeCY, readNodeCZ,
  readPortPX, readPortPY, readPortPZ,
  readOverlayOverlaysVis, readOverlayDoubleLinks,
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
// Arrowhead cone dims for the layout-link overlay (slightly larger than the tube arrows).
const DL_ARROWHEAD_LENGTH = 7;
const DL_ARROWHEAD_RADIUS = 3.5;

const TUBE_EMISSIVE_COLOR = new THREE.Color(SHADING_PARAM_TUBE_EMISSIVE);
const LAYOUT_LINK_EMISSIVE_COLOR = new THREE.Color(SHADING_PARAM_LAYOUT_LINK_EMISSIVE);

interface EdgeSeg { sx: number; sy: number; sz: number; ex: number; ey: number; ez: number; }

// Imperative per-slot handle: the parent pushes this slot's current segment every frame,
// bypassing React state so the tube/arrow land the same frame the ports do (see the timing
// contract at the top of this file).
interface EdgeHandle { update(seg: EdgeSeg): void }

function sameSeg(a: EdgeSeg, b: EdgeSeg): boolean {
  return a.sx === b.sx && a.sy === b.sy && a.sz === b.sz
    && a.ex === b.ex && a.ey === b.ey && a.ez === b.ez;
}

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
// remains raycast-hittable. `dimmed` (the layout-link overlay is on) drops opacity to 0.25,
// same as the pre-removal DoubleEdgeOverlay dim.
//
// The SEGMENT is NOT a prop: the parent pushes it every frame via the imperative handle
// (update), which rebuilds the tube/halo TubeGeometry in place and re-transforms the arrow
// mesh, all synchronously inside the parent's useFrame. That is what keeps the edge on the
// same frame as its ports (timing contract, top of file). Only the low-frequency props
// (dimmed/row/selected) flow through React.
const EdgeTube = forwardRef<EdgeHandle, { dimmed: boolean; row: number; selected: boolean }>(
  function EdgeTube({ dimmed, row, selected }, ref) {
    const tubeTransparent = dimmed;
    const tubeOpacity = dimmed ? 0.25 : 1;
    const matKey = dimmed ? "dimmed" : "solid";

    const tubeMeshRef = useRef<THREE.Mesh>(null);
    const haloMeshRef = useRef<THREE.Mesh>(null);
    const arrowMeshRef = useRef<THREE.Mesh>(null);
    const lastSeg = useRef<EdgeSeg | null>(null);
    // The two TubeGeometries this slot currently owns, so update() can dispose the previous
    // pair before replacing them (R3F does not auto-dispose an imperatively-assigned geometry).
    const geoRef = useRef<{ tube: THREE.TubeGeometry; halo: THREE.TubeGeometry } | null>(null);

    useImperativeHandle(ref, () => ({
      update(seg: EdgeSeg) {
        // Skip the rebuild when this slot's endpoints did not move — same gate the old
        // sameSegs check gave, now per-slot. Selection/dim are props, handled by React.
        if (lastSeg.current && sameSeg(lastSeg.current, seg)) return;
        lastSeg.current = seg;

        const start = new THREE.Vector3(seg.sx, seg.sy, seg.sz);
        const end = new THREE.Vector3(seg.ex, seg.ey, seg.ez);
        const curve = new THREE.LineCurve3(start, end);
        const tubeGeo = new THREE.TubeGeometry(curve, 1, 1.5, 6, false);
        // Wide concentric halo on the same segment — the pre-branch pick radius (5).
        const haloGeo = new THREE.TubeGeometry(curve, 1, EDGE_HALO_RADIUS, 6, false);
        if (geoRef.current) { geoRef.current.tube.dispose(); geoRef.current.halo.dispose(); }
        geoRef.current = { tube: tubeGeo, halo: haloGeo };
        if (tubeMeshRef.current) tubeMeshRef.current.geometry = tubeGeo;
        if (haloMeshRef.current) haloMeshRef.current.geometry = haloGeo;

        const arrow = arrowMeshRef.current;
        if (arrow) {
          const dir = end.clone().sub(start);
          if (dir.length() >= DIRECTION_ZERO_EPS) {
            dir.normalize();
            const { center, q } = buildArrow(end, dir, ARROWHEAD_LENGTH);
            arrow.position.set(center.x, center.y, center.z);
            arrow.quaternion.set(q.x, q.y, q.z, q.w);
            arrow.visible = true;
          } else {
            arrow.visible = false;
          }
        }
      },
    }), []);

    // Dispose this slot's geometries on unmount (the last pair update() assigned).
    useEffect(() => () => {
      if (geoRef.current) { geoRef.current.tube.dispose(); geoRef.current.halo.dispose(); }
    }, []);

    return (
      <>
        <mesh ref={tubeMeshRef} raycast={() => null} frustumCulled={false}>
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
        <mesh ref={haloMeshRef} userData={{ [BUFFER_EDGE_TAG]: row }} frustumCulled={false}>
          <meshBasicMaterial
            color={EDGE_HALO_COLOR}
            transparent
            opacity={selected ? EDGE_HALO_SELECTED_OPACITY : 0}
            side={THREE.DoubleSide}
            depthWrite={false}
          />
        </mesh>
        {/* Arrow transform is pushed imperatively by update(); starts hidden until the first
            update populates its position/quaternion (avoids a one-frame arrow at the origin). */}
        <mesh ref={arrowMeshRef} raycast={() => null} frustumCulled={false} visible={false}>
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
      </>
    );
  },
);

// One layout-link pair's cyan bidirectional overlay: thin tube (radius 1.0) + an
// outward-pointing arrowhead at each end. Mirrors the pre-removal DoubleEdgeOverlay. The
// segment endpoints are the connecting bead edge's own port-anchored SX..EZ (Edge block,
// resolved via this pair's LayoutLink EdgeRow) — the same points the bead wire itself uses,
// so the overlay terminates at the PORTS, not the node centers, and stays attached under a
// drag (the Edge block is re-emitted on every node/port move). `viaEdge=false` means this
// pair had no bead edge to ride along (LayoutLink EdgeRow === -1); the caller falls back to
// node centers, and this is rendered visibly dimmer so a center-anchored fallback segment
// never looks identical to a real port-anchored one.
//
// Same timing contract as EdgeTube: the segment is pushed imperatively (update), not a prop,
// so a link overlay tracks its dragged endpoints on the same frame as the ports it rides.
const LayoutLinkOverlay = forwardRef<EdgeHandle, { viaEdge: boolean }>(
  function LayoutLinkOverlay({ viaEdge }, ref) {
    const lineMeshRef = useRef<THREE.Mesh>(null);
    const arrowStartRef = useRef<THREE.Mesh>(null);
    const arrowEndRef = useRef<THREE.Mesh>(null);
    const lastSeg = useRef<EdgeSeg | null>(null);
    const geoRef = useRef<THREE.TubeGeometry | null>(null);

    useImperativeHandle(ref, () => ({
      update(seg: EdgeSeg) {
        if (lastSeg.current && sameSeg(lastSeg.current, seg)) return;
        lastSeg.current = seg;

        const start = new THREE.Vector3(seg.sx, seg.sy, seg.sz);
        const end = new THREE.Vector3(seg.ex, seg.ey, seg.ez);
        const curve = new THREE.LineCurve3(start, end);
        const lineGeo = new THREE.TubeGeometry(curve, 1, 1.0, 6, false);
        if (geoRef.current) geoRef.current.dispose();
        geoRef.current = lineGeo;
        if (lineMeshRef.current) lineMeshRef.current.geometry = lineGeo;

        const dir = end.clone().sub(start);
        const as = arrowStartRef.current;
        const ae = arrowEndRef.current;
        if (dir.length() >= DIRECTION_ZERO_EPS) {
          const dirNorm = dir.clone().normalize();
          if (as) {
            const { center, q } = buildArrow(start, dirNorm.clone().negate(), DL_ARROWHEAD_LENGTH);
            as.position.set(center.x, center.y, center.z);
            as.quaternion.set(q.x, q.y, q.z, q.w);
            as.visible = true;
          }
          if (ae) {
            const { center, q } = buildArrow(end, dirNorm, DL_ARROWHEAD_LENGTH);
            ae.position.set(center.x, center.y, center.z);
            ae.quaternion.set(q.x, q.y, q.z, q.w);
            ae.visible = true;
          }
        } else {
          if (as) as.visible = false;
          if (ae) ae.visible = false;
        }
      },
    }), []);

    useEffect(() => () => { if (geoRef.current) geoRef.current.dispose(); }, []);

    const coneMesh = (r: React.Ref<THREE.Mesh>) => (
      <mesh ref={r} raycast={() => null} frustumCulled={false} visible={false}>
        <coneGeometry args={[DL_ARROWHEAD_RADIUS, DL_ARROWHEAD_LENGTH, 16]} />
        <meshStandardMaterial
          color={SHADING_PARAM_LAYOUT_LINK_COLOR}
          emissive={LAYOUT_LINK_EMISSIVE_COLOR}
          emissiveIntensity={SHADING_PARAM_LAYOUT_LINK_EMISSIVE_INTENSITY}
        />
      </mesh>
    );

    return (
      <>
        <mesh ref={lineMeshRef} raycast={() => null} frustumCulled={false}>
          <meshStandardMaterial
            color={SHADING_PARAM_LAYOUT_LINK_COLOR}
            emissive={LAYOUT_LINK_EMISSIVE_COLOR}
            emissiveIntensity={SHADING_PARAM_LAYOUT_LINK_EMISSIVE_INTENSITY}
            transparent={!viaEdge}
            opacity={viaEdge ? 1 : 0.35}
          />
        </mesh>
        {coneMesh(arrowStartRef)}
        {coneMesh(arrowEndRef)}
      </>
    );
  },
);

export function EdgeTubes({ capacity, layoutLinkCapacity }: { capacity: number; layoutLinkCapacity: number }) {
  // Number of edge slots to MOUNT (not their coordinates). Changes only when edges are
  // added/removed — never during a drag — so its one-frame commit latency is invisible.
  const [edgeCount, setEdgeCount] = useState(0);
  // The Go-selected edge's buffer row (-1 = none). Tracked separately from geometry so a
  // selection change (which moves no endpoint) toggles the halo without touching the tubes.
  const [selRow, setSelRow] = useState(-1);
  const [showDouble, setShowDouble] = useState(false);
  // Mounted layout-link slot count + each slot's viaEdge flag (line color/opacity). Both are
  // low-frequency (a link gains/loses its bead edge, or the overlay toggles) — not per-frame.
  const [linkCount, setLinkCount] = useState(0);
  const [linkViaEdge, setLinkViaEdge] = useState<boolean[]>([]);

  // Imperative handles to every mounted slot — this is the per-frame coordinate channel that
  // replaces the old setSegs/setLinkSegs state (see the timing contract at the top of file).
  const edgeHandles = useRef<(EdgeHandle | null)[]>([]);
  const linkHandles = useRef<(EdgeHandle | null)[]>([]);
  // Scratch reused each frame so viaEdge comparison allocates nothing on the steady path.
  const linkViaScratch = useRef<boolean[]>([]);

  useFrame(() => {
    const blocks = getViewBlocks();
    const decodedNode = getNodeFrame();
    if (!decodedNode || !blocks) return;
    // Every edge's own dedicated stream frame is this edge data's ONLY source (memory/
    // feedback_no_single_writer_bridge.md) — null means no frame has arrived yet.
    const edgeStream = getEdgeStreamAccessor();
    if (!edgeStream) return;
    const bufEdgeCount = edgeStream.edgeCount;
    const srcPortRowAt = (row: number) => edgeStream.srcPortRow(row);
    const dstPortRowAt = (row: number) => edgeStream.dstPortRow(row);
    const selectedAt = (row: number) => edgeStream.selected(row);
    // Layout-link overlay pairs: aggregated from the per-node dedicated streams' own
    // outbound layout-links (see getLayoutLinks' doc comment,
    // memory/feedback_no_single_writer_bridge.md).
    const { layoutLinkCount, layoutLinkView } = getLayoutLinks();
    const { overlayView } = blocks;
    // LayoutLink's SrcNodeRow/DstNodeRow resolve against the NODE frame's Node block — both
    // frames are built from the same Go SnapshotState in the same emitSnapshot call, so they
    // share the same stable node-row order (see frame_tags.go's BufBlockTagNode comment).
    const { nodeView, portView } = decodedNode;
    // The Edge block carries NO endpoint coordinates: SrcPortRow/DstPortRow reference the
    // NODE frame's Port block, the ONLY place the endpoint's world position lives (node-
    // owned) — so a fresh Node frame and this SAME-tick Edge frame can never disagree (the
    // endpoint-tear fix, memory/feedback_no_single_writer_bridge.md option (a)).

    const portEndpoint = (portRow: number): [number, number, number] => {
      if (portRow < 0) return [0, 0, 0];
      return [readPortPX(portView, portRow), readPortPY(portView, portRow), readPortPZ(portView, portRow)];
    };

    const n = Math.min(bufEdgeCount, capacity);
    if (n !== edgeCount) setEdgeCount(n);

    let sel = -1;
    for (let i = 0; i < n; i++) {
      const srcRow = srcPortRowAt(i);
      const dstRow = dstPortRowAt(i);
      const [sx, sy, sz] = portEndpoint(srcRow);
      const [ex, ey, ez] = portEndpoint(dstRow);
      if (sel < 0 && selectedAt(i)) sel = i;
      // Push this edge's current endpoints straight to its slot — no state, so it lands this
      // frame. A slot mounted THIS frame (n just grew) has no handle yet; it gets its first
      // push next frame, an imperceptible one-frame delay on edge APPEARANCE, never on a move.
      edgeHandles.current[i]?.update({ sx, sy, sz, ex, ey, ez });
    }
    if (sel !== selRow) setSelRow(sel);

    // Layout-link overlay: Go-streamed pairs (LayoutLink block). Each pair's endpoints are the
    // connecting bead edge's own port-anchored SX..EZ (Edge block, row = this pair's EdgeRow) —
    // the same points the bead wire uses, so the overlay terminates at the ports and stays
    // attached as a node is dragged. Fallback (EdgeRow === -1): the two nodes' CENTERS from the
    // Node block — an honest degradation, rendered dimmer (viaEdge=false).
    // Both overlay flags (0/1 columns) must be set. Coerce each side explicitly with `> 0`.
    const dbl = readOverlayOverlaysVis(overlayView) > 0 && readOverlayDoubleLinks(overlayView) > 0;
    if (dbl !== showDouble) setShowDouble(dbl);

    // Clamp with the layout-link's OWN capacity, never the edge `capacity`: layout links come
    // from LocalPolars (not the Edge block), so layoutLinkCount is independent of edgeCount and
    // can exceed edgeCap — clamping by edgeCap silently dropped links.
    const linkN = Math.min(layoutLinkCount, layoutLinkCapacity);
    if (linkN !== linkCount) setLinkCount(linkN);

    const via = linkViaScratch.current;
    via.length = linkN;
    for (let i = 0; i < linkN; i++) {
      const edgeRow = readLayoutLinkEdgeRow(layoutLinkView, i);
      let seg: EdgeSeg;
      if (edgeRow >= 0 && edgeRow < bufEdgeCount) {
        const [sx, sy, sz] = portEndpoint(srcPortRowAt(edgeRow));
        const [ex, ey, ez] = portEndpoint(dstPortRowAt(edgeRow));
        seg = { sx, sy, sz, ex, ey, ez };
        via[i] = true;
      } else {
        const srcRow = readLayoutLinkSrcNodeRow(layoutLinkView, i);
        const dstRow = readLayoutLinkDstNodeRow(layoutLinkView, i);
        seg = {
          sx: readNodeCX(nodeView, srcRow), sy: readNodeCY(nodeView, srcRow), sz: readNodeCZ(nodeView, srcRow),
          ex: readNodeCX(nodeView, dstRow), ey: readNodeCY(nodeView, dstRow), ez: readNodeCZ(nodeView, dstRow),
        };
        via[i] = false;
      }
      linkHandles.current[i]?.update(seg);
    }
    // viaEdge drives per-slot material — a prop, so commit it only when the vector changes.
    const viaChanged = linkViaEdge.length !== linkN || via.some((v, i) => v !== linkViaEdge[i]);
    if (viaChanged) setLinkViaEdge(via.slice(0, linkN));
  });

  return (
    <>
      {Array.from({ length: edgeCount }, (_, i) => (
        <EdgeTube
          key={`edge-row-${i}`}
          ref={(h) => { edgeHandles.current[i] = h; }}
          dimmed={showDouble}
          row={i}
          selected={i === selRow}
        />
      ))}
      {showDouble && Array.from({ length: linkCount }, (_, i) => (
        <LayoutLinkOverlay
          key={`layout-link-row-${i}`}
          ref={(h) => { linkHandles.current[i] = h; }}
          viaEdge={!!linkViaEdge[i]}
        />
      ))}
    </>
  );
}
