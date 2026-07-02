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
  it("flushes stdin buffered before spawn (control — proves the drop is deliberate)", () => {
    const r = newRunner();
    r.writeStdin("L1");
    r.writeStdin("L2");
    r.run();
    const proc = spawned[0];
    expect(proc.stdin.write).toHaveBeenCalledWith("L1\n");
    expect(proc.stdin.write).toHaveBeenCalledWith("L2\n");
  });

  it("cancel() clears pendingStdin — not replayed onto the next spawned Go", () => {
    const r = newRunner();
    r.writeStdin("L1");
    r.writeStdin("L2");
    r.cancel(); // proc is undefined here; cancel still drops the buffer
    r.run();
    const proc = spawned[0];
    expect(proc.stdin.write).not.toHaveBeenCalled();
  });

  it("stop() clears pendingStdin — not replayed onto the next spawned Go", () => {
    const r = newRunner();
    r.writeStdin("L1");
    r.stop();
    r.run();
    const proc = spawned[0];
    expect(proc.stdin.write).not.toHaveBeenCalled();
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
