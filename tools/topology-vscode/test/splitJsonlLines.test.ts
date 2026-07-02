// splitJsonlLines is the pure newline-framing step extracted from
// BuildAndRunRunner.handleStdout. Go writes newline-delimited JSONL to stdout; a chunk
// boundary can fall anywhere, so a single trace line may arrive split across two
// chunks. These tests lock the three framing cases: reassembly across chunks, multiple
// lines in one chunk, and a trailing partial buffered until its newline arrives.

import { describe, it, expect } from "vitest";
import { splitJsonlLines } from "../src/runCommand";

describe("splitJsonlLines", () => {
  it("reassembles a line split across two chunks into one complete line", () => {
    const a = splitJsonlLines("", '{"step":1,"ki');
    expect(a.lines).toEqual([]);
    expect(a.rest).toBe('{"step":1,"ki');

    const b = splitJsonlLines(a.rest, 'nd":"fire"}\n');
    expect(b.lines).toEqual(['{"step":1,"kind":"fire"}']);
    expect(b.rest).toBe("");
  });

  it("emits every complete line when multiple arrive in one chunk", () => {
    const { lines, rest } = splitJsonlLines("", "a\nb\nc\n");
    expect(lines).toEqual(["a", "b", "c"]);
    expect(rest).toBe("");
  });

  it("buffers a trailing partial line until it is completed", () => {
    const first = splitJsonlLines("", "a\nb\npar");
    expect(first.lines).toEqual(["a", "b"]);
    expect(first.rest).toBe("par");

    const second = splitJsonlLines(first.rest, "tial\n");
    expect(second.lines).toEqual(["partial"]);
    expect(second.rest).toBe("");
  });

  it("carries the prior buffer in front of the new chunk", () => {
    const { lines, rest } = splitJsonlLines("head", "-tail\nnext");
    expect(lines).toEqual(["head-tail"]);
    expect(rest).toBe("next");
  });
});
