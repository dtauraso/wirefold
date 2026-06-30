// interaction-controls.ts — useInteractionControls hook and related types.
// Handles CLICK, NODE-DRAG, and SCROLL (dolly/pan) detection.
// Single-pointer empty-space drag = free-roll great-circle rotation.

import { useRef, useCallback } from "react";
import * as THREE from "three";
import type { RFNode, RFEdge, NodeData, EdgeData } from "../types";
import type { MoveEntry } from "../../messages";
import { contentSphere } from "./geometry-helpers";
import { vscode } from "../vscode-api";
import type { InteractionCtx } from "./interaction-handlers";
import { handlePointerDown, handlePointerMove, handlePointerUp, handleWheelNative } from "./interaction-handlers";

/**
 * The diagram's WORLD-FIXED content sphere: center = bounding-box center of the node
 * world positions, radius = farthest node from that center (+10% margin). This is the
 * arcball — fixed in world space, so it zooms WITH the diagram (both grow as you dolly
 * in) instead of staying screen-size. Exported shape used by the visible sphere too.
 * Delegates to geometry-helpers.contentSphere (the single source).
 */
export function computeContentSphere(nodes: RFNode<NodeData>[]): { center: THREE.Vector3; radius: number } {
  return contentSphere(nodes);
}

// ---------------------------------------------------------------------------
// ControlState
// ---------------------------------------------------------------------------

export interface ControlState {
  // Interaction phase
  phase: "idle" | "pending" | "dragging" | "rotating" | "handhold-rotating" | "wiring" | "port-move";
  // Pointer-down snapshot
  downX: number;
  downY: number;
  downTime: number;
  // Previous pointer position (updated each frame during drags, including rotation)
  prevX: number;
  prevY: number;
  // True when the pointer-down hit empty space (not a node or edge); gates free-roll rotation.
  emptyDown: boolean;
  // True when the pointer-down hit a handhold; gates the constrained locked-disk rotation.
  handholdDown: boolean;
  // True when the pointer-down was a SECONDARY (button 2) press — a two-finger trackpad
  // tap. A secondary press is always a deliberate tap-to-select: it never converts to a
  // drag/rotate, and on release it selects with a lenient slop and no tight time gate
  // (the first two-finger tap, fingers settling, is often slow/sloppy).
  secondaryDown: boolean;
  // Locked rotation disk normal (axis) for "handhold-rotating", frozen from the first two
  // cursor points and reused for the whole gesture. null until the first move locks it.
  rotAxis: THREE.Vector3 | null;
  // Rotation gesture pivot: content bounding-box center, fixed for the gesture.
  rotPivot: THREE.Vector3;
  // Sphere screen-center (pixels, client coords) and scale, frozen at pointer-down.
  // Valid for the whole gesture: C stays at the same pixel as the camera orbits about it.
  rotCx: number;
  rotCy: number;
  rotPxPerRad: number; // pixels per radian for screenToPolar
}

// ---------------------------------------------------------------------------
// PickOptions (shared with scene-content)
// ---------------------------------------------------------------------------

export interface PickOptions {
  excludeId?: string;
  nodesOnly?: boolean;
  ringOnly?: boolean;
  portOnly?: boolean;
  handholdOnly?: boolean;
}

// ---------------------------------------------------------------------------
// makeRafThrottle — shared rAF coalescing pattern
// ---------------------------------------------------------------------------

/**
 * Returns a `schedule(payload)` function that coalesces calls to `apply` so
 * only the latest payload is delivered once per animation frame (latest-wins).
 * The pending value and scheduled flag live in the caller-supplied refs so they
 * remain accessible to external code (e.g. ctx fields read by interaction-handlers).
 */
function makeRafThrottle<T>(
  pendingRef: React.MutableRefObject<T | null>,
  scheduledRef: React.MutableRefObject<boolean>,
  apply: (payload: T) => void,
): (payload: T) => void {
  return (payload: T) => {
    pendingRef.current = payload;
    if (!scheduledRef.current) {
      scheduledRef.current = true;
      requestAnimationFrame(() => {
        scheduledRef.current = false;
        const p = pendingRef.current;
        if (p !== null) {
          apply(p);
          pendingRef.current = null;
        }
      });
    }
  };
}

