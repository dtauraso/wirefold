// splitFrames is the pure length-prefix framing step for Go's fd3 binary side
// channel. Frames are [len:u32-LE][payload bytes]. These tests lock the three
// framing cases mirroring splitJsonlLines: reassembly across chunks, multiple
// frames per chunk, and a trailing partial buffered until enough bytes arrive.

import { describe, it, expect } from "vitest";
import { splitFrames } from "../src/runCommand";

/** Build a framed Buffer: [len:u32-LE][payload bytes]. */
function frame(payload: number[]): Buffer {
  const buf = Buffer.alloc(4 + payload.length);
  buf.writeUInt32LE(payload.length, 0);
  for (let i = 0; i < payload.length; i++) buf[4 + i] = payload[i];
  return buf;
}

describe("splitFrames", () => {
  it("reassembles a frame split across two chunks", () => {
    const full = frame([0xaa, 0xbb, 0xcc]);
    // Split after the 4-byte header
    const chunk1 = full.slice(0, 4);
    const chunk2 = full.slice(4);

    const a = splitFrames(Buffer.alloc(0), chunk1);
    expect(a.frames).toHaveLength(0);
    expect(a.rest).toHaveLength(4);

    const b = splitFrames(a.rest, chunk2);
    expect(b.frames).toHaveLength(1);
    expect(b.rest).toHaveLength(0);
    const view = new DataView(b.frames[0]);
    expect(view.getUint8(0)).toBe(0xaa);
    expect(view.getUint8(1)).toBe(0xbb);
    expect(view.getUint8(2)).toBe(0xcc);
  });

  it("emits every complete frame when multiple arrive in one chunk", () => {
    const combined = Buffer.concat([frame([1, 2]), frame([3, 4, 5])]);
    const { frames, rest } = splitFrames(Buffer.alloc(0), combined);
    expect(frames).toHaveLength(2);
    expect(rest).toHaveLength(0);
    expect(new DataView(frames[0]).getUint8(0)).toBe(1);
    expect(new DataView(frames[1]).getUint8(0)).toBe(3);
  });

  it("buffers a trailing partial (incomplete header) until it is completed", () => {
    const full = frame([0xde, 0xad]);
    // Only send 2 bytes of the 4-byte header
    const first = splitFrames(Buffer.alloc(0), full.slice(0, 2));
    expect(first.frames).toHaveLength(0);
    expect(first.rest).toHaveLength(2);

    const second = splitFrames(first.rest, full.slice(2));
    expect(second.frames).toHaveLength(1);
    expect(second.rest).toHaveLength(0);
  });

  it("buffers a trailing partial (header complete but payload incomplete)", () => {
    const full = frame([10, 20, 30]);
    // Send header + 1 byte of payload (missing last 2 bytes)
    const first = splitFrames(Buffer.alloc(0), full.slice(0, 5));
    expect(first.frames).toHaveLength(0);
    expect(first.rest).toHaveLength(5);

    const second = splitFrames(first.rest, full.slice(5));
    expect(second.frames).toHaveLength(1);
    expect(second.rest).toHaveLength(0);
  });

  it("carries the prior buffer in front of the new chunk", () => {
    const f1 = frame([0x01]);
    const f2 = frame([0x02]);
    // Deliver f1 split at byte 3, then the rest + f2
    const first = splitFrames(Buffer.alloc(0), f1.slice(0, 3));
    expect(first.frames).toHaveLength(0);

    const { frames, rest } = splitFrames(first.rest, Buffer.concat([f1.slice(3), f2]));
    expect(frames).toHaveLength(2);
    expect(rest).toHaveLength(0);
    expect(new DataView(frames[0]).getUint8(0)).toBe(0x01);
    expect(new DataView(frames[1]).getUint8(0)).toBe(0x02);
  });

  it("handles a zero-length payload frame", () => {
    const f = frame([]);
    const { frames, rest } = splitFrames(Buffer.alloc(0), f);
    expect(frames).toHaveLength(1);
    expect(frames[0].byteLength).toBe(0);
    expect(rest).toHaveLength(0);
  });
});
