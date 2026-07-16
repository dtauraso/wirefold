// @vitest-environment jsdom
//
// camera-ui.tsx — plain-DOM widgets (HomeButton, OverlaysControl). No react-three-fiber
// render involved: both are absolutely-positioned <div>s: HomeButton only reads a
// THREE.PerspectiveCamera ref's plain numeric fields (fov), it never mounts a canvas.

import * as THREE from "three";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, fireEvent, cleanup } from "@testing-library/react";
import { encodeOverlaysToggle } from "../src/schema/input-layout";

const postMessage = vi.fn();
(globalThis as unknown as { acquireVsCodeApi: () => unknown }).acquireVsCodeApi = () => ({
  postMessage,
  setState: () => {},
  getState: () => ({}),
});

const { HomeButton, OverlaysControl } = await import("../src/webview/three/camera-ui");

function arrayBuffersEqual(a: ArrayBuffer, b: ArrayBuffer): boolean {
  if (a.byteLength !== b.byteLength) return false;
  const av = new Uint8Array(a);
  const bv = new Uint8Array(b);
  for (let i = 0; i < av.length; i++) if (av[i] !== bv[i]) return false;
  return true;
}

beforeEach(() => {
  postMessage.mockClear();
});

afterEach(() => {
  cleanup();
});

describe("HomeButton", () => {
  it("clicking posts a go-record raw-input home event derived from the camera's fov/aspect", () => {
    const cam = new THREE.PerspectiveCamera(55, 1, 0.1, 100);
    const cameraRef = { current: cam };
    const { getByTitle } = render(<HomeButton cameraRef={cameraRef} aspect={1.5} />);
    fireEvent.click(getByTitle("Fit diagram in view"));
    expect(postMessage).toHaveBeenCalledTimes(1);
    const sent = postMessage.mock.calls[0][0] as { type: string; record: ArrayBuffer };
    expect(sent.type).toBe("go-record");
    expect(sent.record).toBeInstanceOf(ArrayBuffer);
    expect(sent.record.byteLength).toBeGreaterThan(0);
  });

  it("does nothing when the camera ref is not yet mounted", () => {
    const cameraRef = { current: null };
    const { getByTitle } = render(<HomeButton cameraRef={cameraRef} aspect={1.5} />);
    fireEvent.click(getByTitle("Fit diagram in view"));
    expect(postMessage).not.toHaveBeenCalled();
  });
});

describe("OverlaysControl", () => {
  it("renders the master toggle active by default (no snapshot yet) and posts an overlays toggle go-record on click", () => {
    const { getByTitle } = render(<OverlaysControl />);
    fireEvent.click(getByTitle("Hide overlays"));
    // fireToggle also posts a "webview-log" entry (postLog) before the go-record; find the
    // go-record call specifically rather than asserting a call count.
    const calls = postMessage.mock.calls as Array<[{ type: string; record?: ArrayBuffer }]>;
    const goRecordCalls = calls.filter(([m]) => m.type === "go-record");
    expect(goRecordCalls).toHaveLength(1);
    const sent = goRecordCalls[0][0] as { type: string; record: ArrayBuffer };
    expect(arrayBuffersEqual(sent.record, encodeOverlaysToggle("overlays"))).toBe(true);
  });

  it("caret click opens the popover; clicking a row posts that row's flag toggle", () => {
    const { getByTitle, getByText } = render(<OverlaysControl />);
    fireEvent.click(getByTitle("Open overlay list"));
    const ringsRow = getByText("◎ rings");
    fireEvent.click(ringsRow);
    const calls = postMessage.mock.calls as Array<[{ type: string; record?: ArrayBuffer }]>;
    const goRecordCalls = calls.filter(([m]) => m.type === "go-record");
    expect(goRecordCalls).toHaveLength(1);
    const sent = goRecordCalls[0][0] as { type: string; record: ArrayBuffer };
    expect(arrayBuffersEqual(sent.record, encodeOverlaysToggle("tori"))).toBe(true);
  });
});