// ---------------------------------------------------------------------------
// useInteractionControls
// ---------------------------------------------------------------------------

export function useInteractionControls(
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>,
  canvasSize: { w: number; h: number },
  pickRequest: React.MutableRefObject<((ndcX: number, ndcY: number, opts?: PickOptions) => string | null) | null>,
  onSelect: (id: string | null, ownSphere?: boolean) => void,
  nodesRef: React.MutableRefObject<RFNode<NodeData>[]>,
  storeCreateEdge: (sourceId: string, sourceHandle: string | null, targetId: string, targetHandle: string | null) => void,
  selectedIdRef: React.MutableRefObject<string | null>,
  edgesRef: React.MutableRefObject<RFEdge<EdgeData>[]>,
  targetRef: React.MutableRefObject<THREE.Vector3>,
) {
  const state = useRef<ControlState>({
    phase: "idle",
    downX: 0, downY: 0, downTime: 0,
    prevX: 0, prevY: 0,
    emptyDown: false,
    handholdDown: false,
    secondaryDown: false,
    rotAxis: null,
    rotPivot: new THREE.Vector3(),
    rotCx: 0, rotCy: 0, rotPxPerRad: 1,
  });

  // Node-drag state: set when pointer-down lands on a node.
  const nodeDragRef = useRef<{
    nodeId: string;
    startCenter: THREE.Vector3; // node world center at drag start (defines the drag plane)
    lastWorldTarget: THREE.Vector3 | null; // most recent world target sent during the drag
  } | null>(null);

  // Wiring state: set when pointer-down lands on an UNCONNECTED port sphere
  // (drag port→port creates an edge).
  const wiringRef = useRef<{ nodeId: string; portName: string; isInput: boolean } | null>(null);

  // Port-move state: set when pointer-down lands on a CONNECTED port sphere
  // (drag slides the port along its node's ring; the incident edge follows).
  const portMoveRef = useRef<{
    nodeId: string;
    portName: string;
    isInput: boolean;
    nodeCenter: THREE.Vector3;
  } | null>(null);

  // Throttle node-move IPC: one message per animation frame during drag.
  const pendingNodeMove = useRef<{ nodeId: string; x: number; y: number; z: number } | null>(null);
  const rafPending = useRef(false);

  const flushNodeMove = useCallback((nodeId: string, x: number, y: number, z: number) => {
    // Decentralized node-move: mail-sort the move to the moved node + every incident
    // edge (source===moved || target===moved). TS owns the graph and computes the
    // incident edges; Go's per-node/per-edge goroutines own the recompute. Every
    // entry carries the same moved node id + WORLD-SPACE target (x,y,z) — Go snaps it
    // to the nearest lattice cell (TS computes no cell). Keys are node id + edge ids.
    const entry: MoveEntry = { nodeId, x, y, z };
    const entries: Record<string, MoveEntry> = { [nodeId]: entry };
    for (const e of edgesRef.current) {
      if (e.source === nodeId || e.target === nodeId) {
        entries[e.id] = entry;
      }
    }
    vscode.postMessage({ type: "edit", op: "update", entries });
  }, [edgesRef]);

  const _rafNodeMove = useRef(
    makeRafThrottle(pendingNodeMove, rafPending, (p: { nodeId: string; x: number; y: number; z: number }) =>
      flushNodeMove(p.nodeId, p.x, p.y, p.z)),
  ).current;
  const scheduleNodeMove = useCallback(
    (nodeId: string, x: number, y: number, z: number) => _rafNodeMove({ nodeId, x, y, z }),
    [],
  );

  // Edge ids incident on a specific port (output → source/sourceHandle, input →
  // target/targetHandle). Used both to decide connected-vs-unconnected and to build
  // the port-anchor `keys` fan-out.
  const incidentEdgeIds = useCallback(
    (nodeId: string, portName: string, isInput: boolean): string[] => {
      const ids: string[] = [];
      for (const e of edgesRef.current) {
        const hit = isInput
          ? e.target === nodeId && (e.targetHandle ?? null) === portName
          : e.source === nodeId && (e.sourceHandle ?? null) === portName;
        if (hit) ids.push(e.id);
      }
      return ids;
    },
    [edgesRef],
  );

  // Throttle port-anchor IPC: one message per animation frame during a port drag.
  const pendingAnchor = useRef<{
    nodeId: string;
    portName: string;
    isInput: boolean;
    anchor: { x: number; y: number; z: number };
  } | null>(null);
  const anchorRafPending = useRef(false);

  const flushPortAnchor = useCallback(
    (p: { nodeId: string; portName: string; isInput: boolean; anchor: { x: number; y: number; z: number } }) => {
      // Decentralized port-anchor: mail-sort to the owning node + each edge incident
      // on this port. Go's per-node/per-edge goroutines own the recompute + re-emit.
      const keys = [p.nodeId, ...incidentEdgeIds(p.nodeId, p.portName, p.isInput)];
      vscode.postMessage({
        type: "edit",
        op: "port-anchor",
        node: p.nodeId,
        port: p.portName,
        isInput: p.isInput,
        anchor: p.anchor,
        keys,
      });
    },
    [incidentEdgeIds],
  );

  const _rafPortAnchor = useRef(
    makeRafThrottle(pendingAnchor, anchorRafPending,
      (p: { nodeId: string; portName: string; isInput: boolean; anchor: { x: number; y: number; z: number } }) =>
        flushPortAnchor(p)),
  ).current;
  const schedulePortAnchor = useCallback(
    (p: { nodeId: string; portName: string; isInput: boolean; anchor: { x: number; y: number; z: number } }) =>
      _rafPortAnchor(p),
    [],
  );


  // ------ controller context ------
  //
  // A single stable context object bundling every ref + stable callback the handler
  // bodies (interaction-handlers.ts) close over. Identity is held stable across renders
  // via a ref; its fields are refreshed each render so the handlers always read the
  // current callbacks. The refs themselves are already stable, so reads/writes of
  // ctx.someRef.current behave exactly as the original inline code did.
  const ctxRef = useRef<InteractionCtx | null>(null);
  // Build the full ctx object once per render; use it for both first-init and refresh so
  // the field list is defined in exactly one place and cannot drift.
  const freshCtx: InteractionCtx = {
    state, nodeDragRef, wiringRef, portMoveRef,
    pendingNodeMove, rafPending, pendingAnchor, anchorRafPending,
    cameraRef, nodesRef, edgesRef, targetRef, pickRequest,
    onSelect, storeCreateEdge, incidentEdgeIds,
    scheduleNodeMove, flushNodeMove, schedulePortAnchor, flushPortAnchor,
  };
  if (ctxRef.current === null) {
    ctxRef.current = freshCtx;
  } else {
    // Merge into the existing object to preserve its identity (the useCallback wrappers
    // below close over ctx which is ctxRef.current; replacing the object would make
    // those stale). Object.assign refreshes every field from the single freshCtx source.
    Object.assign(ctxRef.current, freshCtx);
  }
  const ctx = ctxRef.current;

  // ------ pointer event handlers (thin ctx-threaded wrappers) ------

  const onPointerDown = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => handlePointerDown(ctx, e),
    [ctx],
  );
  const onPointerMove = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => handlePointerMove(ctx, e),
    [ctx],
  );
  const onPointerUp = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => handlePointerUp(ctx, e),
    [ctx],
  );
  // Exposed so ThreeView can attach a non-passive native listener.
  const onWheelNative = useCallback(
    (e: WheelEvent) => handleWheelNative(ctx, e),
    [ctx],
  );

  return { onPointerDown, onPointerMove, onPointerUp, onWheelNative };
}
