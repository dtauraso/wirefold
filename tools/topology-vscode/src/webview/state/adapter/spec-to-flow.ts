import type { RFEdge, RFNode, NodeData, EdgeData } from "../../types";
import { NODE_TYPES, type Node as SpecNode, type Spec } from "../../../schema";
import type { ViewerState } from "../viewer/types";
import { NODE_DIM_FALLBACK } from "../node-dims";
import {
  buildEdges,
  buildNoteNodes,
} from "./spec-to-flow-helpers";

export function specToFlow(
  spec: Spec,
  vs: Pick<ViewerState, "nodes" | "edges"> = {},
  lastSelectionIds: string[] = [],
): { nodes: RFNode<NodeData>[]; edges: RFEdge<EdgeData>[] } {
  const memberNodes: RFNode<NodeData>[] = spec.nodes
    .map((n) => {
    const def = NODE_TYPES[n.type];
    const width = def?.width ?? NODE_DIM_FALLBACK.width;
    const height = def?.height ?? NODE_DIM_FALLBACK.height;
    const nv = vs.nodes?.[n.id];
    return {
      id: n.id,
      type: n.type,
      position: { x: nv?.x ?? 0, y: nv?.y ?? 0 },
      selected: lastSelectionIds.includes(n.id),
      data: {
        label: n.id,
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
        // node.state is parsed from data.state (Go wire:"data.state" contract).
        state: nv?.state,
        props: n.props,
        spec: n.spec,
        notes: n.notes,
        // n.data is the raw data blob (includes init, state per kind).
        // flow-to-spec rebuilds it from nodeData + initState.
        nodeData: n.data,
        r: n.r,
        dir: n.dir,
        initState: n.state,
        index: n.index,
      },
    };
  });

  const edges = buildEdges(spec, vs);

  // spec.notes[] render as RF nodes of type "note" so they pan/zoom with the
  // canvas. Index-keyed ids (`__note-N`) keep ordering stable across the
  // round-trip; flowToSpec reads the same prefix back.
  const noteNodes = buildNoteNodes(spec);

  return { nodes: [...memberNodes, ...noteNodes] as RFNode<NodeData>[], edges };
}
