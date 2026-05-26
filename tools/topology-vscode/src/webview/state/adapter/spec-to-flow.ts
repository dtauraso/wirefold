import type { RFEdge, RFNode, NodeData, EdgeData } from "../../types";
import { NODE_TYPES, type Node as SpecNode, type Spec, requiredInputDiagnostics } from "../../../schema";
import type { Fold, ViewerState } from "../viewer/types";
import {
  buildEdges,
  buildFoldNodes,
  buildNoteNodes,
} from "./spec-to-flow-helpers";

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
  const invalid = requiredInputDiagnostics(spec);
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

  const foldNodes = buildFoldNodes(folds, nodeById, vs);

  const memberNodes: RFNode<NodeData>[] = spec.nodes
    .filter((n) => !collapsedFoldFor.has(n.id))
    .map((n) => {
    const def = NODE_TYPES[n.type];
    const width = def?.width ?? 110;
    const height = def?.height ?? 60;
    const nv = vs.nodes?.[n.id];
    return {
      id: n.id,
      type: n.type,
      position: { x: nv?.x ?? 0, y: nv?.y ?? 0 },
      selected: lastSelectionIds.includes(n.id),
      data: {
        label: n.id,
        sublabel: nv?.sublabel,
        x: nv?.x,
        y: nv?.y,
        z: nv?.z ?? 0,
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
        // node.state / node.edgeSeeds are parsed from data.state / data.edgeSeeds
        // (Go wire:"data.state" / data.edgeSeeds contract).
        state: nv?.state,
        props: n.props,
        spec: n.spec,
        notes: n.notes,
        // n.data is the raw data blob (includes init, state, edgeSeeds per kind).
        // flow-to-spec rebuilds it from nodeData + initState + edgeSeeds.
        nodeData: n.data,
        initState: n.state,
        edgeSeeds: n.edgeSeeds,
        index: n.index,
        foldId: foldOf.get(n.id),
        dimmed: dimmed?.has(n.id) ?? false,
        validationError: invalid.get(n.id),
      },
    };
  });

  const edges = buildEdges(spec, collapsedFoldFor, vs);

  // spec.notes[] render as RF nodes of type "note" so they pan/zoom with the
  // canvas. Index-keyed ids (`__note-N`) keep ordering stable across the
  // round-trip; flowToSpec reads the same prefix back.
  const noteNodes = buildNoteNodes(spec);

  // Fold rectangles render *behind* the rest, so emit them first.
  return { nodes: [...foldNodes, ...memberNodes, ...noteNodes], edges };
}
