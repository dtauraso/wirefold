// Parsers for Note + the validatePorts pass that
// flags unknown source/target ids and unknown port names.

import type { Note, Spec } from "./types-graph";
import { NODE_TYPES, RUNTIME_IMPLEMENTED_KINDS } from "./node-types";
import {
  num,
  obj,
  opt,
  ParseError,
  str,
} from "./parse-primitives";

export function parseNote(v: unknown, path: string): Note {
  const o = obj(v, path);
  return {
    x: num(o.x, `${path}.x`),
    y: num(o.y, `${path}.y`),
    width: opt(o.width, (x) => num(x, `${path}.width`)),
    height: opt(o.height, (x) => num(x, `${path}.height`)),
    text: str(o.text, `${path}.text`),
  };
}

export function validatePorts(s: Spec): void {
  const byId = new Map(s.nodes.map((n) => [n.id, n]));
  const issues: string[] = [];

  // Unknown kind check: surface before port/edge validation.
  const knownTypes = new Set([...RUNTIME_IMPLEMENTED_KINDS, ...Object.keys(NODE_TYPES)]);
  for (const n of s.nodes) {
    if (!knownTypes.has(n.type)) {
      issues.push(`node "${n.id}": unknown type "${n.type}"`);
    }
  }
  if (issues.length) throw new ParseError(issues.join("\n"));
  const wiredInputs = new Map<string, Set<string>>();
  for (const e of s.edges) {
    const src = byId.get(e.source);
    const dst = byId.get(e.target);
    if (!src) { issues.push(`edge ${e.id}: unknown source ${e.source}`); continue; }
    if (!dst) { issues.push(`edge ${e.id}: unknown target ${e.target}`); continue; }
    const srcDef = NODE_TYPES[src.type];
    const dstDef = NODE_TYPES[dst.type];
    const srcOutputs = src.outputs ?? srcDef?.outputs;
    const dstInputs = dst.inputs ?? dstDef?.inputs;
    if (srcOutputs && e.sourceHandle && !srcOutputs.some((p) => p.name === e.sourceHandle)) {
      issues.push(`edge ${e.id}: ${src.type} has no output port "${e.sourceHandle}"`);
    }
    if (dstInputs && e.targetHandle && !dstInputs.some((p) => p.name === e.targetHandle)) {
      issues.push(`edge ${e.id}: ${dst.type} has no input port "${e.targetHandle}"`);
    }
    if (e.targetHandle) {
      let set = wiredInputs.get(e.target);
      if (!set) { set = new Set(); wiredInputs.set(e.target, set); }
      set.add(e.targetHandle);
    }
  }
  for (const n of s.nodes) {
    const def = NODE_TYPES[n.type];
    const inputs = n.inputs ?? def?.inputs;
    if (!inputs) continue;
    const wired = wiredInputs.get(n.id) ?? new Set<string>();
    for (const p of inputs) {
      if (p.required && !wired.has(p.name)) {
        issues.push(
          `node ${n.id} (${n.type}): required input "${p.name}" has no incoming edge`,
        );
      }
    }
  }
  for (const n of s.nodes) {
    if (!n.edgeSeeds) continue;
    const def = NODE_TYPES[n.type];
    const inputs = n.inputs ?? def?.inputs;
    if (!inputs) continue;
    const inputNames = new Set(inputs.map((p) => p.name));
    for (const key of Object.keys(n.edgeSeeds)) {
      if (!inputNames.has(key)) {
        issues.push(
          `node ${n.id} (${n.type}): edgeSeeds key "${key}" does not match any declared input port`,
        );
      }
    }
  }
  if (issues.length) throw new ParseError(issues.join("\n"));
}
