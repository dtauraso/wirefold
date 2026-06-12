// Save/load round-trip contract tests (view-split gate).
//
// Loads the live repo topology.json, and asserts that the view-split
// serialization path (topology.json#view vs topology.scene.json) is correct.
// The flowToSpec round-trip tests were retired when flowToSpec was removed
// (flowToSpec was only used by the run pre-write path, which is now spec-less).

import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import {
  parseViewerState,
  mergeSceneIntoViewerState,
  serializeViewerState,
  serializeSceneState,
  type ViewerState,
} from "../../src/webview/state/viewer/types";
import { injectViewText, parseSceneText, serializeSceneText } from "../../src/sidecar";

const TOPOLOGY_PATH = join(__dirname, "../../../../topology.json");

describe("topology.json view-split contract", () => {
  const raw = JSON.parse(readFileSync(TOPOLOGY_PATH, "utf8"));
  // After the scene split, topology.json#view contains only diagram fields
  // (nodes/positions, directlyFadedNodes, directlyFadedEdges, fadeEdgeOrder).
  // Scene fields (camera, camera3d, labelsGlobalHidden) live in topology.scene.json.

  it("topology.json#view does not contain camera, camera3d, or labelsGlobalHidden", () => {
    if (raw.view && typeof raw.view === "object") {
      const view = raw.view as Record<string, unknown>;
      expect(view).not.toHaveProperty("camera");
      expect(view).not.toHaveProperty("camera3d");
      expect(view).not.toHaveProperty("labelsGlobalHidden");
    }
  });

  it("view-save routes positions+fades to topology.json#view and camera/labels to the scene file", () => {
    // Build a viewer state exercising both halves of the split.
    const vs: ViewerState = {
      nodes: { n1: { x: 42, y: 99, z: 7 }, n2: { x: -3, y: 5 } },
      directlyFadedNodes: ["n1"],
      directlyFadedEdges: ["n1.out->n2.in"],
      fadeEdgeOrder: ["n1.out->n2.in"],
      camera: { x: 1, y: 2, zoom: 1.5 },
      camera3d: { position: [1, 2, 3], quaternion: [0, 0, 0, 1] },
      labelsGlobalHidden: true,
    };

    // SAVE path, mirroring performViewSave + handle-message "view-save":
    //  - diagram text injected into topology.json#view
    //  - scene text written to topology.scene.json (flat)
    const diagramText = serializeViewerState(vs);
    const sceneText = serializeSceneText(parseSceneText(serializeSceneState(vs)));
    const docOnDisk = injectViewText(JSON.stringify({ nodes: [], edges: [] }), diagramText);

    const savedTopology = JSON.parse(docOnDisk);
    const savedView = savedTopology.view as Record<string, unknown>;
    // Positions + fades live in topology.json#view (Go reads view.nodes here).
    expect(savedView.nodes).toEqual(vs.nodes);
    expect(savedView.directlyFadedNodes).toEqual(vs.directlyFadedNodes);
    expect(savedView.directlyFadedEdges).toEqual(vs.directlyFadedEdges);
    expect(savedView.fadeEdgeOrder).toEqual(vs.fadeEdgeOrder);
    // Scene keys are NOT in topology.json#view.
    expect(savedView).not.toHaveProperty("camera");
    expect(savedView).not.toHaveProperty("camera3d");
    expect(savedView).not.toHaveProperty("labelsGlobalHidden");

    // Scene file holds camera/labels, and NOT positions/fades.
    const savedScene = JSON.parse(sceneText);
    expect(savedScene.camera).toEqual(vs.camera);
    expect(savedScene.camera3d).toEqual(vs.camera3d);
    expect(savedScene.labelsGlobalHidden).toBe(true);
    expect(savedScene).not.toHaveProperty("nodes");
    expect(savedScene).not.toHaveProperty("directlyFadedNodes");

    // LOAD path, mirroring store.load: diagram view from topology.json#view,
    // scene merged from the scene file. Positions + fades survive the round-trip.
    const diagramView = parseViewerState(JSON.stringify(savedTopology.view));
    const sceneView = parseViewerState(sceneText);
    const loaded = mergeSceneIntoViewerState(diagramView, sceneView);
    expect(loaded.nodes).toEqual(vs.nodes);
    expect(loaded.directlyFadedNodes).toEqual(vs.directlyFadedNodes);
    expect(loaded.directlyFadedEdges).toEqual(vs.directlyFadedEdges);
    expect(loaded.fadeEdgeOrder).toEqual(vs.fadeEdgeOrder);
    expect(loaded.camera).toEqual(vs.camera);
    expect(loaded.camera3d).toEqual(vs.camera3d);
    expect(loaded.labelsGlobalHidden).toBe(true);
  });

});

