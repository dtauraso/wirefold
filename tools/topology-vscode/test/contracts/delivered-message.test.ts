// delivered-message.test.ts — contract test for the webview→host "delivered" message.
//
// Go's stdin reader expects: {"type":"delivered","edge":"<edge-label>"}
// This test ensures the TS-side parser accepts that shape and rejects bad ones.

import { describe, expect, it } from "vitest";
import { parseWebviewToHost, WEBVIEW_TO_HOST_TYPES } from "../../src/messages";

describe("delivered message contract", () => {
  it("parses a valid delivered message", () => {
    const msg = parseWebviewToHost({ type: "delivered", edge: "in08ToReadGate1" });
    expect(msg).toEqual({ type: "delivered", edge: "in08ToReadGate1" });
  });

  it("rejects delivered with non-string edge", () => {
    const msg = parseWebviewToHost({ type: "delivered", edge: 42 });
    expect(msg).toBeUndefined();
  });

  it("rejects delivered with missing edge", () => {
    const msg = parseWebviewToHost({ type: "delivered" });
    expect(msg).toBeUndefined();
  });

  it("delivered type is in WEBVIEW_TO_HOST_TYPES", () => {
    expect(WEBVIEW_TO_HOST_TYPES.has("delivered")).toBe(true);
  });
});
