// Cross-language contract: Go Trace.Event JSON tags ↔ pump.ts field reads.
//
// Go emits JSONL trace events; pump.ts reads them by string key. A Go
// json-tag rename passes `go build` / `go test` but silently breaks the
// pump. This test pins the exact fields pump.ts reads so any rename is
// caught at `npm test` time.
//
// Fixture: test/fixtures/trace-events.jsonl — one representative event
// per `kind` variant, hand-curated to match Go Trace.marshalEvent output.
// Regen when Go adds a new kind: add one line to the fixture and one
// assertion here.

import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import type { TraceEvent } from "../../src/messages";
import { TRACE_EVENT_KINDS } from "../../src/webview/three/trace-kinds";

const FIXTURE = join(__dirname, "../fixtures/trace-events.jsonl");

function loadEvents(): TraceEvent[] {
  return readFileSync(FIXTURE, "utf8")
    .split("\n")
    .filter((l) => l.trim() !== "")
    .map((l) => JSON.parse(l) as TraceEvent);
}

describe("trace-event-fields contract", () => {
  const events = loadEvents();

  it("fixture has one event for each kind variant", () => {
    const kinds = new Set(events.map((e) => e.kind));
    expect(kinds).toEqual(new Set(["recv", "fire", "send", "done", "position", "geometry", "pulse-cancelled", "node-geometry"]));
  });

  it("every fixture event kind is in TRACE_EVENT_KINDS", () => {
    const allowed = new Set<string>(TRACE_EVENT_KINDS);
    for (const e of events) {
      expect(allowed.has(e.kind), `unknown kind "${e.kind}" in fixture`).toBe(true);
    }
  });

  it("TRACE_EVENT_KINDS covers all fixture kinds (no generated kind missing from fixture)", () => {
    const fixtureKinds = new Set(events.map((e) => e.kind));
    for (const k of TRACE_EVENT_KINDS) {
      expect(fixtureKinds.has(k), `TRACE_EVENT_KINDS contains "${k}" but fixture has no such event`).toBe(true);
    }
  });

  it("recv event has step, kind, node, port, value", () => {
    const e = events.find((ev) => ev.kind === "recv")!;
    expect(typeof e.step).toBe("number");
    expect(e.kind).toBe("recv");
    expect(typeof e.node).toBe("string");
    expect(typeof (e as Extract<TraceEvent, { kind: "recv" }> & { port?: string }).port).toBe("string");
    expect(typeof (e as Extract<TraceEvent, { kind: "recv" }> & { value?: number }).value).toBe("number");
  });

  it("fire event has step, kind, node", () => {
    const e = events.find((ev) => ev.kind === "fire")!;
    expect(typeof e.step).toBe("number");
    expect(e.kind).toBe("fire");
    expect(typeof e.node).toBe("string");
  });

  it("send event has step, kind, node, port, value", () => {
    const e = events.find((ev) => ev.kind === "send")!;
    expect(typeof e.step).toBe("number");
    expect(e.kind).toBe("send");
    expect(typeof e.node).toBe("string");
    const asObj = e as Record<string, unknown>;
    expect(typeof asObj["port"]).toBe("string");
    expect(typeof asObj["value"]).toBe("number");
  });

  it("done event has step, kind, node, port", () => {
    const e = events.find((ev) => ev.kind === "done")!;
    expect(typeof e.step).toBe("number");
    expect(e.kind).toBe("done");
    expect(typeof e.node).toBe("string");
    expect(typeof (e as Extract<TraceEvent, { kind: "done" }> & { port?: string }).port).toBe("string");
  });

  it("position event has step, kind, node, port, x, y, z (Phase 2)", () => {
    const e = events.find((ev) => ev.kind === "position")! as Extract<TraceEvent, { kind: "position" }>;
    expect(typeof e.step).toBe("number");
    expect(e.kind).toBe("position");
    expect(typeof e.node).toBe("string");
    expect(typeof e.port).toBe("string");
    expect(typeof e.x).toBe("number");
    expect(typeof e.y).toBe("number");
    expect(typeof e.z).toBe("number");
  });

  it("geometry event has step, kind, edge, and nine control-point coords (Phase 3)", () => {
    const e = events.find((ev) => ev.kind === "geometry")! as Extract<TraceEvent, { kind: "geometry" }>;
    expect(typeof e.step).toBe("number");
    expect(e.kind).toBe("geometry");
    expect(typeof e.edge).toBe("string");
    for (const key of ["p0x", "p0y", "p0z", "p1x", "p1y", "p1z", "p2x", "p2y", "p2z"] as const) {
      expect(typeof e[key]).toBe("number");
    }
  });

  it("pulse-cancelled event has step, kind, node, port (Phase 3)", () => {
    const e = events.find((ev) => ev.kind === "pulse-cancelled")! as Extract<TraceEvent, { kind: "pulse-cancelled" }>;
    expect(typeof e.step).toBe("number");
    expect(e.kind).toBe("pulse-cancelled");
    expect(typeof e.node).toBe("string");
    expect(typeof e.port).toBe("string");
  });
});
