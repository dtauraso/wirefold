// scene-camera.tsx — CameraFitter, CameraRefBridge, LabelProjector, CameraSettleDetector, PolarCameraRestorer.
import React, { useEffect, useRef } from "react";
import { useThree, useFrame } from "@react-three/fiber";
import * as THREE from "three";
import type { RFNode, NodeData } from "../types";
import type { Camera3D } from "../state/viewer/types";
import type { PolarCamera } from "./camera-store";
import { sendViewpointSet } from "./viewpoint-bridge";
import { useThreeStore } from "./store";
import { useNodeGeometryStore, getNodeGeometry } from "./node-geometry";
import { boundingBox, nodeWorldPos, nodeTopWorldPos, ndcToPixel, fitDistance } from "./geometry-helpers";
import { postLog } from "../log/post";

// ---------------------------------------------------------------------------
// Camera fitter: perspective camera framed head-on to show graph flat at z=0.
// Fits once, but waits until nodes are actually non-empty (not just on mount).
// ---------------------------------------------------------------------------

export function CameraFitter({ nodes, hasRestoredCamera }: { nodes: RFNode<NodeData>[]; hasRestoredCamera?: boolean }) {
  const { camera, size } = useThree();
  const loadEpoch = useThreeStore((s) => s.loadEpoch);
  // Subscribe to the Go node-geometry store so the effect re-runs when Go's
  // node-geometry trace stream populates centers after load (it lands ~1 frame
  // AFTER store:load). The first fit must wait for this, or it frames the
  // pre-emit fallback coords instead of the real Go-streamed centers.
  const geoms = useNodeGeometryStore((s) => s.geoms);
  // Track the last epoch we actually fitted for. The effect may run several
  // times as size/nodes/geometry settle on first load; we fit only the first
  // time everything is ready for a given epoch, and never again until the next load.
  const lastFittedEpoch = useRef<number>(-1);
  useEffect(() => {
    // Already fitted for this load epoch (e.g. a node drag re-triggered us).
    if (lastFittedEpoch.current === loadEpoch) return;
    // Skip if no content or canvas not yet sized — re-runs when either settles.
    if (nodes.length === 0) return;
    if (size.width === 0 || size.height === 0) return;
    // Skip auto-fit when the saved camera is being restored.
    if (hasRestoredCamera) return;
    // Wait until Go geometry has landed for EVERY current node, so we frame the
    // ACTUAL rendered centers (g.center) and not the pre-emit fallback. Re-runs
    // when `geoms` gains the last node's center. (geoms read so the dep is live.)
    void geoms;
    if (!nodes.every((n) => getNodeGeometry(n.id) !== undefined)) return;
    lastFittedEpoch.current = loadEpoch;
    const persp = camera as THREE.PerspectiveCamera;
    const PAD = 80;
    // boundingBox reads nodeWorldPos (Go g.center, already Three y-up world
    // coords) — the SAME source GraphNode renders the node group from. So the
    // center is used DIRECTLY with no y-negation: negating here (the old bug)
    // framed the mirror-image y and pushed the nodes off-screen until manual Fit.
    // This now matches HomeButton's math (camera-ui.tsx), which works.
    const { minX, maxX, minY, maxY } = boundingBox(nodes);
    const gw = (maxX - minX) + 2 * PAD;
    const gh = (maxY - minY) + 2 * PAD;
    const cx = (minX + maxX) / 2;
    const cy = (minY + maxY) / 2; // nodeWorldPos is already y-up world — no negate
    postLog("lifecycle", { phase: "camera-fit", nodeCount: nodes.length, cx, cy, minX, maxX, minY, maxY });
    const aspect = size.width / size.height;
    // Choose z so the graph fills the view.
    const z = fitDistance(persp.fov, aspect, gw, gh) + 50;
    persp.position.set(cx, cy, z);
    persp.up.set(0, 1, 0);
    persp.lookAt(cx, cy, 0);
    persp.near = 0.1;
    persp.far = 20000;
    persp.updateProjectionMatrix();
  // Re-run when size/nodes/geometry settle; the lastFittedEpoch ref makes it fit
  // once per load epoch and never on drag (same epoch).
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loadEpoch, size.width, size.height, nodes.length, geoms]);
  return null;
}

// ---------------------------------------------------------------------------
// CameraRef bridge: exposes the live camera to React state outside the Canvas.
// ---------------------------------------------------------------------------

