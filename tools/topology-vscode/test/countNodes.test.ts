// countNodes.test.ts — mirrors the (untested) countEdges shape: sizes the per-node
// dedicated node/interior fd range from a topology spec BEFORE Go is spawned (see
// runCommand.ts's NODE_BASE_FD/INTERIOR_BASE_FD doc comment). Both the directory-tree
// form (`<root>/nodes/<id>/` — one SUBDIRECTORY per node id, e.g. data.json/meta.json/
// inputs/outputs/, NOT flat `<id>.json` files — see nodes/Wiring/loader.go's parseSpec
// dispatch and headless_node_row_order_test.go's wantNodeRowOrder, the Go-side
// counterpart this mirrors) and the monolithic `{"nodes":[...]}` form are exercised,
// plus the required fallback (0) on any read/parse failure.

import { describe, it, expect } from "vitest";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { countNodes } from "../src/runCommand";

function mkTmpDir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "wirefold-countnodes-"));
}

describe("countNodes", () => {
  it("counts SUBDIRECTORIES under <root>/nodes for the directory-tree spec form", () => {
    const root = mkTmpDir();
    const nodesDir = path.join(root, "nodes");
    fs.mkdirSync(nodesDir);
    fs.mkdirSync(path.join(nodesDir, "a"));
    fs.writeFileSync(path.join(nodesDir, "a", "data.json"), "{}");
    fs.mkdirSync(path.join(nodesDir, "b"));
    fs.writeFileSync(path.join(nodesDir, "b", "data.json"), "{}");
    // A stray flat file (never a real node shape) must not be counted as a node.
    fs.writeFileSync(path.join(nodesDir, "not-a-node.json"), "{}");
    expect(countNodes(root)).toBe(2);
  });

  it("returns 0 when the directory-tree spec has no nodes/ subdir", () => {
    const root = mkTmpDir();
    expect(countNodes(root)).toBe(0);
  });

  it("counts the nodes array for the monolithic topology.json spec form", () => {
    const root = mkTmpDir();
    const specPath = path.join(root, "topology.json");
    fs.writeFileSync(specPath, JSON.stringify({ nodes: [{}, {}, {}], edges: [] }));
    expect(countNodes(specPath)).toBe(3);
  });

  it("returns 0 on a missing path (read failure)", () => {
    expect(countNodes("/definitely/does/not/exist/topology")).toBe(0);
  });

  it("returns 0 on unparseable JSON (parse failure)", () => {
    const root = mkTmpDir();
    const specPath = path.join(root, "topology.json");
    fs.writeFileSync(specPath, "{not json");
    expect(countNodes(specPath)).toBe(0);
  });
});
