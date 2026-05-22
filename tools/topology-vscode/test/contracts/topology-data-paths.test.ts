// Contract: topology.json must not have root-level "state" or "edgeSeeds" on nodes.
// Go reads both at data.state / data.edgeSeeds; root-level fields are silently ignored
// by the loader and cause the parser to throw since go-ts-leverage merged.

import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const TOPOLOGY_PATH = join(__dirname, "../../../../topology.json");

interface TopoNode {
  id: string;
  type: string;
  state?: unknown;
  edgeSeeds?: unknown;
  data?: Record<string, unknown>;
}
interface Topology {
  nodes: TopoNode[];
}

describe("topology-data-paths contract", () => {
  const topo: Topology = JSON.parse(readFileSync(TOPOLOGY_PATH, "utf8"));

  it("no node has root-level 'state'", () => {
    const offenders = topo.nodes
      .filter((n) => "state" in n)
      .map((n) => `${n.id} (${n.type})`);
    expect(offenders).toEqual([]);
  });

  it("no node has root-level 'edgeSeeds'", () => {
    const offenders = topo.nodes
      .filter((n) => "edgeSeeds" in n)
      .map((n) => `${n.id} (${n.type})`);
    expect(offenders).toEqual([]);
  });

  it("nodes with data.state have it as an object", () => {
    const offenders = topo.nodes
      .filter((n) => n.data && "state" in n.data && (typeof n.data["state"] !== "object" || n.data["state"] === null))
      .map((n) => `${n.id} (${n.type}): data.state is ${JSON.stringify(n.data?.["state"])}`);
    expect(offenders).toEqual([]);
  });

  it("nodes with data.edgeSeeds have it as an object", () => {
    const offenders = topo.nodes
      .filter((n) => n.data && "edgeSeeds" in n.data && (typeof n.data["edgeSeeds"] !== "object" || n.data["edgeSeeds"] === null))
      .map((n) => `${n.id} (${n.type}): data.edgeSeeds is ${JSON.stringify(n.data?.["edgeSeeds"])}`);
    expect(offenders).toEqual([]);
  });
});
