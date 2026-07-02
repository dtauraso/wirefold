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

function fakeRunner(running: boolean) {
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
    play: rec("play"),
    pause: rec("pause"),
    resume: rec("resume"),
    stop: rec("stop"),
    cancel: rec("cancel"),
    resend: rec("resend"),
    writeStdin: rec("writeStdin"),
  };
  return runner;
}

function ctxFor(runner: ReturnType<typeof fakeRunner>): MessageCtx {
  // Cast: the fake implements exactly the runner surface handleMessage touches.
  return { logUri: undefined, runner: runner as unknown as MessageCtx["runner"], post: () => {} };
}

const names = (r: ReturnType<typeof fakeRunner>) => r.calls.map((c) => c.method);

describe("handleMessage dispatch — control signals", () => {
  it("play → runner.play()", async () => {
    const r = fakeRunner(true);
    await handleMessage({ type: "play" }, ctxFor(r));
    expect(names(r)).toEqual(["play"]);
  });

  it("pause → runner.pause()", async () => {
    const r = fakeRunner(true);
    await handleMessage({ type: "pause" }, ctxFor(r));
    expect(names(r)).toEqual(["pause"]);
  });

  it("resume → runner.resume()", async () => {
    const r = fakeRunner(true);
    await handleMessage({ type: "resume" }, ctxFor(r));
    expect(names(r)).toEqual(["resume"]);
  });

  it("stop → runner.stop()", async () => {
    const r = fakeRunner(true);
    await handleMessage({ type: "stop" }, ctxFor(r));
    expect(names(r)).toEqual(["stop"]);
  });

  it("run-cancel → runner.cancel()", async () => {
    const r = fakeRunner(true);
    await handleMessage({ type: "run-cancel" }, ctxFor(r));
    expect(names(r)).toEqual(["cancel"]);
  });

  it("run → runner.run() then runner.play()", async () => {
    const r = fakeRunner(true);
    await handleMessage({ type: "run" }, ctxFor(r));
    expect(names(r)).toEqual(["run", "play"]);
  });

  it("ready spawns; requests resend only when Go was ALREADY running", async () => {
    const wasRunning = fakeRunner(true);
    await handleMessage({ type: "ready" }, ctxFor(wasRunning));
    expect(names(wasRunning)).toEqual(["run", "resend"]);

    const fresh = fakeRunner(false);
    await handleMessage({ type: "ready" }, ctxFor(fresh));
    expect(names(fresh)).toEqual(["run"]);
  });
});

describe("handleMessage dispatch — edit ops (running)", () => {
  it("edit create → writeStdin with the verbatim message", async () => {
    const r = fakeRunner(true);
    const msg = { type: "edit", op: "create", target: "n1", targetHandle: "out" };
    await handleMessage(msg, ctxFor(r));
    const w = r.calls.filter((c) => c.method === "writeStdin");
    expect(w).toHaveLength(1);
    expect(w[0].args[0]).toBe(JSON.stringify(msg));
  });

  it("edit delete → writeStdin with the verbatim message", async () => {
    const r = fakeRunner(true);
    const msg = { type: "edit", op: "delete", target: "e1", targetHandle: "in" };
    await handleMessage(msg, ctxFor(r));
    const w = r.calls.filter((c) => c.method === "writeStdin");
    expect(w).toHaveLength(1);
    expect(w[0].args[0]).toBe(JSON.stringify(msg));
  });

  it("edit update (edge/faded) → writeStdin with the verbatim message", async () => {
    const r = fakeRunner(true);
    const msg = { type: "edit", op: "update", kind: "edge", attr: "faded", edges: { e1: true } };
    await handleMessage(msg, ctxFor(r));
    const w = r.calls.filter((c) => c.method === "writeStdin");
    expect(w).toHaveLength(1);
    expect(w[0].args[0]).toBe(JSON.stringify(msg));
  });
});

describe("handleMessage dispatch — edit while stopped is DROPPED, not buffered", () => {
  it("edit create while !isRunning → writeStdin NOT called", async () => {
    const r = fakeRunner(false);
    await handleMessage(
      { type: "edit", op: "create", target: "n1", targetHandle: "out" },
      ctxFor(r),
    );
    expect(r.calls.filter((c) => c.method === "writeStdin")).toHaveLength(0);
  });

  it("edit update while !isRunning → writeStdin NOT called", async () => {
    const r = fakeRunner(false);
    await handleMessage(
      { type: "edit", op: "update", kind: "edge", attr: "faded", edges: { e1: true } },
      ctxFor(r),
    );
    expect(r.calls.filter((c) => c.method === "writeStdin")).toHaveLength(0);
  });
});
