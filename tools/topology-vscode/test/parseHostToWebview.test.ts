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

  it("accepts a load with a string text and validates sceneText", () => {
    const msg = { type: "load", text: "{}", sceneText: "{}" };
    expect(parseHostToWebview(msg)).toEqual(msg);
  });

  it("rejects a load whose text is not a string", () => {
    expect(parseHostToWebview({ type: "load", text: 42 })).toBeUndefined();
  });

  it("rejects a load whose sceneText is a non-string", () => {
    expect(
      parseHostToWebview({ type: "load", text: "{}", sceneText: 7 }),
    ).toBeUndefined();
  });

  it("accepts run-status with a documented state", () => {
    const msg = { type: "run-status", state: "running" };
    expect(parseHostToWebview(msg)).toEqual(msg);
  });

  it("rejects run-status with an unknown state", () => {
    expect(parseHostToWebview({ type: "run-status", state: "bogus" })).toBeUndefined();
  });

  it("rejects save-error without a message string", () => {
    expect(parseHostToWebview({ type: "save-error" })).toBeUndefined();
  });

  it("accepts flush with no payload", () => {
    const msg = { type: "flush" };
    expect(parseHostToWebview(msg)).toEqual(msg);
  });

  it("accepts a node-label sidecar with id + label strings", () => {
    const msg = { type: "node-label", id: "n1", label: "Source" };
    expect(parseHostToWebview(msg)).toEqual(msg);
  });

  it("rejects a node-label missing id or label", () => {
    expect(parseHostToWebview({ type: "node-label", id: "n1" })).toBeUndefined();
    expect(parseHostToWebview({ type: "node-label", label: "Source" })).toBeUndefined();
    expect(parseHostToWebview({ type: "node-label", id: 1, label: "Source" })).toBeUndefined();
  });

  it("rejects an unknown message type", () => {
    expect(parseHostToWebview({ type: "nope" })).toBeUndefined();
  });
});
