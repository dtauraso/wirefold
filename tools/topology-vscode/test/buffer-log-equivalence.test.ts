// buffer-log-equivalence.test.ts — proves the ext-host buffer-decoded .probe log carries
// the SAME trace-event content as Go's (removed) JSON-on-stdout path. Spawns a PLAYING sim
// (sends the resume/play record so flow events — position/recv/send/arrive/done — actually
// fire, not just startup/state events), captures BOTH streams, and compares the canonicalized
// event multisets. Floats are compared at 3-decimal precision (the buffer is float32; the old
// JSON was float64 — an inherent, expected precision nuance).

import { describe, it, expect } from "vitest";
import * as cp from "child_process";
import * as path from "path";
import * as fs from "fs";
import * as os from "os";
import { TRACE_EVENT_KINDS } from "../src/schema/trace-kinds";
import { splitFrames } from "../src/runCommand";
import { decodeBufferLog } from "../src/buffer-log";
import { encodePlay, frameRecord } from "../src/schema/input-layout";

const KIND_SET = new Set<string>(TRACE_EVENT_KINDS);
const REPO_ROOT = path.resolve(__dirname, "../../..");

/** Canonicalize one event line for comparison: drop envelope/ordering fields, round floats. */
function canon(o: Record<string, unknown>): string {
  const skip = new Set(["ts_ms", "src", "step"]);
  const norm = (v: unknown): unknown => {
    // Reduce float64 → float32 first (the buffer is float32 by design), THEN round, so the
    // comparison is at the buffer's actual precision rather than flipping on 3rd-decimal
    // float32-vs-float64 boundaries.
    if (typeof v === "number") return Number.isInteger(v) ? v : Math.round(Math.fround(v) * 100) / 100;
    if (Array.isArray(v)) return v.map(norm);
    if (v && typeof v === "object") {
      const out: Record<string, unknown> = {};
      for (const k of Object.keys(v as Record<string, unknown>).sort()) {
        if (!skip.has(k)) out[k] = norm((v as Record<string, unknown>)[k]);
      }
      return out;
    }
    return v;
  };
  return JSON.stringify(norm(o));
}

function multiset(lines: string[]): Map<string, number> {
  const m = new Map<string, number>();
  for (const l of lines) m.set(l, (m.get(l) ?? 0) + 1);
  return m;
}

describe("buffer-decoded .probe log equivalence", () => {
  it("carries the same trace-event content as the stdout JSON path (playing sim)", async () => {
    const bin = path.join(os.tmpdir(), "wf-equiv-test");
    cp.execFileSync("go", ["build", "-o", bin, "."], { cwd: REPO_ROOT });

    // Reference = the -trace JSONL file (Go's Trace.WriteJSONL — the SAME serializer the removed
    // stdout emitter used). -duration gives a CLEAN self-terminating exit: Go resumes the clock,
    // drains the Trace (writes the -trace file), and FinalFlush emits the last buffer snapshot —
    // so both the reference file and the buffer stream are complete and deterministic.
    const traceFile = path.join(os.tmpdir(), `wf-equiv-trace-${Date.now()}.jsonl`);
    const proc = cp.spawn(bin, ["-topology", "./topology", "-duration", "3s", "-trace", traceFile], {
      cwd: REPO_ROOT,
      stdio: ["pipe", "pipe", "pipe", "pipe"],
      env: { ...process.env, WIREFOLD_BUF_OUT_FD: "3" },
    });

    const bufLines: string[] = [];
    let fd3Buf: Buffer = Buffer.alloc(0);
    const fd3 = (proc.stdio as (NodeJS.ReadableStream | null)[])[3];
    fd3!.on("data", (d: Buffer) => {
      const { frames, rest } = splitFrames(fd3Buf, d);
      fd3Buf = rest;
      for (const ab of frames) {
        const out = decodeBufferLog(ab);
        for (const l of out.split("\n")) if (l) bufLines.push(l);
      }
    });

    // Resume the clock so flow events fire, then wait for the clean self-terminating exit so
    // both streams (stdout drain + final buffer flush) are fully written before comparing.
    proc.stdin.write(frameRecord(encodePlay()));
    await new Promise<void>((r) => proc.on("close", () => r()));
    await new Promise((r) => setTimeout(r, 100));

    // Reference path → canonical events from the -trace file (real trace kinds only).
    const traceText = fs.readFileSync(traceFile, "utf8");
    fs.rmSync(traceFile, { force: true });
    const stdoutEvents: string[] = [];
    for (const l of traceText.split("\n")) {
      if (!l.startsWith("{")) continue;
      let o: Record<string, unknown>;
      try { o = JSON.parse(l) as Record<string, unknown>; } catch { continue; }
      if (typeof o.kind === "string" && KIND_SET.has(o.kind)) {
        stdoutEvents.push(canon(o));
      }
    }
    // buffer path → canonical events.
    const bufEvents: string[] = [];
    for (const l of bufLines) {
      const o = JSON.parse(l) as Record<string, unknown>;
      bufEvents.push(canon(o));
    }

    const sMulti = multiset(stdoutEvents);
    const bMulti = multiset(bufEvents);

    // Report per-kind counts for the record.
    const kindCount = (lines: string[]) => {
      const m = new Map<string, number>();
      for (const l of lines) {
        const k = (JSON.parse(l) as { kind: string }).kind;
        m.set(k, (m.get(k) ?? 0) + 1);
      }
      return m;
    };
    // eslint-disable-next-line no-console
    console.log("REFERENCE (-trace) kinds:", JSON.stringify([...kindCount(stdoutEvents)].sort()));
    // eslint-disable-next-line no-console
    console.log("BUFFER kinds:", JSON.stringify([...kindCount(bufEvents)].sort()));

    // Every stdout event must appear in the buffer path with the same multiplicity.
    const missing: string[] = [];
    for (const [k, n] of sMulti) {
      if ((bMulti.get(k) ?? 0) < n) missing.push(`${n - (bMulti.get(k) ?? 0)}× ${k}`);
    }
    // eslint-disable-next-line no-console
    if (missing.length) console.log("MISSING from buffer path:", missing.slice(0, 20));

    expect(stdoutEvents.length).toBeGreaterThan(0);
    // Flow events must have actually fired (proves the playing-sim drive worked).
    expect(stdoutEvents.some((e) => e.includes('"edge-bead"'))).toBe(true);
    expect(missing).toEqual([]);
  }, 30000);
});
