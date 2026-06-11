// Structural-save guard: flowToSpec must refuse to serialize a non-note node
// with an empty kind. Such a node round-trips into a typeless, portless
// topology.json node that crash-loops Go's LoadTopology. The guard fails loud
// so performSave can keep the previous good file on disk.

import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { flowToSpec } from "../src/webview/state/adapter/flow-to-spec";
import type { RFNode, RFEdge, NodeData, EdgeData } from "../src/webview/types";
import type { Spec } from "../src/schema";

const EMPTY_SPEC: Spec = { nodes: [], edges: [] };

function node(id: string, type: string | undefined): RFNode<NodeData> {
  return {
    id,
    type: "graphNode",
    position: { x: 0, y: 0 },
    data: { label: id, type: type as string } as NodeData,
  } as RFNode<NodeData>;
}

describe("flowToSpec structural guard", () => {
  it("throws naming the node when a non-note node has empty type", () => {
    const nodes = [node("good", "Input"), node("bad", "")];
    expect(() => flowToSpec(nodes, [] as RFEdge<EdgeData>[], EMPTY_SPEC)).toThrowError(
      /node "bad" has empty type/,
    );
  });

  it("throws when type is undefined", () => {
    const nodes = [node("ghost", undefined)];
    expect(() => flowToSpec(nodes, [] as RFEdge<EdgeData>[], EMPTY_SPEC)).toThrowError(
      /node "ghost" has empty type/,
    );
  });

  it("serializes normally when all nodes carry a kind", () => {
    const nodes = [node("a", "Input"), node("b", "ReadGate")];
    const spec = flowToSpec(nodes, [] as RFEdge<EdgeData>[], EMPTY_SPEC);
    expect(spec.nodes.map((n) => n.id)).toEqual(["a", "b"]);
    expect(spec.nodes.map((n) => n.type)).toEqual(["Input", "ReadGate"]);
  });

  it("does not flag note nodes (no type kind required)", () => {
    const noteNode = {
      id: "__note-0",
      type: "note",
      position: { x: 1, y: 2 },
      data: { text: "hi" },
    } as unknown as RFNode<NodeData>;
    const spec = flowToSpec([noteNode], [] as RFEdge<EdgeData>[], EMPTY_SPEC);
    expect(spec.nodes).toHaveLength(0);
    expect(spec.notes?.[0]?.text).toBe("hi");
  });
});

describe("performSave refuses to post when spec is structurally invalid", () => {
  beforeEach(() => {
    // jsdom-free: stub the #status element performSave's setStatus touches.
    const el = { textContent: "", className: "" };
    vi.stubGlobal("document", {
      getElementById: () => el,
    });
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.resetModules();
  });

  it("does not post a save message for an empty-type node, posts for a valid one", async () => {
    const posted: unknown[] = [];
    vi.doMock("../src/webview/vscode-api", () => ({
      vscode: { postMessage: (m: unknown) => posted.push(m) },
    }));
    vi.doMock("../src/webview/log/post", () => ({ postLog: () => {} }));

    const storeNodes: RFNode<NodeData>[] = [node("bad", "")];
    vi.doMock("../src/webview/three/store", () => ({
      useThreeStore: { getState: () => ({ nodes: storeNodes, edges: [] }) },
    }));

    const save = await import("../src/webview/save");
    save.performSave();
    expect(posted.filter((m) => (m as { type?: string }).type === "save")).toHaveLength(0);

    // Now a valid node → it posts.
    storeNodes.length = 0;
    storeNodes.push(node("ok", "Input"));
    save.performSave();
    expect(posted.filter((m) => (m as { type?: string }).type === "save")).toHaveLength(1);
  });
});
