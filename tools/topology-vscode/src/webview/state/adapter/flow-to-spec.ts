// Reverse adapter: RF state → Spec.
// Mirrors spec-to-flow.ts field-by-field. Position/state
// are viewer-only and are NOT written into the spec. edgeData and nodeData are
// carried verbatim so simulator-relevant fields survive the round-trip.
// currentSpec provides passthrough for top-level metadata fields via TOPOLOGY_META_FIELDS
// (passThrough: true entries). notes is rebuilt from RF note nodes instead.

import type { RFEdge, RFNode, NodeData, EdgeData } from "../../types";
import type { Spec, Node as SpecNode, Edge as SpecEdge, Note } from "../../../schema";
import { WIRE_PROPS } from "../../../schema/wire-defs";
import { TOPOLOGY_META_FIELDS } from "../../../schema/meta-field-defs";

export function flowToSpec(
  rfNodes: RFNode[],
  rfEdges: RFEdge[],
  currentSpec: Spec,
): Spec {
  const nodes: SpecNode[] = [];
  const notes: Note[] = [];

  for (const n of rfNodes) {
    // Note nodes are stored as spec.notes[], not spec.nodes[].
    if (n.type === "note") {
      const d = n.data as {
        text: string;
        width?: number;
        height?: number;
        hasWidth?: boolean;
        hasHeight?: boolean;
      };
      notes.push({
        x: n.position.x,
        y: n.position.y,
        text: d.text,
        ...(d.hasWidth ? { width: d.width } : {}),
        ...(d.hasHeight ? { height: d.height } : {}),
      });
      continue;
    }

    const d = n.data as NodeData;
    const node: SpecNode = {
      id: n.id,
      type: d.type,
    };
    if (d.index !== undefined) node.index = d.index;
    if (d.props !== undefined) node.props = d.props;
    if (d.spec !== undefined) node.spec = d.spec;
    if (d.notes !== undefined) node.notes = d.notes;
    // Pack state into data (Go reads it at data.state via wire:"data.state").
    // Merge with any existing nodeData so other data fields (e.g. init) are preserved.
    if (d.nodeData !== undefined || d.initState !== undefined) {
      const base = (d.nodeData ?? {}) as Record<string, unknown>;
      const merged: Record<string, unknown> = { ...base };
      if (d.initState !== undefined) merged["state"] = d.initState;
      node.data = merged;
    }
    if (d.inputs && d.inputs.length > 0) node.inputs = d.inputs;
    if (d.outputs && d.outputs.length > 0) node.outputs = d.outputs;
    nodes.push(node);
  }

  const edges: SpecEdge[] = [];
  for (const e of rfEdges) {
    const d = e.data as EdgeData | undefined;
    const sourceHandle = d?.sourceHandle ?? e.sourceHandle ?? "";
    const targetHandle = d?.targetHandle ?? e.targetHandle ?? "";
    const edge: SpecEdge = {
      id: e.id,
      source: e.source,
      sourceHandle,
      target: e.target,
      targetHandle,
      kind: d?.kind ?? "signal",
    };
    // Copy optional wire props from EdgeData → SpecEdge by iterating WIRE_PROPS.
    // `kind` is excluded: it is required and already set above via fallback.
    for (const key of Object.keys(WIRE_PROPS)) {
      if (key === "kind") continue;
      const v = d ? (d as unknown as Record<string, unknown>)[key] : undefined;
      if (v !== undefined) (edge as Record<string, unknown>)[key] = v;
    }
    if (d?.edgeData !== undefined) edge.data = d.edgeData;
    edges.push(edge);
  }

  const result: Spec = {
    nodes,
    edges,
    ...(notes.length > 0
      ? { notes }
      : currentSpec.notes && currentSpec.notes.length > 0
        ? { notes: currentSpec.notes }
        : {}),
  };

  // Spread passThrough metadata fields from currentSpec verbatim.
  for (const key of Object.keys(TOPOLOGY_META_FIELDS) as (keyof typeof TOPOLOGY_META_FIELDS)[]) {
    if (!TOPOLOGY_META_FIELDS[key].passThrough) continue;
    const v = (currentSpec as Record<string, unknown>)[key];
    if (v !== undefined) (result as Record<string, unknown>)[key] = v;
  }

  return result;
}
