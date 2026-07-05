// buffer-scene-shared.ts — constants, types, and the nodeRowColors helper shared by the
// buffer-scene.tsx sub-components (BeadInstances, NodeInstances, PortInstances,
// SelectionHighlight/HoverHighlight, SphereRings, InteriorBeadInstances, EdgeTube,
// BufferLabelProjector). Split out of buffer-scene.tsx so those sibling files have a single
// source of truth for the pick-tags, sizing/epsilon constants, and hover/highlight colors
// instead of each keeping its own copy. Pure constants/types — no React, no state.

import { readNodeKindId } from "../../schema/buffer-layout";
import { NODE_DEFS_ARRAY } from "../../schema/node-defs";

/** Projected label position for one buffer node row. `row` is the node's buffer node-row
 *  index (identity); `label` is its human label decoded from the buffer's label section. */
export interface BufferLabelPos { row: number; label: string; px: number; py: number; cx: number; cy: number; }

// userData tag marking the NodeInstances body InstancedMesh as the pickable node
// target under the new system. RaycasterHelper (scene-content.tsx) sees this tag on a
// hit and resolves hit.instanceId → node id via the buffer-nav id table, since the
// buffer-rendered nodes carry no per-node userData.nodeId the old raycast path relies on.
export const BUFFER_NODE_TAG = "bufferNode";
// userData tag marking the PortInstances InstancedMesh as the pickable PORT target under the
// new system. On a hit, RaycasterHelper (scene-content.tsx) reads intersection.instanceId —
// which IS the buffer PORT-ROW index (PortInstances draws ports in buffer row order) — and
// forwards that numeric row to Go, which resolves it back to a (node, port). No port-name
// string is rendered or sent.
export const BUFFER_PORT_TAG = "bufferPort";
// userData tag marking the NodeInstances border-ring InstancedMesh as the pickable TORUS
// target (a `port ∈ torus` lock is captured by picking a port then this ring). Instance i
// IS the node row (same loop that draws the body mesh), so a hit's instanceId resolves to
// the owning node id exactly like BUFFER_NODE_TAG.
export const BUFFER_RING_TAG = "bufferRing";
// userData key marking a per-edge wide pick-halo mesh (EdgeTube.tsx) as the pickable
// EDGE target under the new system. Unlike the node/port tags (a boolean, resolved via the
// InstancedMesh instanceId), edges are individual meshes, so this key HOLDS the numeric buffer
// EDGE-ROW index directly. On a hit, RaycasterHelper (scene-content.tsx pickBufferEdge) reads
// userData[BUFFER_EDGE_TAG] as the edge row and forwards it to Go, which resolves the row back to
// its edge. No edge-label string is rendered or sent (mirrors the port-row scheme).
export const BUFFER_EDGE_TAG = "bufferEdgeRow";

// ── Sizing constants shared across sub-components ──────────────────────────────
export const NODE_SPHERE_RADIUS = 12;
// Port hit-sphere radius (world units): the small grabbable ball drawn at each port. Matches
// the pre-branch PortSphere (scene-graph.tsx PORT_SPHERE_R).
export const PORT_SPHERE_R = 4;
// Border-ring tube thickness as a fraction of the node radius (mirrors GraphNode's
// resting torusThick = r * 0.08).
export const NODE_RING_TUBE_RATIO = 0.08;
// Invisible pick-proxy tube thickness as a fraction of the node radius. The VISIBLE ring
// (NODE_RING_TUBE_RATIO=0.08) sits flush on the body sphere's surface and is practically
// unhittable by raycast — clicks aimed at it register as hitKind=node instead (confirmed via
// runtime breadcrumbs). This proxy is an invisible torus sharing the ring's per-instance
// transform. It must sit EXACTLY on the visible ring (same tube thickness) so the pick band
// covers the torus and nothing else — at 0.4 the tube spanned 0.6r..1.4r and its projected
// donut covered most of the node face, stealing body clicks (the "band spread over the whole
// node" bug). Matched to NODE_RING_TUBE_RATIO so the band IS the torus.
export const RING_PICK_TUBE_RATIO = NODE_RING_TUBE_RATIO;
// Pointer-hover highlight (pre-branch scene-graph.tsx): the hovered node's border ring turns
// #aaddff and thickens to r*0.14 (HOVER_RING_TUBE_RATIO); a hovered port sphere turns #aaddff
// and grows to 1.3× (PortSphere isHov). Go OWNS hover (the Hovered columns); this is render-only.
export const HOVER_COLOR = "#aaddff";
export const HOVER_RING_TUBE_RATIO = 0.14;
export const PORT_HOVER_COLOR = HOVER_COLOR;
export const PORT_HOVER_SCALE = 1.3;

// ── Epsilon constants (named by use, not reconciled — distinct purposes) ──────
// Below this squared-length, a ring-plane normal vector is treated as degenerate/unset
// and replaced with a default axis rather than normalized (would divide by ~0).
export const NORMAL_DEGENERATE_EPS = 1e-12;
// Below this reach radius, a sphere-ring owner is treated as having no sphere to draw.
export const SPHERE_RING_MIN_RADIUS = 1e-3;
// Below this vector length, a direction is treated as zero (skip orienting off it)
// rather than normalized.
export const DIRECTION_ZERO_EPS = 1e-6;

// Fallback fill/stroke for a node whose kind is unknown or whose sidecar message has
// not arrived yet. Neutral grey — matches GraphNode's own defaults
// (fill "#ffffff"/stroke "#888888" ← node.data fallbacks).
const NODE_DEFAULT_FILL = "#ffffff";
const NODE_DEFAULT_STROKE = "#888888";
// Node-KindId sentinel value meaning "unknown kind" (no entry in NODE_DEFS_ARRAY).
// Must match the Go-side sentinel written into the buffer's KindId column.
const UNKNOWN_KIND = 0xFF;

/**
 * Resolve a node row's fill/stroke from its KindId column in the buffer.
 * Reads KindId (u8) at the given row and indexes NODE_DEFS_ARRAY; falls back to
 * grey when the id is out-of-range (UNKNOWN_KIND sentinel = unknown kind).
 */
export function nodeRowColors(nodeView: DataView, row: number): { fill: string; stroke: string } {
  const kindId = readNodeKindId(nodeView, row);
  const def = kindId === UNKNOWN_KIND ? undefined : NODE_DEFS_ARRAY[kindId];
  return {
    fill: def?.fill ?? NODE_DEFAULT_FILL,
    stroke: def?.stroke ?? NODE_DEFAULT_STROKE,
  };
}
