// Pure helpers for spec-to-flow conversion: wire-prop picking, fold node
// building, and note node building.

import type { Edge as RFEdge, Node as RFNode } from "reactflow";
import { KIND_COLORS, type Node as SpecNode, type Spec } from "../../../schema";
import type { Fold, ViewerState } from "../../state/viewer/types";
import { COLLAPSED_FOLD_W, COLLAPSED_FOLD_H, expandedBounds } from "./_bounds";
import type { NodeData, EdgeData } from "../types";
import { WIRE_PROPS } from "../../../schema/wire-defs";

/** Iterate WIRE_PROPS registry; skip `kind` (handled explicitly at call sites). */
export function pickWireProps(e: Record<string, unknown>): Partial<EdgeData> {
  const out: Partial<EdgeData> = {};
  for (const key of Object.keys(WIRE_PROPS)) {
    if (key !== "kind" && e[key] !== undefined) (out as Record<string, unknown>)[key] = e[key];
  }
  return out;
}

export function buildFoldNodes(
  folds: Fold[],
  nodeById: Map<string, SpecNode>,
  vs: Pick<ViewerState, "nodes" | "edges">,
): RFNode[] {
  return folds.map((f) => {
    if (f.collapsed) {
      return {
        id: f.id,
        type: "fold",
        position: { x: f.position[0], y: f.position[1] },
        data: {
          label: f.label,
          collapsed: true,
          memberCount: f.memberIds.length,
          memberIds: f.memberIds,
          width: COLLAPSED_FOLD_W,
          height: COLLAPSED_FOLD_H,
        },
        zIndex: 0,
      };
    }
    const b = expandedBounds(f, nodeById, vs.nodes ?? {});
    return {
      id: f.id,
      type: "fold",
      position: { x: b.x, y: b.y },
      data: {
        label: f.label,
        collapsed: false,
        memberCount: f.memberIds.length,
        memberIds: f.memberIds,
        width: b.w,
        height: b.h,
      },
      // Expanded folds render *behind* members. Don't set zIndex: -1 — that
      // drops the wrapper (label tab included) below the canvas background.
      // Array order is enough: fold nodes are emitted first below.
      // Not draggable because the frame's position is recomputed from member
      // bounds on every render.
      draggable: false,
    };
  });
}

const NOTE_DEFAULT_W = 160;
const NOTE_DEFAULT_H = 60;

export function buildNoteNodes(spec: Spec): RFNode[] {
  return (spec.notes ?? []).map((nt, i) => ({
    id: `__note-${i}`,
    type: "note",
    position: { x: nt.x, y: nt.y },
    data: {
      text: nt.text,
      width: nt.width ?? NOTE_DEFAULT_W,
      height: nt.height ?? NOTE_DEFAULT_H,
      hasWidth: nt.width !== undefined,
      hasHeight: nt.height !== undefined,
    },
    draggable: false,
    selectable: false,
  }));
}

export function buildEdges(
  spec: Spec,
  collapsedFoldFor: Map<string, string>,
  vs: Pick<ViewerState, "nodes" | "edges">,
): RFEdge<EdgeData>[] {
  const edges: RFEdge<EdgeData>[] = [];
  for (const e of spec.edges) {
    const srcFold = collapsedFoldFor.get(e.source);
    const dstFold = collapsedFoldFor.get(e.target);
    if (srcFold && dstFold && srcFold === dstFold) continue;

    const source = srcFold ?? e.source;
    const target = dstFold ?? e.target;
    // When an endpoint is rerouted to a fold placeholder, the original port
    // handle no longer applies. Drop sourceHandle/targetHandle on rerouted
    // endpoints so RF falls back to the placeholder's default handles.
    const sourceHandle = srcFold ? undefined : e.sourceHandle;
    const targetHandle = dstFold ? undefined : e.targetHandle;
    const ev = vs.edges?.[e.id];
    edges.push({
      id: e.id,
      source,
      target,
      sourceHandle,
      targetHandle,
      type: "substrate",
      // label rendered via EdgeLabelRenderer (data.label), not RF's foreignObject.
      style: { stroke: (KIND_COLORS as Record<string, string>)[e.kind] ?? "#888", strokeWidth: 1.5 },
      data: {
        kind: e.kind,
        sourceHandle: e.sourceHandle,
        targetHandle: e.targetHandle,
        route: ev?.route ?? (e.data as Record<string, unknown> | undefined)?.route as string | undefined,
        ...pickWireProps(e as unknown as Record<string, unknown>),
        value: (e.data as Record<string, unknown> | undefined)?.value,
        edgeData: e.data, // verbatim: flow-to-spec round-trips backpressure/delay config
      },
    });
  }
  return edges;
}
