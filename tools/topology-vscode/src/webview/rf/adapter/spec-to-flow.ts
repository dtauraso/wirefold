import type { Edge as RFEdge, Node as RFNode } from "reactflow";
import { KIND_COLORS, NODE_TYPES, type Node as SpecNode, type Spec } from "../../../schema";
import type { Fold, ViewerState } from "../../state/viewer/types";
import { COLLAPSED_FOLD_W, COLLAPSED_FOLD_H, expandedBounds } from "./_bounds";
import type { NodeData, EdgeData } from "../types";
import { WIRE_PROPS } from "../../../schema/wire-defs";

/** Iterate WIRE_PROPS registry; skip `kind` (handled explicitly at call sites). */
function pickWireProps(e: Record<string, unknown>): Partial<EdgeData> {
  const out: Partial<EdgeData> = {};
  for (const key of Object.keys(WIRE_PROPS)) {
    if (key !== "kind" && e[key] !== undefined) (out as Record<string, unknown>)[key] = e[key];
  }
  return out;
}

/** Converts a spec kind (PascalCase) to the RF node type name (camelCase). */
export function specKindToRfType(kind: string): string {
  return kind.charAt(0).toLowerCase() + kind.slice(1);
}

// Fold-aware spec→flow conversion. Folds are viewer-only state; they never
// touch the spec (the runtime loader ignores topology.view.json). Edges that cross a
// collapsed fold boundary are re-routed onto the fold placeholder *only in
// the flow*; the underlying spec edge keeps its original endpoints, so on
// expand the original wiring is reinstated without mutation.

export function specToFlow(
  spec: Spec,
  folds: Fold[] = [],
  vs: Pick<ViewerState, "nodes" | "edges"> = {},
  lastSelectionIds: string[] = [],
  dimmed: Set<string> | null = null,
): { nodes: RFNode<NodeData>[]; edges: RFEdge<EdgeData>[] } {
  // Map memberId → containing collapsed fold id. Nested folds are not
  // supported here; if a node appears in multiple collapsed folds, the first
  // wins (the plan flags nested folds as needing manual coordination).
  const collapsedFoldFor = new Map<string, string>();
  for (const f of folds) {
    if (!f.collapsed) continue;
    for (const m of f.memberIds) {
      if (!collapsedFoldFor.has(m)) collapsedFoldFor.set(m, f.id);
    }
  }

  // Map memberId → containing fold id (all folds, collapsed or expanded).
  // First-wins precedence matches collapsedFoldFor above.
  const foldOf = new Map<string, string>();
  for (const f of folds) {
    for (const m of f.memberIds) {
      if (!foldOf.has(m)) foldOf.set(m, f.id);
    }
  }

  const nodeById = new Map<string, SpecNode>();
  for (const n of spec.nodes) nodeById.set(n.id, n);

  const foldNodes: RFNode[] = folds.map((f) => {
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

  const memberNodes: RFNode<NodeData>[] = spec.nodes
    .filter((n) => !collapsedFoldFor.has(n.id))
    .map((n) => {
    const def = NODE_TYPES[n.type];
    const width = def?.width ?? 110;
    const height = def?.height ?? 60;
    const nv = vs.nodes?.[n.id];
    return {
      id: n.id,
      type: specKindToRfType(n.type),
      position: { x: nv?.x ?? 0, y: nv?.y ?? 0 },
      selected: lastSelectionIds.includes(n.id),
      data: {
        label: n.id,
        sublabel: nv?.sublabel,
        x: nv?.x,
        y: nv?.y,
        type: n.type,
        fill: def?.fill ?? "#ffffff",
        stroke: def?.stroke ?? "#888",
        shape: def?.shape ?? "rect",
        width,
        height,
        inputs: n.inputs ?? def?.inputs ?? [],
        outputs: n.outputs ?? def?.outputs ?? [],
        // Round-trip node.state so initial dx/dy (and any other handler-state
        // seed) survives spec → flow → spec. Runner overwrites world.state
        // from initWorld; the spec field is the seed, not the live value.
        state: nv?.state,
        props: n.props,
        spec: n.spec,
        notes: n.notes,
        // Same round-trip story for n.data — Input nodes use it for
        // {init: [...]}, future node types may use it for other handler
        // config. flow-to-spec puts it back.
        nodeData: n.data,
        initialSlots: n.initialSlots,
        index: n.index,
        foldId: foldOf.get(n.id),
        dimmed: dimmed?.has(n.id) ?? false,
      },
    };
  });

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
      style: { stroke: KIND_COLORS[e.kind] ?? "#888", strokeWidth: 1.5 },
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

  // spec.notes[] render as RF nodes of type "note" so they pan/zoom with the
  // canvas. Index-keyed ids (`__note-N`) keep ordering stable across the
  // round-trip; flowToSpec reads the same prefix back.
  const NOTE_DEFAULT_W = 160;
  const NOTE_DEFAULT_H = 60;
  const noteNodes: RFNode[] = (spec.notes ?? []).map((nt, i) => ({
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

  // Fold rectangles render *behind* the rest, so emit them first.
  return { nodes: [...foldNodes, ...memberNodes, ...noteNodes], edges };
}
