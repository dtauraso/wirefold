// buffer-log-equivalence.test.ts — proves the ext-host buffer-decoded .probe logs carry
// the SAME trace-event KINDS (per-kind counts) as Go's (removed) JSON-on-stdout path.
// Spawns a PLAYING sim (the clock is free-running, so flow events — position/recv/send/
// arrive — fire from startup), captures every per-owner stream, and compares PER-KIND
// counts — NOT full canonicalized-line equality. This is deliberately loosened from the
// pre-per-owner-streams version of this test (memory/feedback_no_single_writer_bridge.md):
// events now arrive on N different fds (node/edge/interior/view), each resolved
// independently by its own owner goroutine and written to its OWN .probe file (go.jsonl/
// go-node.jsonl/go-edge.jsonl/go-interior.jsonl — never merged on write, see probe-files.ts),
// so cross-owner arrival ORDER is no longer meaningful (it never was causal — see the doc
// note in stream_events.go) and per-owner row-to-label resolution is now best-effort
// rather than guaranteed full-fidelity (a stream-origin event doesn't always have the
// OTHER frames on hand to resolve every string). The KIND, and its total COUNT summed
// ACROSS all the separate per-owner logs, is the invariant this test protects.

import { describe, it, expect } from "vitest";
import * as cp from "child_process";
import * as path from "path";
import * as fs from "fs";
import * as os from "os";
import { TRACE_EVENT_KINDS } from "../src/schema/trace-kinds";
import { splitFrames, countNodes, countEdges } from "../src/runCommand";
import { decodeBufferLog, decodeStreamFrameEvents } from "../src/buffer-log";
import { decodeNodeStreamFrame, decodeEdgeStreamFrame, decodeInteriorStreamFrame } from "../src/webview/three/buffer-decode";
import { BUF_BLOCK_TAG_NODE, BUF_BLOCK_TAG_EDGE } from "../src/schema/frame-tags";

const KIND_SET = new Set<string>(TRACE_EVENT_KINDS);
const REPO_ROOT = path.resolve(__dirname, "../../..");

// Mirrors runCommand.ts's own fd-allocation contract (memory/feedback_no_single_writer_bridge.md,
// Buffer/stream_fds.go): VIEW_FD, then one fd per edge row, then one NODE + one INTERIOR fd
// per node row. This test wires the SAME layout so every decentralized kind (NodeGeometry/
// Geometry/Position/Arrive/NodeBead) actually has an owner fd to stream on — the fallback
// (WIREFOLD_STREAM_FDS unset) drops the EVENT block ENTIRELY now (acceptable — the fallback
// packer is headless-only and is being deleted next commit; see Buffer/pack.go).
const VIEW_FD = 4;
const EDGE_BASE_FD = 5;

function kindCounts(lines: string[]): Map<string, number> {
  const m = new Map<string, number>();
  for (const l of lines) {
    if (!l) continue;
    const k = (JSON.parse(l) as { kind: string }).kind;
    m.set(k, (m.get(k) ?? 0) + 1);
  }
  return m;
}

