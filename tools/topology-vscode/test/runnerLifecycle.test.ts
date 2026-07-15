// BuildAndRunRunner lifecycle tests: pendingStdin drain and respawn/looping.
//
// These drive the real runner but replace its two external edges: cp.spawn (returns a
// fake child process we can emit "close" on) and goBuild (so no real `go build`/kill
// runs). vscode is the aliased stub; we point workspaceFolders at a temp dir holding a
// stub binary so ensureBinaryBuilt takes its up-to-date fast path. process.kill is
// stubbed to a no-op so cancel() doesn't signal the test runner's own process group.
//
// Locked behaviors (both prior-audit fixes):
//   - pendingStdin is CLEARED on stop() and cancel(), never replayed onto the next
//     spawned Go (which re-reads the graph from disk → would double-apply edits).
//   - looping respawns only on a NATURAL exit; cancel()/stop() must not respawn.

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { frameRecord } from "../src/schema/input-layout";
import { EventEmitter } from "node:events";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import * as vscodeStub from "vscode";

type FakeProc = EventEmitter & {
  pid: number;
  stdin: { write: ReturnType<typeof vi.fn> };
  stdout: { on: ReturnType<typeof vi.fn> };
  stderr: { on: ReturnType<typeof vi.fn> };
  stdio: (null | { on: ReturnType<typeof vi.fn> })[];
  kill: ReturnType<typeof vi.fn>;
};

const spawned: FakeProc[] = [];

function makeFakeProc(): FakeProc {
  const p = new EventEmitter() as FakeProc;
  p.pid = 4242 + spawned.length;
  p.stdin = { write: vi.fn() };
  p.stdout = { on: vi.fn() };
  p.stderr = { on: vi.fn() };
  // stdio[3] = fd3 binary side channel stub (null = not available).
  p.stdio = [null, null, null, { on: vi.fn() }];
  p.kill = vi.fn();
  return p;
}

const spawnMock = vi.fn(() => {
  const p = makeFakeProc();
  spawned.push(p);
  return p;
});

vi.mock("child_process", () => ({
  spawn: (...args: unknown[]) => spawnMock(...(args as [])),
}));

vi.mock("../src/goBuild", () => ({
  buildBinary: () => ({ ok: true, busy: false }),
  maxGoMtime: () => 0,
  killOrphanedSims: () => ({ killed: 0 }),
}));

// Imported after the mocks are registered.
import { BuildAndRunRunner } from "../src/runCommand";

let tmpDir: string;
let killSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
  spawned.length = 0;
  spawnMock.mockClear();
  tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "wirefold-runner-"));
  // ensureBinaryBuilt statSyncs this path; maxGoMtime is mocked to 0 so it's "fresh".
  fs.mkdirSync(path.join(tmpDir, ".wirefold-cache"), { recursive: true });
  fs.writeFileSync(path.join(tmpDir, ".wirefold-cache", "wirefold"), "stub");
  vscodeStub.workspace.workspaceFolders = [{ uri: { fsPath: tmpDir } }];
  killSpy = vi.spyOn(process, "kill").mockImplementation(() => true);
});

afterEach(() => {
  killSpy.mockRestore();
  vscodeStub.workspace.workspaceFolders = undefined;
  fs.rmSync(tmpDir, { recursive: true, force: true });
});

function newRunner() {
  return new BuildAndRunRunner(() => {});
}

describe("pendingStdin drain", () => {
  // writeStdin now takes a BINARY record; it FRAMES it as [len:u32-LE][record] and buffers
  // the framed bytes before spawn, flushing them on run().
  const rec1 = new Uint8Array([1]).buffer; // a stand-in control record
  const rec2 = new Uint8Array([2]).buffer;

  it("flushes stdin buffered before spawn (control — proves the drop is deliberate)", () => {
    const r = newRunner();
    r.writeStdin(rec1);
    r.writeStdin(rec2);
    r.run();
    const proc = spawned[0];
    expect(proc.stdin.write).toHaveBeenCalledWith(frameRecord(rec1));
    expect(proc.stdin.write).toHaveBeenCalledWith(frameRecord(rec2));
  });

  it("cancel() clears pendingStdin — not replayed onto the next spawned Go", () => {
    const r = newRunner();
    r.writeStdin(rec1);
    r.writeStdin(rec2);
    r.cancel(); // proc is undefined here; cancel still drops the buffer
    r.run();
    const proc = spawned[0];
    expect(proc.stdin.write).not.toHaveBeenCalled();
  });

  it("stop() clears pendingStdin — not replayed onto the next spawned Go", () => {
    const r = newRunner();
    r.writeStdin(rec1);
    r.stop();
    r.run();
    const proc = spawned[0];
    expect(proc.stdin.write).not.toHaveBeenCalled();
  });
});

