// Pure helpers for spec-to-flow conversion: wire-prop picking and note node building.

import type { RFEdge, RFNode, EdgeData } from "../../types";
import { KIND_COLORS, type Spec } from "../../../schema";
import type { ViewerState } from "../viewer/types";
import { WIRE_PROPS } from "../../../schema/wire-defs";

/** Iterate WIRE_PROPS registry; skip `kind` (handled explicitly at call sites). */
function pickWireProps(e: Record<string, unknown>): Partial<EdgeData> {
  const out: Partial<EdgeData> = {};
  for (const key of Object.keys(WIRE_PROPS)) {
    if (key !== "kind" && e[key] !== undefined) (out as Record<string, unknown>)[key] = e[key];
  }
  return out;
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
  vs: Pick<ViewerState, "nodes" | "edges">,
): RFEdge<EdgeData>[] {
  const edges: RFEdge<EdgeData>[] = [];
  for (const e of spec.edges) {
    const ev = vs.edges?.[e.id];
    edges.push({
      id: e.id,
      source: e.source,
      target: e.target,
      sourceHandle: e.sourceHandle,
      targetHandle: e.targetHandle,
      type: "go",
      // label rendered via EdgeLabelRenderer (data.label), not RF's foreignObject.
      style: { stroke: (KIND_COLORS as Record<string, string>)[e.kind] ?? "#888", strokeWidth: 1.5 },
      data: {
        kind: e.kind,
        sourceHandle: e.sourceHandle,
        targetHandle: e.targetHandle,
        route: ev?.route ?? (e.data as { route?: string } | undefined)?.route,
        ...pickWireProps(e as unknown as Record<string, unknown>),
        // label is a required wire prop (edge identity — keys the channel); set it
        // explicitly AFTER the spread so its type stays `string` (pickWireProps
        // returns Partial<EdgeData>, which would otherwise re-widen it to optional).
        label: e.label,
        value: (e.data as Record<string, unknown> | undefined)?.value,
        edgeData: e.data, // verbatim: flow-to-spec round-trips backpressure/delay config
      },
    });
  }
  return edges;
}
