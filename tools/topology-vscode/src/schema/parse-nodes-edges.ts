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
  numMap,
  stateMap,
  str,
} from "./parse-primitives";
import { parseNodeData } from "./node-data-types";
import { WIRE_PROPS } from "./wire-defs";
import { RUNTIME_IMPLEMENTED_KINDS } from "./node-types";

const KNOWN_NODE_KINDS: ReadonlySet<string> = new Set([...RUNTIME_IMPLEMENTED_KINDS, "Generic"]);

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
  if (o.anchor !== undefined) {
    const a = o.anchor;
    if (
      a === null || typeof a !== "object" || Array.isArray(a) ||
      typeof (a as Record<string, unknown>).x !== "number" ||
      typeof (a as Record<string, unknown>).y !== "number" ||
      typeof (a as Record<string, unknown>).z !== "number"
    ) {
      throw new Error(`${path}.anchor: expected {x,y,z} numbers, got ${JSON.stringify(o.anchor)}`);
    }
    const ar = a as Record<string, unknown>;
    out.anchor = { x: ar.x as number, y: ar.y as number, z: ar.z as number };
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
  const nodeType = str(o.type, `${path}.type`);
  if (!KNOWN_NODE_KINDS.has(nodeType)) {
    throw new Error(
      `${path}.type: unknown node kind "${nodeType}". ` +
      `Known kinds: ${[...KNOWN_NODE_KINDS].sort().join(", ")}`,
    );
  }
  return {
    id: str(o.id, `${path}.id`),
    type: nodeType,
    index: opt(o.index, (x) => num(x, `${path}.index`)),
    props: opt(o.props, (x) => stateMap(x, `${path}.props`)),
    spec: opt(o.spec, (x) => parseNodeSpec(x, `${path}.spec`)),
    notes: opt(o.notes, (x) => str(x, `${path}.notes`)),
    data: parseNodeData(nodeType, o.data, path),
    inputs: opt(o.inputs, (x) => parsePorts(x, `${path}.inputs`)),
    outputs: opt(o.outputs, (x) => parsePorts(x, `${path}.outputs`)),
    state: (() => {
      if (o.state !== undefined) throw new Error(
        `${path}.state: root-level "state" is not valid; use data.state instead (Go wire:"data.state" contract).`,
      );
      const d = o.data as Record<string, unknown> | undefined;
      return opt(d?.["state"], (x) => numMap(x, `${path}.data.state`));
    })(),
  };
}

export function parseEdge(v: unknown, path: string): Edge {
  const o = obj(v, path);
  const id = str(o.id, `${path}.id`);
  const source = str(o.source, `${path}.source`);
  const target = str(o.target, `${path}.target`);
  const label = (o as Record<string, unknown>).label;
  if (label === undefined || label === null || String(label).trim() === "") {
    throw new Error(`${path}: edge "${id}" (${source}→${target}) has missing or empty label`);
  }
  const edge: Record<string, unknown> = {
    id,
    source,
    sourceHandle: str(o.sourceHandle, `${path}.sourceHandle`),
    target,
    targetHandle: str(o.targetHandle, `${path}.targetHandle`),
    // kind: required EdgeKind enum — kept explicit
    kind: oneOf(o.kind, EDGE_KINDS, `${path}.kind`),
    data: o.data,
  };
  // Loop over WIRE_PROPS for simple scalar types (string, number, boolean).
  for (const [key, def] of Object.entries(WIRE_PROPS)) {
    if (key === "kind") continue; // handled explicitly above
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
