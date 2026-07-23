// countNodes.test.ts — mirrors the (untested) countEdges shape: sizes the per-node
// dedicated node/interior fd range from a topology spec BEFORE Go is spawned (see
// runCommand.ts's NODE_BASE_FD/INTERIOR_BASE_FD doc comment). Both the directory-tree
// form (`<root>/nodes/<label>.json`) and the monolithic `{"nodes":[...]}` form are
// exercised, plus the required fallback (0) on any read/parse failure.

import { describe, it, expect } from "vitest";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { countNodes } from "../src/runCommand";

function mkTmpDir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "wirefold-countnodes-"));
}

describe("countNodes", () => {
  it("counts .json files under <root>/nodes for the directory-tree spec form", () => {
    const root = mkTmpDir();
    const nodesDir = path.join(root, "nodes");
    fs.mkdirSync(nodesDir);
    fs.writeFileSync(path.join(nodesDir, "a.json"), "{}");
    fs.writeFileSync(path.join(nodesDir, "b.json"), "{}");
    fs.writeFileSync(path.join(nodesDir, "not-json.txt"), "x");
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
