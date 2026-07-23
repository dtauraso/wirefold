// handleMessage dispatch tests. handleMessage(raw, ctx) takes an injectable ctx
// {logUri, runner, post}; these tests drive it with a FAKE runner (recording which
// methods were called with what) and assert each webview→host message routes to the
// right runner method. logUri is left undefined so appendWebviewLog is a no-op (it
// early-returns on an undefined document uri) — no .probe/ writes happen.
//
// The load-bearing case is edit-while-stopped: an "edit" received while
// runner.isRunning() is false must be DROPPED (writeStdin NOT called), never buffered,
// because writeStdin's buffer flushes onto the NEXT spawned Go which re-reads the
// graph from disk and would double-apply. That drop is a prior-audit fix; this locks it.

import { describe, it, expect } from "vitest";
import { handleMessage, type MessageCtx } from "../src/extension/handle-message";

type Call = { method: string; args: unknown[] };

function fakeRunner(running: boolean, viewFrame: ArrayBuffer | undefined = undefined) {
  const calls: Call[] = [];
  const rec =
    (method: string) =>
    (...args: unknown[]) => {
      calls.push({ method, args });
    };
  const runner = {
    calls,
    isRunning: () => running,
    run: rec("run"),
    getLastViewFrame: () => {
      calls.push({ method: "getLastViewFrame", args: [] });
      return viewFrame;
    },
    // Per-edge/per-node/per-interior dedicated-stream replay caches (see
    // BuildAndRunRunner.getLastEdgeFrames/getLastNodeFrames/getLastInteriorFrames) — empty
    // in every test here (no dedicated-stream fixture data needed for these dispatch
    // assertions), but must exist or the "ready" handler's replay loop throws.
    getLastEdgeFrames: () => {
      calls.push({ method: "getLastEdgeFrames", args: [] });
      return [];
    },
    getLastNodeFrames: () => {
      calls.push({ method: "getLastNodeFrames", args: [] });
      return [];
    },
    getLastInteriorFrames: () => {
      calls.push({ method: "getLastInteriorFrames", args: [] });
      return [];
    },
    writeStdin: rec("writeStdin"),
  };
  return runner;
}

function ctxFor(runner: ReturnType<typeof fakeRunner>, post: MessageCtx["post"] = () => {}): MessageCtx {
  // Cast: the fake implements exactly the runner surface handleMessage touches.
  return { logUri: undefined, runner: runner as unknown as MessageCtx["runner"], post };
}

const names = (r: ReturnType<typeof fakeRunner>) => r.calls.map((c) => c.method);

describe("handleMessage dispatch — ready / auto-launch", () => {
  it("ready spawns; posts the cached last VIEW frame only when Go was ALREADY running", async () => {
    const cachedView = new Uint8Array([1, 2, 3]).buffer;
    const posted: unknown[] = [];
    const wasRunning = fakeRunner(true, cachedView);
    await handleMessage({ type: "ready" }, ctxFor(wasRunning, (m) => posted.push(m)));
    expect(names(wasRunning)).toEqual(["run", "getLastViewFrame", "getLastEdgeFrames", "getLastNodeFrames", "getLastInteriorFrames"]);
    expect(posted).toEqual([
      { type: "buffer-snapshot", buffer: cachedView, tag: 4 },
    ]);

    // A just-spawned Go needs no cached frame — it emits its own startup geometry.
    const fresh = fakeRunner(false, cachedView);
    const freshPosted: unknown[] = [];
    await handleMessage({ type: "ready" }, ctxFor(fresh, (m) => freshPosted.push(m)));
    expect(names(fresh)).toEqual(["run"]);
    expect(freshPosted).toEqual([]);
  });

  it("ready + wasRunning but no cached frame yet → no post", async () => {
    const wasRunning = fakeRunner(true, undefined);
    const posted: unknown[] = [];
    await handleMessage({ type: "ready" }, ctxFor(wasRunning, (m) => posted.push(m)));
    expect(names(wasRunning)).toEqual(["run", "getLastViewFrame", "getLastEdgeFrames", "getLastNodeFrames", "getLastInteriorFrames"]);
    expect(posted).toEqual([]);
  });
});

describe("handleMessage dispatch — go-record (binary editor→Go bridge, running)", () => {
  // The webview now encodes raw-input / edit messages into a BINARY record and posts a
  // { type: "go-record", record } envelope. The host writes the record's ArrayBuffer to
  // Go's stdin VERBATIM (framed inside writeStdin) — it does not inspect or re-encode it.
  it("go-record → writeStdin with the record's ArrayBuffer", async () => {
    const r = fakeRunner(true);
    const record = new Uint8Array([20, 0, 0, 0, 0]).buffer; // a stand-in edit record
    await handleMessage({ type: "go-record", record }, ctxFor(r));
    const w = r.calls.filter((c) => c.method === "writeStdin");
    expect(w).toHaveLength(1);
    expect(w[0].args[0]).toBe(record);
  });
});

describe("handleMessage dispatch — go-record while stopped is DROPPED, not buffered", () => {
  it("go-record while !isRunning → writeStdin NOT called", async () => {
    const r = fakeRunner(false);
    await handleMessage(
      { type: "go-record", record: new Uint8Array([1]).buffer },
      ctxFor(r),
    );
    expect(r.calls.filter((c) => c.method === "writeStdin")).toHaveLength(0);
  });
});
