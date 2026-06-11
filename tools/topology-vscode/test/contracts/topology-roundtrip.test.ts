// Deterministic save/load round-trip gate (Phase 5 verifier).
//
// Loads the live repo topology.json, runs the editor's load → save serialization
// path (parseSpec → specToFlow → flowToSpec), and asserts the round-trip is
// idempotent: the re-serialized spec re-parses validly AND is deep-equal to the
// originally parsed spec. This is the deterministic stand-in for the manual
// "save/load through the VS Code UI" end-check — if the in-memory serialization
// path drifts (a dropped field, a changed edge id, a lost port list), this fails
// at `npm test` rather than silently corrupting topology.json on the next save.

import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { parseSpec } from "../../src/schema";
import type { Spec } from "../../src/schema";
import { specToFlow } from "../../src/webview/state/adapter/spec-to-flow";
import { flowToSpec } from "../../src/webview/state/adapter/flow-to-spec";
import { parseViewerState } from "../../src/webview/state/viewer/types";

const TOPOLOGY_PATH = join(__dirname, "../../../../topology.json");

// One full editor round-trip: spec → RF flow state → spec, exactly as the live
// load (store.load) then save (save.performSave → flowToSpec) path runs it.
function roundTrip(spec: Spec, viewText: string | undefined): Spec {
  const vs = parseViewerState(viewText);
  const { nodes, edges } = specToFlow(spec, vs, vs.lastSelectionIds ?? []);
  return flowToSpec(nodes, edges, spec);
}

describe("topology.json save/load round-trip is idempotent", () => {
  const raw = JSON.parse(readFileSync(TOPOLOGY_PATH, "utf8"));
  const viewText = raw.view !== undefined ? JSON.stringify(raw.view) : undefined;
  const spec = parseSpec(raw, raw.view);

  it("re-serialized spec re-parses without throwing", () => {
    const out = roundTrip(spec, viewText);
    expect(() => parseSpec(out, raw.view)).not.toThrow();
  });

  it("reloaded round-trip is deep-equal to the originally parsed spec (idempotent)", () => {
    // The real equivalence: the editor SAVES roundTrip(spec) to disk, then on the
    // next open RE-PARSES it. parse(save(load(x))) must equal load(x). (A raw
    // deep-equal of save(load(x)) vs load(x) is intentionally NOT used: parseSpec
    // surfaces data.state as a convenience top-level node.state that the serializer
    // keeps only in data.state — re-parsing restores it, which is what reload does.)
    const reloaded = parseSpec(roundTrip(spec, viewText), raw.view);
    expect(reloaded).toEqual(spec);
  });

  it("a second round-trip is a fixpoint (stable, no progressive drift)", () => {
    const once = roundTrip(spec, viewText);
    const twice = roundTrip(parseSpec(once, raw.view), viewText);
    expect(twice).toEqual(once);
  });

  it("preserves node ids, types, and edge endpoints", () => {
    const out = roundTrip(spec, viewText);
    expect(out.nodes.map((n) => n.id).sort()).toEqual(spec.nodes.map((n) => n.id).sort());
    expect(out.nodes.map((n) => n.type).sort()).toEqual(spec.nodes.map((n) => n.type).sort());
    const edgeKey = (e: Spec["edges"][number]) =>
      `${e.source}.${e.sourceHandle}->${e.target}.${e.targetHandle}`;
    expect(out.edges.map(edgeKey).sort()).toEqual(spec.edges.map(edgeKey).sort());
  });
});