describe("buffer-decoded .probe logs equivalence", () => {
  it("carry the same trace-event KINDS (per-kind counts, summed across the N separate per-owner logs) as the stdout JSON path (playing sim)", async () => {
    const bin = path.join(os.tmpdir(), "wf-equiv-test");
    cp.execFileSync("go", ["build", "-o", bin, "."], { cwd: REPO_ROOT });

    const topologyPath = path.join(REPO_ROOT, "topology");
    const edgeCount = countEdges(topologyPath);
    const nodeCount = countNodes(topologyPath);
    const nodeBaseFd = EDGE_BASE_FD + edgeCount;
    const interiorBaseFd = nodeBaseFd + nodeCount;

    // Build the SAME per-owner stdio layout runCommand.ts's BuildAndRunRunner wires in
    // production (memory/feedback_no_single_writer_bridge.md): fd 3 (scene, now carries
    // no EVENT block), VIEW_FD, one fd per edge row, then one NODE + one INTERIOR fd per
    // node row.
    const stdio: (string | number)[] = ["pipe", "pipe", "pipe", "pipe", "pipe"];
    for (let i = 0; i < edgeCount; i++) stdio.push("pipe");
    // ALL node fds first, THEN all interior fds (matches runCommand.ts's own construction
    // exactly — nodeBaseFd = EDGE_BASE_FD+edgeCount, interiorBaseFd = nodeBaseFd+nodeCount;
    // interleaving them here would misalign every fd index past the first node).
    for (let i = 0; i < nodeCount; i++) stdio.push("pipe");
    for (let i = 0; i < nodeCount; i++) stdio.push("pipe");
    const streamFDsEnv = [
      `view:${VIEW_FD}`,
      edgeCount > 0 ? `edge:${EDGE_BASE_FD}` : "",
      nodeCount > 0 ? `node:${nodeBaseFd}` : "",
      nodeCount > 0 ? `interior:${interiorBaseFd}` : "",
    ].filter(Boolean).join(",");

    // Reference = the -trace JSONL file (Go's Trace.WriteJSONL — the SAME serializer the removed
    // stdout emitter used). -duration gives a CLEAN self-terminating exit: Go runs free,
    // drains the Trace (writes the -trace file), and FinalFlush emits the last buffer snapshot —
    // so both the reference file and the buffer streams are complete and deterministic.
    const traceFile = path.join(os.tmpdir(), `wf-equiv-trace-${Date.now()}.jsonl`);
    const proc = cp.spawn(bin, ["-topology", "./topology", "-duration", "3s", "-trace", traceFile], {
      cwd: REPO_ROOT,
      stdio,
      env: { ...process.env, WIREFOLD_BUF_OUT_FD: "3", WIREFOLD_STREAM_FDS: streamFDsEnv },
    });

    // Four SEPARATE in-memory logs, mirroring the four SEPARATE .probe files runCommand.ts
    // writes (go.jsonl / go-node.jsonl / go-edge.jsonl / go-interior.jsonl) — never merged
    // while being written; only this test's OWN verification step sums their kind counts.
    const viewLines: string[] = [];
    const nodeLines: string[] = [];
    const edgeLines: string[] = [];
    const interiorLines: string[] = [];
    const pushLines = (arr: string[], out: string) => { for (const l of out.split("\n")) if (l) arr.push(l); };

    // NODE/EDGE frames (fd 3, fallback tags) are cached only so a VIEW-bucket event can
    // resolve a node/port/edge string — the fallback tags never appear on fd 3 once the
    // dedicated streams are active (as here), so these simply stay undefined; decodeBufferLog
    // degrades gracefully (see its dn/de optional params).
    // NOTE: this comment's "never appear" claim is not exercised by this run — see the
    // separate pre-existing RangeError flake documented in this task's final report; it is
    // NOT the close/end race this file's other changes fix, and is left as discovered but
    // unaddressed (out of scope: fixing it touches production decode/schema code).
    let lastNodeFallbackFrame: ArrayBuffer | undefined;
    let lastEdgeFallbackFrame: ArrayBuffer | undefined;
    let fd3Buf: Buffer = Buffer.alloc(0);

    // Every stream we attach a 'data' handler to also gets an 'end' promise below, so we
    // can await full drain of ALL of them (not just process 'close') — 'end' fires strictly
    // after a stream's last 'data', and fires even on a zero-byte stream, so this is a
    // reliable per-stream EOF signal (unlike 'close' alone, which does not guarantee every
    // extra-fd pipe's final buffered 'data' has been delivered to the Node read side).
    const streamEndPromises: Promise<void>[] = [];
    const trackEnd = (s: NodeJS.ReadableStream) => {
      streamEndPromises.push(new Promise<void>((resolve) => {
        if ((s as unknown as { readableEnded?: boolean }).readableEnded) { resolve(); return; }
        s.on("end", () => resolve());
      }));
    };

    const fd3 = (proc.stdio as (NodeJS.ReadableStream | null)[])[3];
    trackEnd(fd3!);
    fd3!.on("data", (d: Buffer) => {
      const { frames, rest } = splitFrames(fd3Buf, d);
      fd3Buf = rest;
      for (const framed of frames) {
        const tag = new DataView(framed).getUint8(0);
        if (tag === BUF_BLOCK_TAG_NODE) { lastNodeFallbackFrame = framed.slice(1); continue; }
        if (tag === BUF_BLOCK_TAG_EDGE) { lastEdgeFallbackFrame = framed.slice(1); continue; }
      }
    });

    // VIEW fd → go.jsonl: the fallback bucket of not-yet-decentralized kinds (Fire/Recv/
    // Send/Select/Hover/AbcDrag*/Camera/SceneSphere/overlay toggles/LayoutLink).
    let viewBuf: Buffer = Buffer.alloc(0);
    const viewStream = (proc.stdio as (NodeJS.ReadableStream | null)[])[VIEW_FD];
    trackEnd(viewStream!);
    viewStream!.on("data", (d: Buffer) => {
      const { frames, rest } = splitFrames(viewBuf, d);
      viewBuf = rest;
      for (const ab of frames) pushLines(viewLines, decodeBufferLog(ab, lastNodeFallbackFrame, lastEdgeFallbackFrame));
    });

    // Per-edge fds → go-edge.jsonl: Geometry/Position/Arrive — this edge's own goroutine's
    // row-resolved events.
    const edgeBufs: Buffer[] = new Array(edgeCount).fill(Buffer.alloc(0));
    for (let row = 0; row < edgeCount; row++) {
      const s = (proc.stdio as (NodeJS.ReadableStream | null)[])[EDGE_BASE_FD + row];
      trackEnd(s!);
      s!.on("data", (d: Buffer) => {
        const { frames, rest } = splitFrames(edgeBufs[row], d);
        edgeBufs[row] = rest;
        for (const ab of frames) {
          const decoded = decodeEdgeStreamFrame(row, ab);
          if (decoded && decoded.eventCount > 0) pushLines(edgeLines, decodeStreamFrameEvents(decoded.eventCount, decoded.eventView));
        }
      });
    }

    // Per-node NODE fds → go-node.jsonl: NodeGeometry — this nodeMover goroutine's own
    // row-resolved event. Per-node INTERIOR fds → go-interior.jsonl: NodeBead — this
    // node's own Update-loop goroutine's events.
    const nodeBufs: Buffer[] = new Array(nodeCount).fill(Buffer.alloc(0));
    const interiorBufs: Buffer[] = new Array(nodeCount).fill(Buffer.alloc(0));
    for (let row = 0; row < nodeCount; row++) {
      const ns = (proc.stdio as (NodeJS.ReadableStream | null)[])[nodeBaseFd + row];
      trackEnd(ns!);
      ns!.on("data", (d: Buffer) => {
        const { frames, rest } = splitFrames(nodeBufs[row], d);
        nodeBufs[row] = rest;
        for (const ab of frames) {
          const decoded = decodeNodeStreamFrame(row, ab);
          if (decoded && decoded.eventCount > 0) pushLines(nodeLines, decodeStreamFrameEvents(decoded.eventCount, decoded.eventView));
        }
      });
      const is = (proc.stdio as (NodeJS.ReadableStream | null)[])[interiorBaseFd + row];
      trackEnd(is!);
      is!.on("data", (d: Buffer) => {
        const { frames, rest } = splitFrames(interiorBufs[row], d);
        interiorBufs[row] = rest;
        for (const ab of frames) {
          const decoded = decodeInteriorStreamFrame(row, ab);
          if (decoded && decoded.eventCount > 0) pushLines(interiorLines, decodeStreamFrameEvents(decoded.eventCount, decoded.eventView));
        }
      });
    }

    // The clock is free-running (no play/pause gate), so flow events fire from startup.
    // 'close' fires only after the process has ended AND all its stdio streams have been
    // closed — strictly after Go's deferred f.Close() on the -trace file completes, since
    // the OS only reports process exit once every write the process made has been
    // committed. No arbitrary sleep: this await is tied to real completion signals.
    //
    // 'close' alone is NOT sufficient for the ~33-pipe fan-out this test spawns: it does not
    // reliably guarantee every extra-fd Readable has delivered its LAST buffered 'data' to
    // the Node read side before it fires (a read-side pipe-drain race in the test harness,
    // not in Go — Go writes every event correctly on every run). A stream's 'end' fires
    // strictly after its last 'data' (and fires on a zero-byte stream too), so awaiting
    // 'end' on every stream IN ADDITION to 'close' closes that race.
    await Promise.all([
      new Promise<void>((r) => proc.on("close", () => r())),
      ...streamEndPromises,
    ]);

    // Reference path → per-kind counts from the -trace file (real trace kinds only).
    const traceText = fs.readFileSync(traceFile, "utf8");
    fs.rmSync(traceFile, { force: true });
    const stdoutEvents: string[] = [];
    for (const l of traceText.split("\n")) {
      if (!l.startsWith("{")) continue;
      let o: Record<string, unknown>;
      try { o = JSON.parse(l) as Record<string, unknown>; } catch { continue; }
      if (typeof o.kind === "string" && KIND_SET.has(o.kind)) stdoutEvents.push(l);
    }

    const sCounts = kindCounts(stdoutEvents);
    // Sum kind counts ACROSS the four separate per-owner logs — this is a read-time
    // verification step, not a write-time merge (those stay four separate files/streams).
    const perLogCounts = {
      view: kindCounts(viewLines),
      node: kindCounts(nodeLines),
      edge: kindCounts(edgeLines),
      interior: kindCounts(interiorLines),
    };
    const bCounts = new Map<string, number>();
    for (const m of Object.values(perLogCounts)) {
      for (const [k, n] of m) bCounts.set(k, (bCounts.get(k) ?? 0) + n);
    }

    // eslint-disable-next-line no-console
    console.log("REFERENCE (-trace) kinds:", JSON.stringify([...sCounts].sort()));
    // eslint-disable-next-line no-console
    console.log("PER-LOG kinds:", JSON.stringify({
      view: [...perLogCounts.view].sort(),
      node: [...perLogCounts.node].sort(),
      edge: [...perLogCounts.edge].sort(),
      interior: [...perLogCounts.interior].sort(),
    }));

    // Every stdout KIND must appear across the per-owner logs with the same TOTAL count —
    // the invariant this test protects (cross-owner arrival order and full per-field
    // fidelity are NOT asserted; see this file's header comment).
    const missing: string[] = [];
    for (const [k, n] of sCounts) {
      if ((bCounts.get(k) ?? 0) !== n) missing.push(`${k}: stdout=${n} buffer-total=${bCounts.get(k) ?? 0}`);
    }
    // eslint-disable-next-line no-console
    if (missing.length) console.log("KIND COUNT MISMATCH:", missing);

    expect(stdoutEvents.length).toBeGreaterThan(0);
    // Flow events must have actually fired (proves the playing-sim drive worked).
    expect(stdoutEvents.some((e) => e.includes('"edge-bead"'))).toBe(true);
    expect(missing).toEqual([]);
  }, 30000);
});
