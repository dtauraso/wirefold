// Cross-language contract: topology tree edge handles ↔ NODE_DEFS inputs/outputs.
//
// The Go loader validates handles at runtime; this test catches drift at
// `npm test` time so a bad topology fails the CI step, not the runner.
//
// Fixture: topology/ tree — edges/*.json and nodes/*/meta.json

import { describe, expect, it } from "vitest";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { NODE_DEFS } from "../../src/schema/node-defs";

const TREE_NODES_DIR = join(__dirname, "../../../../topology/nodes");
const TREE_EDGES_DIR = join(__dirname, "../../../../topology/edges");

interface TopoNode {
  id: string;
  type: string;
}
interface TopoEdge {
  source: string;
  sourceHandle: string;
  target: string;
  targetHandle: string;
  label: string;
}

const nodes: TopoNode[] = readdirSync(TREE_NODES_DIR).map((idDir) => {
  const meta = JSON.parse(
    readFileSync(join(TREE_NODES_DIR, idDir, "meta.json"), "utf8")
  );
  return { id: meta.id, type: meta.type };
});

const edges: TopoEdge[] = readdirSync(TREE_EDGES_DIR)
  .filter((f) => f.endsWith(".json"))
  .map((f) =>
    JSON.parse(readFileSync(join(TREE_EDGES_DIR, f), "utf8"))
  );

// Go OutMulti ports are referenced as "<portName><index>" in the tree.
// If the port carries isMulti:true (from generated metadata), match by base name prefix.
// Exact match is always accepted (covers non-multi ports).
function handleMatchesPort(handle: string, portName: string, isMulti?: boolean): boolean {
  if (handle === portName) return true;
  if (isMulti) {
    return handle.startsWith(portName) && /^\d+$/.test(handle.slice(portName.length));
  }
  return false;
}

describe("topology-edge-handles contract", () => {
  const nodeById = new Map(nodes.map((n) => [n.id, n]));

  it("every edge sourceHandle exists in source node's NODE_DEFS outputs", () => {
    const failures: string[] = [];
    for (const edge of edges) {
      const src = nodeById.get(edge.source);
      if (!src) { failures.push(`edge ${edge.label}: source node "${edge.source}" not found`); continue; }
      const def = NODE_DEFS[src.type as keyof typeof NODE_DEFS];
      if (!def) { failures.push(`edge ${edge.label}: kind "${src.type}" not in NODE_DEFS (checked verbatim)`); continue; }
      const outputs = (def as { outputs?: { name: string; isMulti?: boolean }[] }).outputs ?? [];
      if (!outputs.some((o) => handleMatchesPort(edge.sourceHandle, o.name, o.isMulti))) {
        failures.push(`edge ${edge.label}: node "${edge.source}" (${src.type}) has no output "${edge.sourceHandle}"`);
      }
    }
    expect(failures).toEqual([]);
  });

  it("every edge targetHandle exists in target node's NODE_DEFS inputs", () => {
    const failures: string[] = [];
    for (const edge of edges) {
      const tgt = nodeById.get(edge.target);
      if (!tgt) { failures.push(`edge ${edge.label}: target node "${edge.target}" not found`); continue; }
      const def = NODE_DEFS[tgt.type as keyof typeof NODE_DEFS];
      if (!def) { failures.push(`edge ${edge.label}: kind "${tgt.type}" not in NODE_DEFS (checked verbatim)`); continue; }
      const inputs = (def as { inputs?: { name: string; isMulti?: boolean }[] }).inputs ?? [];
      if (!inputs.some((i) => handleMatchesPort(edge.targetHandle, i.name, i.isMulti))) {
        failures.push(`edge ${edge.label}: node "${edge.target}" (${tgt.type}) has no input "${edge.targetHandle}"`);
      }
    }
    expect(failures).toEqual([]);
  });
});
