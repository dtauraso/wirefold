// Parsers for Node, Edge, NodeSpec, SpecSegment.

import { EDGE_KINDS } from "./types";
import type { Port } from "./types";
import type {
  Edge,
  Node,
  NodeSpec,
  SpecSegment,
} from "./types-graph";
import {
  arr,
  bool,
  fail,
  num,
  obj,
  oneOf,
  opt,
  stateMap,
  str,
} from "./parse-primitives";
import { parseNodeData } from "./node-data-types";
import { WIRE_PROPS } from "./wire-defs";

function parsePort(v: unknown, path: string): Port {
  const o = obj(v, path);
  const out: Port = {
    name: str(o.name, `${path}.name`),
    kind: oneOf(o.kind, EDGE_KINDS, `${path}.kind`),
  };
  if (o.required !== undefined) out.required = bool(o.required, `${path}.required`);
  if (o.side !== undefined) out.side = oneOf(o.side, ["left", "right", "top", "bottom"] as const, `${path}.side`);
  if (o.slot !== undefined) {
    if (o.slot !== 0 && o.slot !== 1 && o.slot !== 2) {
      throw new Error(`${path}.slot: expected 0|1|2, got ${JSON.stringify(o.slot)}`);
    }
    out.slot = o.slot;
  }
  return out;
}

function parsePorts(v: unknown, path: string): Port[] {
  return arr(v, path).map((p, i) => parsePort(p, `${path}[${i}]`));
}

function parseSpecSegment(v: unknown, path: string): SpecSegment {
  const o = obj(v, path);
  if ("text" in o) return { text: str(o.text, `${path}.text`) };
  if ("outputRef" in o) return { outputRef: str(o.outputRef, `${path}.outputRef`) };
  return fail(path, `expected {text} or {outputRef}, got ${JSON.stringify(o)}`);
}

function parseNodeSpec(v: unknown, path: string): NodeSpec {
  const o = obj(v, path);
  return {
    lang: str(o.lang, `${path}.lang`),
    segments: arr(o.segments, `${path}.segments`).map((s, i) =>
      parseSpecSegment(s, `${path}.segments[${i}]`),
    ),
  };
}

export function parseNode(v: unknown, path: string): Node {
  const o = obj(v, path);
  return {
    id: str(o.id, `${path}.id`),
    type: str(o.type, `${path}.type`),
    index: opt(o.index, (x) => num(x, `${path}.index`)),
    props: opt(o.props, (x) => stateMap(x, `${path}.props`)),
    spec: opt(o.spec, (x) => parseNodeSpec(x, `${path}.spec`)),
    notes: opt(o.notes, (x) => str(x, `${path}.notes`)),
    data: parseNodeData(str(o.type, `${path}.type`), o.data, path),
    inputs: opt(o.inputs, (x) => parsePorts(x, `${path}.inputs`)),
    outputs: opt(o.outputs, (x) => parsePorts(x, `${path}.outputs`)),
    state: opt(o.state, (x) => stateMap(x, `${path}.state`)),
    edgeSeeds: opt(o.edgeSeeds, (x) => stateMap(x, `${path}.edgeSeeds`)),
  };
}

export function parseEdge(v: unknown, path: string): Edge {
  const o = obj(v, path);
  const edge: Record<string, unknown> = {
    id: str(o.id, `${path}.id`),
    source: str(o.source, `${path}.source`),
    sourceHandle: str(o.sourceHandle, `${path}.sourceHandle`),
    target: str(o.target, `${path}.target`),
    targetHandle: str(o.targetHandle, `${path}.targetHandle`),
    // kind: required EdgeKind enum — kept explicit
    kind: oneOf(o.kind, EDGE_KINDS, `${path}.kind`),
    // arrowStyle: optional ArrowStyle enum — kept explicit (no ARROW_STYLES constant)
    arrowStyle: opt(o.arrowStyle, (x) =>
      oneOf(x, ["filled", "open"] as const, `${path}.arrowStyle`),
    ),
    data: o.data,
  };
  // Loop over WIRE_PROPS for simple scalar types (string, number, boolean).
  for (const [key, def] of Object.entries(WIRE_PROPS)) {
    if (key === "kind" || key === "arrowStyle") continue; // handled explicitly above
    const val = o[key];
    if (def.required) {
      if (def.tsType === "string") edge[key] = str(val, `${path}.${key}`);
      else if (def.tsType === "number") edge[key] = num(val, `${path}.${key}`);
      else if (def.tsType === "boolean") edge[key] = bool(val, `${path}.${key}`);
    } else {
      if (def.tsType === "string") edge[key] = opt(val, (x) => str(x, `${path}.${key}`));
      else if (def.tsType === "number") edge[key] = opt(val, (x) => num(x, `${path}.${key}`));
      else if (def.tsType === "boolean") edge[key] = opt(val, (x) => bool(x, `${path}.${key}`));
    }
  }
  return edge as unknown as Edge;
}
