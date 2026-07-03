// tryParseBreadcrumb is the ext-host classifier for the Go DEBUG BREADCRUMB channel:
// a stdout line is a breadcrumb when it is valid JSON with kind="breadcrumb". Such
// lines are routed to .probe/go-debug.jsonl (src="go-debug"), NOT to go-errors.jsonl.
// These tests lock that a marked breadcrumb classifies as a breadcrumb, and that
// trace-event / spec / plain-error lines do NOT.

import { describe, it, expect } from "vitest";
import { tryParseBreadcrumb } from "../src/runCommand";

describe("tryParseBreadcrumb", () => {
  it("classifies a kind=breadcrumb line as a breadcrumb", () => {
    const crumb = tryParseBreadcrumb(
      '{"kind":"breadcrumb","label":"topology-loaded","node":"n7","value":"nodes=3"}',
    );
    expect(crumb).toBeDefined();
    expect(crumb!.kind).toBe("breadcrumb");
    expect(crumb!.label).toBe("topology-loaded");
  });

  it("does NOT classify a trace event as a breadcrumb", () => {
    expect(tryParseBreadcrumb('{"step":1,"kind":"fire","node":"n1"}')).toBeUndefined();
  });

  it("does NOT classify a spec line as a breadcrumb", () => {
    expect(tryParseBreadcrumb('{"kind":"spec","nodes":[],"edges":[]}')).toBeUndefined();
  });

  it("does NOT classify a non-JSON stderr error line as a breadcrumb", () => {
    expect(tryParseBreadcrumb("panic: runtime error: index out of range")).toBeUndefined();
  });

  it("does NOT classify a JSON error envelope as a breadcrumb", () => {
    expect(tryParseBreadcrumb('{"src":"go","kind":"error","message":"boom"}')).toBeUndefined();
  });
});
