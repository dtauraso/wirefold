// decodeSnapshotEvents reads the transient per-node event columns from a binary
// snapshot payload and returns human-readable log lines. These tests lock the
// decode against a hand-crafted known snapshot buffer.

import { describe, it, expect } from "vitest";
import { decodeSnapshotEvents } from "../src/runCommand";
import {
  BUF_HEADER_SIZE,
  BEAD_STRIDE,
  NODE_STRIDE,
  NODE_COL_EV_RECV,
  NODE_COL_EV_FIRE,
  NODE_COL_EV_SEND,
  NODE_COL_EV_ARRIVE,
  NODE_COL_EV_DONE,
} from "../src/schema/buffer-layout";

/**
 * Build a minimal snapshot ArrayBuffer with the given node event flags.
 * nodeEvents: array of {recv,fire,send,arrive,done} booleans per node.
 * No beads, no edges, no camera, no overlay — just header + node block.
 */
function makeSnapshot(nodeEvents: { recv?: boolean; fire?: boolean; send?: boolean; arrive?: boolean; done?: boolean }[]): ArrayBuffer {
  const tick = 42;
  const beadCount = 0;
  const nodeCount = nodeEvents.length;
  const edgeCount = 0;
  const totalSize = BUF_HEADER_SIZE + beadCount * BEAD_STRIDE + nodeCount * NODE_STRIDE;
  const buf = new ArrayBuffer(totalSize);
  const view = new DataView(buf);
  view.setUint32(0, tick, true);
  view.setUint32(4, beadCount, true);
  view.setUint32(8, nodeCount, true);
  view.setUint32(12, edgeCount, true);

  const nodeBlockOff = BUF_HEADER_SIZE + beadCount * BEAD_STRIDE;
  for (let i = 0; i < nodeCount; i++) {
    const rowOff = nodeBlockOff + i * NODE_STRIDE;
    const ev = nodeEvents[i];
    view.setUint8(rowOff + NODE_COL_EV_RECV,   ev.recv   ? 1 : 0);
    view.setUint8(rowOff + NODE_COL_EV_FIRE,   ev.fire   ? 1 : 0);
    view.setUint8(rowOff + NODE_COL_EV_SEND,   ev.send   ? 1 : 0);
    view.setUint8(rowOff + NODE_COL_EV_ARRIVE, ev.arrive ? 1 : 0);
    view.setUint8(rowOff + NODE_COL_EV_DONE,   ev.done   ? 1 : 0);
  }
  return buf;
}

describe("decodeSnapshotEvents", () => {
  it("returns empty array when no events are set", () => {
    const snap = makeSnapshot([{ }, { }]);
    expect(decodeSnapshotEvents(snap)).toHaveLength(0);
  });

  it("returns one line per set event flag", () => {
    const snap = makeSnapshot([{ recv: true, fire: true }, { send: true }]);
    const lines = decodeSnapshotEvents(snap);
    expect(lines).toHaveLength(3);
  });

  it("each line is valid JSONL with src=buf, kind=buf-event, node index, event name", () => {
    const snap = makeSnapshot([{ fire: true }]);
    const lines = decodeSnapshotEvents(snap);
    expect(lines).toHaveLength(1);
    const obj = JSON.parse(lines[0].trim()) as Record<string, unknown>;
    expect(obj.src).toBe("buf");
    expect(obj.kind).toBe("buf-event");
    expect(obj.event).toBe("fire");
    expect(obj.node).toBe(0);
    expect(obj.tick).toBe(42);
    expect(typeof obj.ts_ms).toBe("number");
  });

  it("decodes arrive and done flags", () => {
    const snap = makeSnapshot([{ arrive: true, done: true }]);
    const lines = decodeSnapshotEvents(snap);
    expect(lines).toHaveLength(2);
    const events = lines.map((l) => (JSON.parse(l.trim()) as Record<string, unknown>).event);
    expect(events).toContain("arrive");
    expect(events).toContain("done");
  });

  it("returns empty array for a buffer shorter than the header", () => {
    const buf = new ArrayBuffer(8); // shorter than BUF_HEADER_SIZE (16)
    expect(decodeSnapshotEvents(buf)).toHaveLength(0);
  });

  it("includes events from multiple nodes with correct node indices", () => {
    const snap = makeSnapshot([{}, { recv: true }, { fire: true }]);
    const lines = decodeSnapshotEvents(snap);
    expect(lines).toHaveLength(2);
    const objs = lines.map((l) => JSON.parse(l.trim()) as Record<string, unknown>);
    const nodeIndices = objs.map((o) => o.node);
    expect(nodeIndices).toContain(1);
    expect(nodeIndices).toContain(2);
  });
});
