// Cross-language contract: topology.json edge handles ↔ NODE_DEFS inputs/outputs.
//
// The Go loader validates handles at runtime; this test catches drift at
// `npm test` time so a bad topology.json fails the CI step, not the runner.
//
// Fixture: repo-root topology.json — the live topology.

import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { NODE_DEFS } from "../../src/schema/node-defs";

const TOPOLOGY_PATH = join(__dirname, "../../../../topology.json");

interface TopoNode {
  id: string;
  type: string;
}
interface TopoEdge {
  id: string;
  source: string;
  sourceHandle: string;
  target: string;
  targetHandle: string;
}
interface Topology {
  nodes: TopoNode[];
  edges: TopoEdge[];
}

// Go OutMulti ports are referenced as "<portName><index>" in topology.json.
// If the port carries isMulti:true (from generated metadata), match by base name prefix.
// Exact match is always accepted (covers non-multi ports).
function handleMatchesPort(handle: string, portName: string, isMulti?: boolean): boolean {
  if (handle === portName) return true;
  if (isMulti) {
    // OutMulti: handle must start with portName followed by a digit
    return handle.startsWith(portName) && /^\d+$/.test(handle.slice(portName.length));
  }
  return false;
}

describe("topology-edge-handles contract", () => {
  const topo: Topology = JSON.parse(readFileSync(TOPOLOGY_PATH, "utf8"));
  const nodeById = new Map(topo.nodes.map((n) => [n.id, n]));

  it("every edge sourceHandle exists in source node's NODE_DEFS outputs", () => {
    const failures: string[] = [];
    for (const edge of topo.edges) {
      const src = nodeById.get(edge.source);
      if (!src) { failures.push(`edge ${edge.id}: source node "${edge.source}" not found`); continue; }
      const def = NODE_DEFS[src.type as keyof typeof NODE_DEFS];
      if (!def) { failures.push(`edge ${edge.id}: kind "${src.type}" not in NODE_DEFS (checked verbatim)`); continue; }
      const outputs = (def as { outputs?: { name: string; isMulti?: boolean }[] }).outputs ?? [];
      if (!outputs.some((o) => handleMatchesPort(edge.sourceHandle, o.name, o.isMulti))) {
        failures.push(`edge ${edge.id}: node "${edge.source}" (${src.type}) has no output "${edge.sourceHandle}"`);
      }
    }
    expect(failures).toEqual([]);
  });

  it("every edge targetHandle exists in target node's NODE_DEFS inputs", () => {
    const failures: string[] = [];
    for (const edge of topo.edges) {
      const tgt = nodeById.get(edge.target);
      if (!tgt) { failures.push(`edge ${edge.id}: target node "${edge.target}" not found`); continue; }
      const def = NODE_DEFS[tgt.type as keyof typeof NODE_DEFS];
      if (!def) { failures.push(`edge ${edge.id}: kind "${tgt.type}" not in NODE_DEFS (checked verbatim)`); continue; }
      const inputs = (def as { inputs?: { name: string; isMulti?: boolean }[] }).inputs ?? [];
      if (!inputs.some((i) => handleMatchesPort(edge.targetHandle, i.name, i.isMulti))) {
        failures.push(`edge ${edge.id}: node "${edge.target}" (${tgt.type}) has no input "${edge.targetHandle}"`);
      }
    }
    expect(failures).toEqual([]);
  });
});
