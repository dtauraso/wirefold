// Cross-language contract: Go Trace.Event JSON tags ↔ buffer-log.ts's decodeEventLine field
// reads. Go's JSON-on-stdout trace path was removed; the live decoder is buffer-log.ts's
// decodeEventLine, which reads the buffer EVENT block and emits the SAME field shape the
// removed stdout path used to. A Go json-tag rename on the (still-live, file-based -trace)
// serializer passes `go build` / `go test` but could silently drift from what
// decodeEventLine's switch expects. This test pins the exact field shape.
//
// Fixture: test/fixtures/trace-events.jsonl — one representative event
// per `kind` variant, hand-curated to match Go Trace.marshalEvent output.
// Regen when Go adds a new kind: add one line to the fixture and one
// assertion here.

import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import type { DecodedEventLine } from "../../src/buffer-log";
import { TRACE_EVENT_KINDS } from "../../src/schema/trace-kinds";

const FIXTURE = join(__dirname, "../fixtures/trace-events.jsonl");

function loadEvents(): DecodedEventLine[] {
  return readFileSync(FIXTURE, "utf8")
    .split("\n")
    .filter((l) => l.trim() !== "")
    .map((l) => JSON.parse(l) as DecodedEventLine);
}

describe("trace-event-fields contract", () => {
  const events = loadEvents();

  it("fixture has one event for each kind variant", () => {
    const kinds = new Set(events.map((e) => e.kind));
    expect(kinds).toEqual(new Set(["recv", "fire", "send", "done", "edge-bead", "geometry", "pulse-cancelled", "node-geometry", "arrive", "node-bead", "camera", "scene-tori", "scene-poles", "node-poles", "sel-sphere-poles", "handholds", "labels-global", "overlays-vis", "double-links", "select", "fade", "hover", "scene-sphere", "layout-link", "halted"]));
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
    expect(typeof (e as Extract<DecodedEventLine, { kind: "recv" }> & { port?: string }).port).toBe("string");
    expect(typeof (e as Extract<DecodedEventLine, { kind: "recv" }> & { value?: number }).value).toBe("number");
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
    expect(typeof (e as Extract<DecodedEventLine, { kind: "done" }> & { port?: string }).port).toBe("string");
  });

  it("edge-bead event has step, kind, node, port, x, y, z, f (Phase 2)", () => {
    const e = events.find((ev) => ev.kind === "edge-bead")! as Extract<DecodedEventLine, { kind: "edge-bead" }>;
    expect(typeof e.step).toBe("number");
    expect(e.kind).toBe("edge-bead");
    expect(typeof e.node).toBe("string");
    expect(typeof e.port).toBe("string");
    expect(typeof e.x).toBe("number");
    expect(typeof e.y).toBe("number");
    expect(typeof e.z).toBe("number");
    expect(typeof e.f).toBe("number");
  });

  it("geometry event has step, kind, edge, and six segment-endpoint coords (Phase 3)", () => {
    const e = events.find((ev) => ev.kind === "geometry")! as Extract<DecodedEventLine, { kind: "geometry" }>;
    expect(typeof e.step).toBe("number");
    expect(e.kind).toBe("geometry");
    expect(typeof e.edge).toBe("string");
    for (const key of ["sx", "sy", "sz", "ex", "ey", "ez"] as const) {
      expect(typeof e[key]).toBe("number");
    }
  });

  it("pulse-cancelled event has step, kind, node, port (Phase 3)", () => {
    const e = events.find((ev) => ev.kind === "pulse-cancelled")! as Extract<DecodedEventLine, { kind: "pulse-cancelled" }>;
    expect(typeof e.step).toBe("number");
    expect(e.kind).toBe("pulse-cancelled");
    expect(typeof e.node).toBe("string");
    expect(typeof e.port).toBe("string");
  });

  it("arrive event has step, kind, node, port", () => {
    const e = events.find((ev) => ev.kind === "arrive")!;
    expect(typeof e.step).toBe("number");
    expect(e.kind).toBe("arrive");
    expect(typeof e.node).toBe("string");
    expect(typeof (e as Extract<DecodedEventLine, { kind: "arrive" }> & { port?: string }).port).toBe("string");
  });

  it("node-geometry event has step, kind, node, nx, ny, nz, radius, ports", () => {
    const e = events.find((ev) => ev.kind === "node-geometry")! as Extract<DecodedEventLine, { kind: "node-geometry" }>;
    expect(typeof e.step).toBe("number");
    expect(e.kind).toBe("node-geometry");
    expect(typeof e.node).toBe("string");
    for (const key of ["nx", "ny", "nz", "radius"] as const) {
      expect(typeof e[key]).toBe("number");
    }
    expect(Array.isArray(e.ports)).toBe(true);
  });

  it("node-bead event has step, kind, node, row, col, value, x, y, z (Phase 2b)", () => {
    const e = events.find((ev) => ev.kind === "node-bead")! as Extract<DecodedEventLine, { kind: "node-bead" }>;
    expect(typeof e.step).toBe("number");
    expect(e.kind).toBe("node-bead");
    expect(typeof e.node).toBe("string");
    expect(typeof e.row).toBe("number");
    expect(typeof e.col).toBe("number");
    expect(typeof e.value).toBe("number");
    expect(typeof e.x).toBe("number");
    expect(typeof e.y).toBe("number");
    expect(typeof e.z).toBe("number");
  });

  it("scene-sphere event has step, kind, cx, cy, cz, radius", () => {
    const e = events.find((ev) => ev.kind === "scene-sphere")! as Extract<DecodedEventLine, { kind: "scene-sphere" }>;
    expect(typeof e.step).toBe("number");
    expect(e.kind).toBe("scene-sphere");
    expect(typeof e.cx).toBe("number");
    expect(typeof e.cy).toBe("number");
    expect(typeof e.cz).toBe("number");
    expect(typeof e.radius).toBe("number");
  });

});
