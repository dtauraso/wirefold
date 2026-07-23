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
  // stdio[3] = reserved, unused pipe slot (nothing reads it — see runCommand.ts's stdio
  // comment). stdio[4] = VIEW_FD, the dedicated view-stream pipe.
  p.stdio = [null, null, null, { on: vi.fn() }, { on: vi.fn() }];
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
  return new BuildAndRunRunner();
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
});

describe("lastViewFrame cache (getLastViewFrame) — resend replacement", () => {
  // The ext host caches the last VIEW-stream frame so a webview "ready" (after a remount)
  // can be served instantly instead of asking Go to manufacture a frame (resend, removed).
  // THE TRAP: runCommand.ts hands onSnapshot the SAME ArrayBuffer it forwards to
  // postMessage, and VS Code's webview.postMessage TRANSFERS (not clones) ArrayBuffers on
  // engines >=1.57 — a real transfer DETACHES the source (byteLength -> 0). This test
  // proves the cache survives that by actually detaching the posted buffer via the real
  // structured-clone transfer primitive (Node's structuredClone with a transfer list — the
  // same detach semantics a MessagePort/Electron IPC transfer performs), then asserting
  // getLastViewFrame() still returns the untouched bytes.
  // Frames on the dedicated VIEW fd are [len:u32-LE][payload] with NO tag byte (the fd
  // position identifies the stream).
  function framed(bytes: number[]): Uint8Array {
    const out = new Uint8Array(4 + bytes.length);
    new DataView(out.buffer).setUint32(0, bytes.length, true);
    out.set(bytes, 4);
    return out;
  }

  // The VIEW_FD "data" listener run() registered on this process's stdio[4] stub.
  function viewDataCb(proc: FakeProc): (d: Buffer) => void {
    const on = proc.stdio[4]!.on as ReturnType<typeof vi.fn>;
    const call = on.mock.calls.find((c) => c[0] === "data");
    if (!call) throw new Error("view fd 'data' handler was never registered");
    return call[1] as (d: Buffer) => void;
  }

  it("cached buffer survives the posted buffer being TRANSFER-detached", () => {
    const posted: ArrayBuffer[] = [];
    const r = new BuildAndRunRunner(
      (snap) => {
        posted.push(snap.buffer);
        // Simulate exactly what a real postMessage transfer does to its source buffer.
        structuredClone(snap.buffer, { transfer: [snap.buffer] });
      },
    );
    r.run();
    const onViewData = viewDataCb(spawned[0]);

    onViewData(Buffer.from(framed([1, 2, 3]).buffer));

    // The buffer handed to onSnapshot is now detached (byteLength 0) — proving the trap is
    // real and the test isn't vacuously passing.
    expect(posted[0].byteLength).toBe(0);

    // The CACHE, independent of that reference, must still hold the original 3 bytes.
    const cached = r.getLastViewFrame();
    expect(cached).toBeDefined();
    expect(cached!.byteLength).toBe(3);
    expect(new Uint8Array(cached!)).toEqual(new Uint8Array([1, 2, 3]));

    // SERVING the cache must not detach it. The "ready" handler posts what
    // getLastViewFrame returns, and postMessage TRANSFERS — so if we handed out the
    // cached reference, the first remount would empty our own cache. That breaks the
    // exact case this cache exists for: while PAUSED no new frame arrives to
    // repopulate it, so the SECOND remount would be served zero bytes.
    structuredClone(cached!, { transfer: [cached!] }); // simulate the post transferring it
    expect(cached!.byteLength).toBe(0); // the served copy is detached, as postMessage would

    const second = r.getLastViewFrame(); // a second remount, no new frame in between
    expect(second).toBeDefined();
    expect(second!.byteLength).toBe(3);
    expect(new Uint8Array(second!)).toEqual(new Uint8Array([1, 2, 3]));
  });

  it("cache is overwritten by each new frame; getLastViewFrame reflects the LATEST one", () => {
    const r = new BuildAndRunRunner(); // no onSnapshot — cache still populates
    r.run();
    const onViewData = viewDataCb(spawned[0]);

    expect(r.getLastViewFrame()).toBeUndefined();

    onViewData(Buffer.from(framed([9]).buffer));
    expect(new Uint8Array(r.getLastViewFrame()!)).toEqual(new Uint8Array([9]));

    onViewData(Buffer.from(framed([1, 2, 3, 4]).buffer));
    expect(new Uint8Array(r.getLastViewFrame()!)).toEqual(new Uint8Array([1, 2, 3, 4]));
  });
});

describe("view-stream partial-frame parse state does not survive a respawn", () => {
  // Pins the fix: stdoutBuf/viewBuf were runner-lifetime fields reset only at declaration,
  // so a process killed mid-frame left a partial frame that concatenated with the NEXT
  // (respawned) process's first chunk — splitFrames then read a frame length from inside
  // the stale bytes and froze/starved the scene. run() now mints fresh parse state at every
  // spawn (freshStreamState), so a dead process's tail can never prefix the next stream.

  // Build a length-prefixed, no-tag-byte VIEW-fd frame: [len:u32-LE][body].
  function frameBuf(body: number[]): Buffer {
    const out = Buffer.alloc(4 + body.length);
    out.writeUInt32LE(body.length, 0);
    Buffer.from(body).copy(out, 4);
    return out;
  }

  // The VIEW_FD "data" listener run() registered on this process's stdio[4] stub.
  function viewDataCb(proc: FakeProc): (d: Buffer) => void {
    const on = proc.stdio[4]!.on as ReturnType<typeof vi.fn>;
    const call = on.mock.calls.find((c) => c[0] === "data");
    return call![1] as (d: Buffer) => void;
  }

  it("discards a dead process's leftover partial frame before decoding the next process's stream", () => {
    const snaps: number[][] = [];
    const r = new BuildAndRunRunner((msg) => snaps.push([...new Uint8Array(msg.buffer)]));
    r.run();

    // proc0 dies mid-frame: header claims an 8-byte body, only 2 body bytes arrive.
    viewDataCb(spawned[0])(frameBuf([1, 2, 3, 4, 5, 6, 7, 8]).slice(0, 6));
    expect(snaps).toEqual([]); // incomplete — nothing decoded yet

    // Natural exit → respawn (proc1).
    spawned[0].emit("close", 0);
    expect(spawned.length).toBe(2);

    // proc1 sends ONE complete, valid frame. With the leftover discarded it decodes cleanly;
    // if the stale 6 bytes had carried over, splitFrames would have mis-framed into garbage.
    viewDataCb(spawned[1])(frameBuf([0xaa, 0xbb]));
    expect(snaps).toEqual([[0xaa, 0xbb]]);
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

});
