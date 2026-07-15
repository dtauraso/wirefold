// parseHostToWebview payload-validation tests. A malformed host→webview message
// (esp. trace-event with a missing/malformed event) must be dropped (undefined) so
// it can never reach pump.ts and throw, blanking the editor.
import { describe, it, expect } from "vitest";
import { parseHostToWebview } from "../src/messages";

describe("parseHostToWebview", () => {
  it("accepts a well-formed trace-event envelope", () => {
    const msg = { type: "trace-event", event: { step: 3, kind: "fire", node: "n1" } };
    expect(parseHostToWebview(msg)).toEqual(msg);
  });

  it("rejects trace-event with a missing event", () => {
    expect(parseHostToWebview({ type: "trace-event" })).toBeUndefined();
  });

  it("rejects trace-event with a null event", () => {
    expect(parseHostToWebview({ type: "trace-event", event: null })).toBeUndefined();
  });

  it("rejects trace-event whose event lacks a numeric step", () => {
    expect(
      parseHostToWebview({ type: "trace-event", event: { kind: "fire" } }),
    ).toBeUndefined();
  });

  it("rejects trace-event whose event lacks a string kind", () => {
    expect(
      parseHostToWebview({ type: "trace-event", event: { step: 1 } }),
    ).toBeUndefined();
  });

  it("rejects the removed load type (the render path is buffer-only; no spec/scene load message)", () => {
    expect(parseHostToWebview({ type: "load", text: "{}", sceneText: "{}" })).toBeUndefined();
  });

  it("accepts run-status with a documented state", () => {
    const msg = { type: "run-status", state: "running" };
    expect(parseHostToWebview(msg)).toEqual(msg);
  });

  it("rejects run-status with an unknown state", () => {
    expect(parseHostToWebview({ type: "run-status", state: "bogus" })).toBeUndefined();
  });

  it("rejects the removed save-error type (nothing posts it; Go persists its own scene state)", () => {
    expect(parseHostToWebview({ type: "save-error", message: "x" })).toBeUndefined();
  });

  it("rejects the removed flush type (Go persists its own scene state; nothing to flush)", () => {
    expect(parseHostToWebview({ type: "flush" })).toBeUndefined();
  });

  it("rejects the removed id/label sidecar type (labels now ride the binary buffer)", () => {
    expect(parseHostToWebview({ type: "node-label", id: "n1", label: "Source" })).toBeUndefined();
  });

  it("rejects an unknown message type", () => {
    expect(parseHostToWebview({ type: "nope" })).toBeUndefined();
  });
});