export function CameraRefBridge({
  cameraRef,
  initialCamera3d,
}: {
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>;
  initialCamera3d?: Camera3D;
}) {
  const { camera } = useThree();
  const restoredRef = useRef(false);
  useEffect(() => {
    const cam = camera as THREE.PerspectiveCamera;
    cameraRef.current = cam; // always keep the ref current
    // Restore the saved camera ONCE. Without this guard, every commitCamera during a
    // drag mutates viewerState.camera3d → this effect re-runs → it re-applies the saved
    // pose on top of the live rotation each frame, FIGHTING the gesture (jitter) and
    // partly undoing it (slow). We still re-run (restoredRef stays false) until a real
    // saved pose is available, so the async load() case is covered; once applied we stop.
    if (restoredRef.current) return;
    if (initialCamera3d) {
      cam.position.set(...initialCamera3d.position);
      if (initialCamera3d.quaternion) {
        const [qx, qy, qz, qw] = initialCamera3d.quaternion;
        cam.quaternion.set(qx, qy, qz, qw);
        cam.updateMatrixWorld(true);
        restoredRef.current = true; // restored a real pose → ignore later commits
        return;
      }
    }
    // No saved quaternion yet: default square-on orientation (don't mark restored, so a
    // later load can still apply the saved pose).
    cam.up.set(0, 1, 0);
    cam.lookAt(cam.position.x, cam.position.y, 0);
    cam.updateMatrixWorld(true);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [camera, cameraRef, initialCamera3d]);
  return null;
}

// ---------------------------------------------------------------------------
// LabelProjector: runs every N frames (throttled) to project node world
// positions to screen. Projects only the visible set (hovered ∪ selected ∪
// nearest-N) plus a full refresh every 6 frames for smooth camera motion.
// Updates positions via a ref callback (no React state → no re-render cost).
// ---------------------------------------------------------------------------

// Module-scope scratch vectors reused across the projection loop so the throttled
// useFrame allocates no Vector3 per node per frame. Kept distinct (never aliased):
// _topScratch holds the node top, _centerScratch the center, and neither is read
// after the other is overwritten within one node's iteration.
const _topScratch = new THREE.Vector3();
const _centerScratch = new THREE.Vector3();

export function LabelProjector({
  nodes,
  onPositions,
}: {
  nodes: RFNode<NodeData>[];
  onPositions: (positions: { id: string; px: number; py: number; cx: number; cy: number }[]) => void;
}) {
  const { camera, size } = useThree();
  const frameCountRef = useRef(0);

  useFrame(() => {
    frameCountRef.current++;
    // Project every 2 frames (~30fps) for label smoothness during camera motion.
    // This is much cheaper than every frame while still tracking well visually.
    if (frameCountRef.current % 2 !== 0) return;
    const positions = nodes.map((n) => {
      const top = nodeTopWorldPos(n, _topScratch);
      top.project(camera);
      const topPx = ndcToPixel(top.x, top.y, size);
      const center = nodeWorldPos(n, _centerScratch);
      center.project(camera);
      const centerPx = ndcToPixel(center.x, center.y, size);
      return { id: n.id, px: topPx.px, py: topPx.py, cx: centerPx.px, cy: centerPx.py };
    });
    onPositions(positions);
  });

  return null;
}

// ---------------------------------------------------------------------------
// CameraSettleDetector: fires onSettle ~250ms after the camera stops moving.
// Compares camera matrix each frame; on change resets a debounce timer.
// ---------------------------------------------------------------------------

export function CameraSettleDetector({
  onSettle,
}: {
  onSettle: () => void;
}) {
  const { camera } = useThree();
  const lastElements = useRef<Float32Array>(new Float32Array(16));
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useFrame(() => {
    // Compare matrix elements with epsilon matching toFixed(2) rounding (~5e-3).
    camera.updateMatrixWorld();
    const els = camera.matrixWorld.elements;
    const EPSILON = 5e-3;
    let changed = false;
    for (let i = 0; i < 16; i++) {
      if (Math.abs(els[i] - lastElements.current[i]) > EPSILON) { changed = true; break; }
    }
    if (changed) {
      lastElements.current.set(els);
      if (timerRef.current !== null) clearTimeout(timerRef.current);
      timerRef.current = setTimeout(() => {
        timerRef.current = null;
        onSettle();
      }, 250);
    }
  });

  // Clear any pending settle timer on unmount so it can't fire onSettle after the
  // component is gone.
  useEffect(() => () => {
    if (timerRef.current !== null) clearTimeout(timerRef.current);
  }, []);

  return null;
}

// ---------------------------------------------------------------------------
// PolarCameraRestorer: on mount, sends the saved polar camera to Go once.
// Go echoes it back as a "camera" trace event, which CameraFromStore applies.
// Guarded by a ref so it fires exactly once even if the prop reference changes.
// ---------------------------------------------------------------------------

export function PolarCameraRestorer({ initialCameraPolar }: { initialCameraPolar: PolarCamera }) {
  const sentRef = useRef(false);
  useEffect(() => {
    if (sentRef.current) return;
    sentRef.current = true;
    sendViewpointSet(initialCameraPolar.pivot, initialCameraPolar.r, initialCameraPolar.pos, initialCameraPolar.up);
  // Run once on mount — prop is the initial value and won't change.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return null;
}

