// Parsers for Node, Edge, NodeSpec, SpecSegment.

import { EDGE_KINDS, DEFAULT_EDGE_KIND } from "./types";
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

function parseDir(v: unknown, path: string): [number, number, number] {
  const a = arr(v, path);
  if (a.length !== 3) {
    throw new Error(`${path}: expected 3-element unit direction [x,y,z], got length ${a.length}`);
  }
  const c = a.map((x, i) => {
    const n = num(x, `${path}[${i}]`);
    if (!Number.isFinite(n)) {
      throw new Error(`${path}[${i}]: expected finite number, got ${JSON.stringify(x)}`);
    }
    return n;
  });
  // c has exactly 3 elements (mapped from the length-3-checked `a`).
  return [c[0]!, c[1]!, c[2]!];
}

function parsePort(v: unknown, path: string): Port {
  const o = obj(v, path);
  // Port `kind` is vestigial: not stored in the canonical topology tree
  // (nodes/<id>/inputs|outputs/*.json) and has no downstream consumer — edge
  // colour/behaviour keys off edge.kind, never port.kind. Default it when
  // absent so the Go spec emission (which omits it) parses; still validate
  // against EDGE_KINDS when present so a genuinely-bad value is rejected.
  const out: Port = {
    name: str(o.name, `${path}.name`),
    kind: o.kind === undefined
      ? DEFAULT_EDGE_KIND
      : oneOf(o.kind, EDGE_KINDS, `${path}.kind`),
  };
  if (o.required !== undefined) out.required = bool(o.required, `${path}.required`);
  if (o.anchorId !== undefined) {
    const id = num(o.anchorId, `${path}.anchorId`);
    if (!Number.isInteger(id) || id < 0) {
      throw new Error(`${path}.anchorId: expected non-negative integer, got ${JSON.stringify(o.anchorId)}`);
    }
    out.anchorId = id;
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
    r: opt(o.r, (x) => {
      const n = num(x, `${path}.r`);
      if (!Number.isFinite(n)) throw new Error(`${path}.r: expected finite number, got ${JSON.stringify(x)}`);
      return n;
    }),
    dir: opt(o.dir, (x) => parseDir(x, `${path}.dir`)),
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
      else
        // A non-scalar wire prop would be silently DROPPED (never copied onto edge),
        // and the double-cast at the bottom would hide the gap. Fail loudly instead so
        // a future non-scalar WIRE_PROPS entry forces a parser update here.
        throw new Error(
          `${path}.${key}: unhandled wire-prop tsType "${def.tsType}" — extend parse-nodes-edges.ts to parse it`,
        );
    } else {
      if (def.tsType === "string") edge[key] = opt(val, (x) => str(x, `${path}.${key}`));
      else if (def.tsType === "number") edge[key] = opt(val, (x) => num(x, `${path}.${key}`));
      else if (def.tsType === "boolean") edge[key] = opt(val, (x) => bool(x, `${path}.${key}`));
      else
        throw new Error(
          `${path}.${key}: unhandled wire-prop tsType "${def.tsType}" — extend parse-nodes-edges.ts to parse it`,
        );
    }
  }
  // Double-cast: edge is built as Record<string,unknown> via the dynamic WIRE_PROPS loop;
  // TypeScript can't statically verify all required Edge fields are present.
  return edge as unknown as Edge;
}
