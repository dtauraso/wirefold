// Cross-language contract: topology.json edge handles ↔ NODE_DEFS inputs/outputs.
//
// The Go loader validates handles at runtime; this test catches drift at
// `npm test` time so a bad topology.json fails the CI step, not the runner.
//
// Fixture: repo-root topology.json — the live topology.

import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { NODE_DEFS } from "../../src/webview/rf/nodes/node-defs";

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

function specKindToDefKey(kind: string): string {
  return kind.charAt(0).toLowerCase() + kind.slice(1);
}

// Go OutMulti ports are referenced as "<portName><index>" in topology.json.
// Normalize by stripping a trailing digit suffix and checking the base name.
function handleMatchesPort(handle: string, portName: string): boolean {
  if (handle === portName) return true;
  const stripped = handle.replace(/\d+$/, "");
  return stripped === portName;
}

describe("topology-edge-handles contract", () => {
  const topo: Topology = JSON.parse(readFileSync(TOPOLOGY_PATH, "utf8"));
  const nodeById = new Map(topo.nodes.map((n) => [n.id, n]));

  it("every edge sourceHandle exists in source node's NODE_DEFS outputs", () => {
    const failures: string[] = [];
    for (const edge of topo.edges) {
      const src = nodeById.get(edge.source);
      if (!src) { failures.push(`edge ${edge.id}: source node "${edge.source}" not found`); continue; }
      const key = specKindToDefKey(src.type);
      const def = NODE_DEFS[key as keyof typeof NODE_DEFS];
      if (!def) { failures.push(`edge ${edge.id}: kind "${src.type}" not in NODE_DEFS`); continue; }
      const outputs = (def as { outputs?: { name: string }[] }).outputs ?? [];
      if (!outputs.some((o) => handleMatchesPort(edge.sourceHandle, o.name))) {
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
      const key = specKindToDefKey(tgt.type);
      const def = NODE_DEFS[key as keyof typeof NODE_DEFS];
      if (!def) { failures.push(`edge ${edge.id}: kind "${tgt.type}" not in NODE_DEFS`); continue; }
      const inputs = (def as { inputs?: { name: string }[] }).inputs ?? [];
      if (!inputs.some((i) => handleMatchesPort(edge.targetHandle, i.name))) {
        failures.push(`edge ${edge.id}: node "${edge.target}" (${tgt.type}) has no input "${edge.targetHandle}"`);
      }
    }
    expect(failures).toEqual([]);
  });
});