describe("lastSnapshot cache (getLastSnapshot) — resend replacement", () => {
  // The ext host caches the last fd3 buffer-snapshot frame so a webview "ready" (after a
  // remount) can be served instantly instead of asking Go to manufacture a frame (resend,
  // removed). THE TRAP: runCommand.ts hands onSnapshot the SAME ArrayBuffer it forwards to
  // postMessage, and VS Code's webview.postMessage TRANSFERS (not clones) ArrayBuffers on
  // engines >=1.57 — a real transfer DETACHES the source (byteLength -> 0). This test proves
  // the cache survives that by actually detaching the posted buffer via the real structured-
  // clone transfer primitive (Node's structuredClone with a transfer list — the same
  // detach semantics a MessagePort/Electron IPC transfer performs), then asserting
  // getLastSnapshot() still returns the untouched bytes.
  function framed(bytes: number[]): Uint8Array {
    const body = new Uint8Array(bytes);
    const out = new Uint8Array(4 + body.length);
    new DataView(out.buffer).setUint32(0, body.length, true);
    out.set(body, 4);
    return out;
  }

  it("cached buffer survives the posted buffer being TRANSFER-detached", () => {
    const posted: ArrayBuffer[] = [];
    const r = new BuildAndRunRunner(
      () => {},
      (snap) => {
        posted.push(snap.buffer);
        // Simulate exactly what a real postMessage transfer does to its source buffer.
        structuredClone(snap.buffer, { transfer: [snap.buffer] });
      },
    );
    r.run();
    const proc = spawned[0];
    const fd3 = proc.stdio[3] as { on: ReturnType<typeof vi.fn> };
    const dataCall = fd3.on.mock.calls.find((c) => c[0] === "data");
    if (!dataCall) throw new Error("fd3 'data' handler was never registered");
    const onFd3Data = dataCall[1] as (d: Buffer) => void;

    onFd3Data(Buffer.from(framed([1, 2, 3]).buffer));

    // The buffer handed to onSnapshot is now detached (byteLength 0) — proving the trap is
    // real and the test isn't vacuously passing.
    expect(posted[0].byteLength).toBe(0);

    // The CACHE, independent of that reference, must still hold the original 3 bytes.
    const cached = r.getLastSnapshot();
    expect(cached).toBeDefined();
    expect(cached!.byteLength).toBe(3);
    expect(new Uint8Array(cached!)).toEqual(new Uint8Array([1, 2, 3]));

    // SERVING the cache must not detach it. The "ready" handler posts what
    // getLastSnapshot returns, and postMessage TRANSFERS — so if we handed out the
    // cached reference, the first remount would empty our own cache. That breaks the
    // exact case this cache exists for: while PAUSED no new frame arrives to
    // repopulate it, so the SECOND remount would be served zero bytes.
    structuredClone(cached!, { transfer: [cached!] }); // simulate the post transferring it
    expect(cached!.byteLength).toBe(0); // the served copy is detached, as postMessage would

    const second = r.getLastSnapshot(); // a second remount, no new frame in between
    expect(second).toBeDefined();
    expect(second!.byteLength).toBe(3);
    expect(new Uint8Array(second!)).toEqual(new Uint8Array([1, 2, 3]));
  });

  it("cache is overwritten by each new frame; getLastSnapshot reflects the LATEST one", () => {
    const r = new BuildAndRunRunner(() => {}); // no onSnapshot — cache still populates
    r.run();
    const proc = spawned[0];
    const fd3 = proc.stdio[3] as { on: ReturnType<typeof vi.fn> };
    const onFd3Data = fd3.on.mock.calls.find((c) => c[0] === "data")![1] as (d: Buffer) => void;

    expect(r.getLastSnapshot()).toBeUndefined();

    onFd3Data(Buffer.from(framed([9]).buffer));
    expect(new Uint8Array(r.getLastSnapshot()!)).toEqual(new Uint8Array([9]));

    onFd3Data(Buffer.from(framed([1, 2, 3, 4]).buffer));
    expect(new Uint8Array(r.getLastSnapshot()!)).toEqual(new Uint8Array([1, 2, 3, 4]));
  });
});

describe("respawn / looping", () => {
  it("a natural exit while looping respawns", () => {
    const r = newRunner();
    r.run();
    expect(spawnMock).toHaveBeenCalledTimes(1);
    // Natural exit (not cancelled) while looping → respawn.
    spawned[0].emit("close", 0);
    expect(spawnMock).toHaveBeenCalledTimes(2);
  });

  it("cancel() during a looping run does NOT respawn", () => {
    const r = newRunner();
    r.run();
    expect(spawnMock).toHaveBeenCalledTimes(1);
    r.cancel();
    // Proc dies from the cancel; close fires with cancelled=true → cancelled branch, no respawn.
    spawned[0].emit("close", null);
    expect(spawnMock).toHaveBeenCalledTimes(1);
  });

  it("stop() clears looping then cancels — terminates the loop", () => {
    const r = newRunner();
    r.run();
    expect(spawnMock).toHaveBeenCalledTimes(1);
    r.stop();
    spawned[0].emit("close", null);
    expect(spawnMock).toHaveBeenCalledTimes(1);
    // And a subsequent natural-looking close does not resurrect the loop either.
    expect(r.isRunning()).toBe(false);
  });
});
