// Contract: topology tree nodes must not have root-level "state" or "edgeSeeds" in meta.json.
// data.json IS the data object — data.state / data.edgeSeeds should be objects if present.

import { describe, expect, it } from "vitest";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

const TREE_NODES_DIR = join(__dirname, "../../../../topology/nodes");

interface NodeMeta {
  id: string;
  type: string;
  [key: string]: unknown;
}

// Only STATEFUL nodes carry a data.json (e.g. Input, Hold, HoldNewSendOld, Pacer);
// stateless kinds (HoldFlip, WindowAndGate, Pulse) have none. Skip the latter
// rather than asserting every node has a data.json — absence is correct, not a failure.
const nodes = readdirSync(TREE_NODES_DIR)
  .map((idDir) => {
    const meta: NodeMeta = JSON.parse(
      readFileSync(join(TREE_NODES_DIR, idDir, "meta.json"), "utf8")
    );
    let data: Record<string, unknown> | null = null;
    try {
      data = JSON.parse(
        readFileSync(join(TREE_NODES_DIR, idDir, "data.json"), "utf8")
      );
    } catch {
      // No data.json for this (stateless) node — skip data assertions for it.
    }
    return { id: meta.id, type: meta.type, meta, data };
  })
  .filter((n): n is typeof n & { data: Record<string, unknown> } => n.data !== null);

describe("topology-data-paths contract", () => {
  it("no node meta has root-level 'state'", () => {
    const offenders = nodes
      .filter((n) => "state" in n.meta)
      .map((n) => `${n.id} (${n.type})`);
    expect(offenders).toEqual([]);
  });

  it("no node meta has root-level 'edgeSeeds'", () => {
    const offenders = nodes
      .filter((n) => "edgeSeeds" in n.meta)
      .map((n) => `${n.id} (${n.type})`);
    expect(offenders).toEqual([]);
  });

  it("nodes with data.state have it as an object", () => {
    const offenders = nodes
      .filter(
        (n) =>
          "state" in n.data &&
          (typeof n.data["state"] !== "object" || n.data["state"] === null)
      )
      .map(
        (n) =>
          `${n.id} (${n.type}): data.state is ${JSON.stringify(n.data["state"])}`
      );
    expect(offenders).toEqual([]);
  });

  it("nodes with data.edgeSeeds have it as an object", () => {
    const offenders = nodes
      .filter(
        (n) =>
          "edgeSeeds" in n.data &&
          (typeof n.data["edgeSeeds"] !== "object" || n.data["edgeSeeds"] === null)
      )
      .map(
        (n) =>
          `${n.id} (${n.type}): data.edgeSeeds is ${JSON.stringify(n.data["edgeSeeds"])}`
      );
    expect(offenders).toEqual([]);
  });
});
