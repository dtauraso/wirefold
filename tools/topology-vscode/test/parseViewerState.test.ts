// Locks the H5 (per-field tolerance) and H8 (canonical vs legacy camera)
// behaviour of parseViewerState. Sub-fields that fail validation are
// dropped with a console.warn while their valid siblings are preserved;
// top-level non-object or unparseable JSON falls back to defaults.

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { isLegacyCamera, parseViewerState } from "../src/webview/state/viewer/types";

beforeEach(() => {
  vi.spyOn(console, "warn").mockImplementation(() => {});
});
afterEach(() => {
  vi.restoreAllMocks();
});

describe("parseViewerState", () => {
  it("undefined input returns defaults", () => {
    expect(parseViewerState(undefined)).toEqual({});
  });

  it("unparseable JSON returns defaults", () => {
    expect(parseViewerState("{not json")).toEqual({});
  });

  it("non-object top-level returns defaults", () => {
    expect(parseViewerState("[]")).toEqual({});
    expect(parseViewerState("42")).toEqual({});
  });

  it("canonical camera is preserved", () => {
    const v = parseViewerState(JSON.stringify({
      camera: { x: 10, y: 20, zoom: 1.5 },
    }));
    expect(v.camera).toEqual({ x: 10, y: 20, zoom: 1.5 });
    expect(v.camera && isLegacyCamera(v.camera)).toBe(false);
  });

  it("transitional sidecar with both zoom and zeroed w/h parses as canonical", () => {
    const v = parseViewerState(JSON.stringify({
      camera: { x: 1, y: 2, w: 0, h: 0, zoom: 1 },
    }));
    // The canonical branch wins, so the divide-by-zero w/h is ignored.
    expect(v.camera).toEqual({ x: 1, y: 2, zoom: 1 });
    expect(v.camera && isLegacyCamera(v.camera)).toBe(false);
  });

  it("legacy {x,y,w,h} camera is preserved as legacy when zoom is absent", () => {
    const v = parseViewerState(JSON.stringify({
      camera: { x: 0, y: 0, w: 100, h: 50 },
    }));
    expect(v.camera).toEqual({ x: 0, y: 0, w: 100, h: 50 });
    expect(v.camera && isLegacyCamera(v.camera)).toBe(true);
  });

  it("camera with zero w/h and no zoom is rejected (no divide-by-zero)", () => {
    const v = parseViewerState(JSON.stringify({
      camera: { x: 0, y: 0, w: 0, h: 0 },
    }));
    expect(v.camera).toBeUndefined();
  });

  it("malformed camera is dropped, valid sibling fields remain", () => {
    const v = parseViewerState(JSON.stringify({
      camera: { x: "no" },
      lastSelectionIds: ["a", "b"],
    }));
    expect(v.camera).toBeUndefined();
    expect(v.lastSelectionIds).toEqual(["a", "b"]);
  });

  it("folds drops entries with missing required fields", () => {
    const v = parseViewerState(JSON.stringify({
      folds: [
        { id: "f1", label: "L", memberIds: ["a"], position: [0, 0], collapsed: true },
        { id: "f2", label: "L", memberIds: ["a"], position: [0] },         // bad position
        { id: "f3", label: "L", memberIds: ["a"], position: [0, 0], collapsed: "yes" }, // bad collapsed
      ],
    }));
    expect(v.folds).toHaveLength(1);
    expect(v.folds![0].id).toBe("f1");
  });

  it("lastSelectionIds with non-string entry is dropped", () => {
    const v = parseViewerState(JSON.stringify({
      lastSelectionIds: ["a", 1, "c"],
    }));
    expect(v.lastSelectionIds).toBeUndefined();
  });
});
