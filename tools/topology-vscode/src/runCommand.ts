import * as vscode from "vscode";
import * as cp from "child_process";
import * as fs from "fs";
import * as path from "path";
import type { RunStatus, HostToWebviewMsg } from "./messages";
import { buildBinary, maxGoMtime, killOrphanedSims } from "./goBuild";
import { encodePlay, encodePause, frameRecord } from "./schema/input-layout";
import { PROBE_DIR, PROBE_FILES } from "./probe-files";
import { decodeBufferLog } from "./buffer-log";

export type { RunStatus };

/** Format a Go-side error as a probe JSONL line (src="go", kind="error"). */
function goErrorLine(message: string): string {
  return JSON.stringify({ ts_ms: Date.now(), src: "go", kind: "error", message }) + "\n";
}

// splitJsonlLines is the pure newline-framing step for stdout: given the carried-over
// partial buffer and a freshly-arrived chunk, it returns every COMPLETE (newline-
// terminated) line and the trailing partial `rest` to carry into the next call. A line
// split across two chunks is reassembled (its bytes accumulate in `rest` until the
// newline arrives); multiple lines in one chunk all come out; a trailing partial is
// buffered. handleStdout owns per-line dispatch; this owns only the framing.
export function splitJsonlLines(buf: string, chunk: string): { lines: string[]; rest: string } {
  let rest = buf + chunk;
  const lines: string[] = [];
  let nl: number;
  while ((nl = rest.indexOf("\n")) !== -1) {
    lines.push(rest.slice(0, nl));
    rest = rest.slice(nl + 1);
  }
  return { lines, rest };
}

// splitFrames is the pure length-prefix framing step for fd3: given the carried-over
// partial Buffer and a freshly-arrived binary chunk, it returns every COMPLETE frame
// payload (as an ArrayBuffer, ready to transfer zero-copy) and the trailing partial
// `rest` to carry into the next call. Frames are [len:u32-LE][payload bytes]; a frame
// split across two chunks is reassembled; multiple frames in one chunk all come out;
// a trailing partial (len header not yet complete, or payload bytes not yet complete)
// is buffered. handleFd3 owns dispatch; this owns only the framing.
export function splitFrames(buf: Buffer, chunk: Buffer): { frames: ArrayBuffer[]; rest: Buffer } {
  let rest = buf.length > 0 ? Buffer.concat([buf, chunk]) : chunk;
  const frames: ArrayBuffer[] = [];
  while (rest.length >= 4) {
    const frameLen = rest.readUInt32LE(0);
    const needed = 4 + frameLen;
    if (rest.length < needed) break;
    // Slice out the payload and copy into a standalone ArrayBuffer (detached from
    // the Node.js Buffer pool so it can be transferred zero-copy to the webview).
    const payload = rest.slice(4, needed);
    const ab = payload.buffer.slice(payload.byteOffset, payload.byteOffset + payload.byteLength);
    frames.push(ab);
    rest = rest.slice(needed);
  }
  return { frames, rest };
}

// Go stdout relay: errors (stderr, non-zero exit, spawn failure) are written to
// .probe/go-errors.jsonl. Trace events are no longer emitted on stdout at all (see
// handleStdout below) — the .probe trace log is now the DECODE of the fd3 binary
// content buffer's EVENT block (decodeBufferLog, in handleFd3).

// tryParseBreadcrumb recognizes the Go Trace.Breadcrumb line shape
// ({"kind":"breadcrumb","label":...}). Breadcrumbs are logging-only, intercepted in
// handleStdout before the line is appended to the output channel as plain process output.
export function tryParseBreadcrumb(line: string): Record<string, unknown> | undefined {
  if (!line.startsWith("{")) return undefined;
  try {
    const obj: unknown = JSON.parse(line);
    if (typeof obj === "object" && obj !== null && (obj as Record<string, unknown>).kind === "breadcrumb") {
      return obj as Record<string, unknown>;
    }
  } catch { /* not JSON */ }
  return undefined;
}

// ensureBinaryBuilt builds the Go binary at binPath if it's missing or stale.
// A rebuild is needed when binPath does not exist OR any *.go source under
// repoRoot is newer than binPath. Up-to-date → no build, returns ok. This
// replaces `go run .` (which relinks a throwaway binary every launch) with a
// single prebuilt binary reused across animation start/restart.
//
// Lazy safety net: even with the eager .go watcher (see extension.ts) keeping
// the binary fresh, this still rebuilds at launch when the watcher missed an
// event or wasn't armed. It delegates to the guarded buildBinary, so if the
// watcher is mid-build this call coalesces (busy → ok) wait-free and never
// blocks run().
// ensureBinaryBuilt retries buildBinary/existsSync this many times while waiting out an
// in-flight coalesced Go binary build (see below) before giving up and reporting an error.
// Each attempt is cheap (a build call that returns immediately when coalesced, or a stat
// check), so 50 is a generous ceiling meant to absorb a slow first-open Go build without
// hanging the extension host indefinitely on a build that never completes.
const BUILD_BINARY_MAX_ATTEMPTS = 50;

function ensureBinaryBuilt(
  repoRoot: string,
  binPath: string,
): { ok: true } | { ok: false; error: string } {
  let binMtime = -1;
  try {
    binMtime = fs.statSync(binPath).mtimeMs;
  } catch { /* missing → rebuild */ }
  const needsRebuild = binMtime < 0 || maxGoMtime(repoRoot) > binMtime;
  if (!needsRebuild) return { ok: true };
  // buildBinary may COALESCE (returns ok with busy:true) when a watcher build is
  // in flight against the same output path. On first open the binary can be
  // absent AND a watcher build in flight — a coalesced ok would let run() spawn a
  // non-existent path (ENOENT, runner stuck). So only report ok once the binary
  // actually exists on disk: retry buildBinary until it runs to completion (the
  // guard is released) or the in-flight build has produced the binary.
  for (let attempt = 0; attempt < BUILD_BINARY_MAX_ATTEMPTS; attempt++) {
    const res = buildBinary(repoRoot, binPath);
    if (!res.ok) return res;
    if (!res.busy) {
      // Our own build ran synchronously to completion (ok). Trust it — but sanity
      // check the file so a silent absence still surfaces as an error, not ENOENT.
      if (fs.existsSync(binPath)) return { ok: true };
      return { ok: false, error: `go build reported success but ${binPath} is missing` };
    }
    // Coalesced against an in-flight build. If that build has already produced the
    // binary, we're done; otherwise retry (the guard will clear and our own build runs).
    if (fs.existsSync(binPath)) return { ok: true };
  }
  return {
    ok: false,
    error: `binary ${binPath} not built after ${BUILD_BINARY_MAX_ATTEMPTS} attempts`,
  };
}

export class BuildAndRunRunner {
  private proc: cp.ChildProcess | undefined;
  // Explicit cancel flag — distinguishing cancellation by signal name races
  // against natural exits, since a process that happened to die from SIGTERM
  // on its own would be misreported as "cancelled".
  private cancelled = false;
  // looping: when true, respawn automatically on natural exit. Set by run();
  // cleared by stop().
  private looping = false;
  private channel: vscode.OutputChannel | undefined;
  // Partial line buffer for stdout — trace lines are newline-delimited.
  private stdoutBuf = "";
  // Partial binary frame buffer for fd3 — length-prefixed binary frames.
  private fd3Buf: Buffer = Buffer.alloc(0);
  private probeFile: string | undefined;
  private goErrorsFile: string | undefined;
  private goDebugFile: string | undefined;
  private tsFile: string | undefined;
  private tsErrorsFile: string | undefined;
  // Last fd3 buffer-snapshot frame, kept so a REMOUNTED webview (which holds no state)
  // can be handed a full frame instantly on "ready" without round-tripping to Go — see
  // getLastSnapshot(). This is a keyframe cache of Go's own bytes, not authored state.
  // MUST be a COPY, not the ArrayBuffer instance handed to onSnapshot/postMessage: VS
  // Code's webview.postMessage TRANSFERS (does not clone) ArrayBuffers to the webview
  // process on engines >=1.57 (see the @types/vscode postMessage doc comment — "will be
  // more efficiently transferred to the webview"), which DETACHES the source buffer
  // (byteLength -> 0) once posted. Caching the same reference would silently hand a
  // later "ready" an empty buffer. See runCommand.test.ts for the byteLength assertion.
  private lastSnapshot: ArrayBuffer | undefined;

  private topologyPath: string | undefined;

  constructor(
    private readonly post: (s: RunStatus) => void,
    private readonly onSnapshot?: (msg: HostToWebviewMsg & { type: "buffer-snapshot" }) => void,
  ) {}

  run(topologyPath?: string) {
    if (this.proc) {
      // Already spawned: re-announce liveness rather than returning silently. "active" is
      // STATE, not a one-shot event — a webview that remounts (reopened file, hot reload)
      // missed the post from the original spawn below and would otherwise never learn a
      // process exists, leaving its run/stop buttons inert while Go streams frames at it.
      // The "ready" case replays the cached snapshot for the same reason; this is its
      // status half. post() is idempotent for an unchanged state.
      this.post({ state: "active" });
      return;
    }
    if (topologyPath) this.topologyPath = topologyPath;
    const folder = vscode.workspace.workspaceFolders?.[0];
    if (!folder) return;
    const probeDir = path.join(folder.uri.fsPath, PROBE_DIR);
    fs.mkdirSync(probeDir, { recursive: true });
    this.probeFile = path.join(probeDir, PROBE_FILES.go);
    this.goErrorsFile = path.join(probeDir, PROBE_FILES.goErrors);
    this.goDebugFile = path.join(probeDir, PROBE_FILES.goDebug);
    this.tsFile = path.join(probeDir, PROBE_FILES.ts);
    this.tsErrorsFile = path.join(probeDir, PROBE_FILES.tsErrors);
    if (!this.channel) this.channel = vscode.window.createOutputChannel("topology run");
    this.channel.clear();
    this.channel.show(true);
    const repoRoot = folder.uri.fsPath;
    const binPath = path.join(repoRoot, ".wirefold-cache", "wirefold");
    const topArgs = this.topologyPath ? ["-topology", this.topologyPath] : [];
    // Build the binary once (and only rebuild when a .go source changed) instead of
    // relinking a throwaway binary via `go run .` on every start/restart.
    const built = ensureBinaryBuilt(repoRoot, binPath);
    if (!built.ok) {
      this.channel.appendLine(`\n[build error: ${built.error}]`);
      if (this.goErrorsFile) {
        try {
          fs.appendFileSync(this.goErrorsFile, goErrorLine(built.error), "utf8");
        } catch { /* swallow */ }
      }
      this.looping = false;
      this.post({ state: "error", message: built.error });
      return;
    }
    // Reap orphaned sims left by prior/crashed editor sessions before spawning a
    // new one. exceptPid spares the proc this runner legitimately manages (the
    // stop/respawn logic still owns that). Single-panel assumption documented in
    // killOrphanedSims: this kills ALL matching sims except the active one.
    // this.proc is guaranteed undefined here (run() returns early at the top if a
    // proc exists), so there is no active sim to spare — exceptPid is undefined.
    // Passing it explicitly keeps the contract honest if that guard ever changes.
    // this.proc is undefined here (guarded by the early return above), so activePid
    // is always undefined — the cast overrides TypeScript's control-flow narrowing.
    const activePid: number | undefined = (this.proc as cp.ChildProcess | undefined)?.pid;
    const { killed } = killOrphanedSims(binPath, activePid);
    if (killed > 0) {
      this.channel.appendLine(`[cleanup] killed ${killed} orphaned sim process(es)`);
    }
    this.channel.appendLine("$ " + binPath + " " + topArgs.join(" "));
    this.cancelled = false;
    this.looping = true;
    // Post "active" here — this is genuine, instant, ext-host-owned truth (cp.spawn below
    // returns synchronously), NOT a prediction of clock state. Go itself starts HALTED
    // (main.go); the running-vs-paused distinction is Go-owned and streamed separately in
    // the binary buffer's Clock block (read via useClockHalted, clock-state.ts) — this
    // status message no longer predicts it (see play()/pause() below).
    // detached: true makes the child the leader of a new process group; the
    // prebuilt binary is the sole group member, so kill(-pid) reaches it
    // directly. Without this, SIGTERM could leave it orphaned on macOS.
    // stdio index 3 = fd3 binary side channel: Go writes length-prefixed binary
    // snapshot frames here (WIREFOLD_BUF_OUT_FD=3). "pipe" opens a readable pipe
    // at proc.stdio[3]; the existing stdin(0)/stdout(1)/stderr(2) are unchanged.
    this.proc = cp.spawn(binPath, [...topArgs], {
      cwd: repoRoot,
      detached: true,
      stdio: ["pipe", "pipe", "pipe", "pipe"],
      env: {
        ...process.env,
        WIREFOLD_BUF_OUT_FD: "3",
      },
    });
    this.post({ state: "active" });
    // Flush any framed binary records buffered before this spawn (writeStdin queued them).
    if (this.pendingStdin.length > 0) {
      for (const rec of this.pendingStdin) this.proc.stdin?.write(rec);
      this.pendingStdin = [];
    }
    this.proc.stdout?.on("data", (d: Buffer) => this.handleStdout(d.toString()));
    // fd3: binary snapshot frames. Cast needed because Node's ChildProcess types
    // only narrow stdio[0..2]; index 3 is typed as Readable|null via the array form.
    const fd3 = (this.proc.stdio as (NodeJS.ReadableStream | null)[])[3];
    if (fd3) {
      fd3.on("data", (d: Buffer) => this.handleFd3(d));
    }
    this.proc.stderr?.on("data", (d: Buffer) => {
      const msg = d.toString();
      this.channel!.append(msg);
      if (this.goErrorsFile) {
        try {
          fs.appendFileSync(this.goErrorsFile, goErrorLine(msg), "utf8");
        } catch { /* swallow */ }
      }
    });
    this.proc.on("close", (code) => {
      const cancelled = this.cancelled;
      const looping = this.looping;
      this.proc = undefined;
      this.cancelled = false;
      if (cancelled) {
        this.channel!.appendLine("\n[cancelled]");
        this.post({ state: "cancelled" });
      } else if (looping) {
        // Natural exit while looping — respawn immediately.
        this.channel!.appendLine(code === 0 ? "\n[ok — restarting]" : `\n[exit ${code} — restarting]`);
        this.run();
      } else if (code === 0) {
        this.channel!.appendLine("\n[ok]");
        this.post({ state: "ok" });
      } else {
        const message = `exit code ${code}`;
        this.channel!.appendLine(`\n[${message}]`);
        if (this.goErrorsFile) {
          try {
            fs.appendFileSync(this.goErrorsFile, goErrorLine(message), "utf8");
          } catch { /* swallow */ }
        }
        this.post({ state: "error", message });
      }
    });
    this.proc.on("error", (err) => {
      this.proc = undefined;
      this.cancelled = false;
      this.channel!.appendLine(`\n[spawn error: ${err.message}]`);
      if (this.goErrorsFile) {
        try {
          fs.appendFileSync(this.goErrorsFile, goErrorLine(err.message), "utf8");
        } catch { /* swallow */ }
      }
      this.post({ state: "error", message: err.message });
    });
  }

  private handleStdout(chunk: string) {
    const { lines, rest } = splitJsonlLines(this.stdoutBuf, chunk);
    this.stdoutBuf = rest;
    for (const line of lines) {
      // Breadcrumb lines are the Go-side DEBUG BREADCRUMB channel (Trace.Breadcrumb →
      // stdout {"kind":"breadcrumb",...}). They are logging-only (no step ordinal, outside
      // the closed trace vocabulary), so they are NEVER dispatched to the pump (its
      // assertNever would throw). They land in a DEDICATED .probe/go-debug.jsonl with a
      // distinct src="go-debug" so they are not conflated with buffer-decoded trace events
      // (.probe/go.jsonl) or genuine stderr errors (.probe/go-errors.jsonl).
      const crumb = tryParseBreadcrumb(line);
      if (crumb) {
        if (this.goDebugFile) {
          try {
            fs.appendFileSync(this.goDebugFile, JSON.stringify({ ts_ms: Date.now(), src: "go-debug", ...crumb }) + "\n", "utf8");
          } catch { /* swallow */ }
        }
        continue;
      }
      // Trace events are NO LONGER emitted on stdout: Go's JSON-trace emitter was removed and
      // the .probe log is now the DECODE of the fd3 binary content buffer's EVENT block (see
      // handleFd3 → decodeBufferLog). The ext host therefore no longer parses trace lines from
      // stdout; any remaining stdout line is just process output.
      this.channel!.appendLine(line);
    }
  }

  private handleFd3(chunk: Buffer) {
    const { frames, rest } = splitFrames(this.fd3Buf, chunk);
    this.fd3Buf = rest;
    for (const ab of frames) {
      // Decode the snapshot's EVENT block into full trace-event .probe lines (the buffer-
      // decoded log — the DECODE of the same binary that replaces Go's JSON-on-stdout path).
      if (this.probeFile) {
        const lines = decodeBufferLog(ab);
        if (lines.length > 0) {
          try {
            fs.appendFileSync(this.probeFile, lines, "utf8");
          } catch { /* swallow */ }
        }
      }
      // Cache a COPY before handing `ab` off — see the lastSnapshot field comment for why
      // the reference itself cannot be cached (postMessage may transfer/detach it).
      this.lastSnapshot = ab.slice(0);
      // Transfer zero-copy to the webview (if a snapshot consumer is registered).
      if (this.onSnapshot) {
        this.onSnapshot({ type: "buffer-snapshot", buffer: ab });
      }
    }
  }

  cancel() {
    // Drop any stdin lines buffered while proc was null — they belong to the
    // stopped session and must NOT replay onto the next spawned Go process (which
    // re-reads the graph from disk); stale replay would double-apply edits.
    this.pendingStdin = [];
    if (!this.proc || this.proc.pid === undefined) return;
    this.cancelled = true;
    try {
      // Negative pid → kill the whole process group (the leader created by
      // detached: true plus any descendants like the compiled binary).
      process.kill(-this.proc.pid, "SIGTERM");
    } catch {
      // Process already exited or no permission — the close handler will
      // clean up either way.
      this.proc.kill("SIGTERM");
    }
  }

  /** Send play to Go's stdin — resumes the clock gate. Fire-and-forget: no status post here.
   *  Called from the ext-host "run" case for BOTH first start and resume-after-pause — Go's
   *  gate has one Resume(), so there is nothing for a separate resume-vs-play distinction to
   *  do on this seam (there is no "resume" webview→host message kind for the same reason).
   *  The running-vs-paused OUTCOME is Go's truth, not this write's — it streams back in the
   *  binary buffer's Clock block (KindHalted) and the webview reflects it via useClockHalted
   *  (clock-state.ts), not a local prediction. */
  play(): void {
    if (!this.proc) return;
    this.writeStdin(encodePlay());
  }

  /** Send pause to Go's stdin — halts the clock gate. Fire-and-forget: no status post here
   *  (see play() above — the outcome streams back from Go's Clock block). */
  pause(): void {
    if (!this.proc) return;
    this.writeStdin(encodePause());
  }

  isRunning(): boolean {
    return this.proc !== undefined;
  }

  /** The most recent fd3 buffer-snapshot frame, or undefined if none has arrived yet.
   *  Used by the "ready" handler to hand a remounted webview a full frame instantly
   *  (see the lastSnapshot field comment).
   *
   *  Returns a FRESH COPY on every call, because the caller posts what it gets and
   *  webview.postMessage TRANSFERS ArrayBuffers — handing out the cached reference
   *  would detach our own cache on the first serve. That breaks the exact case this
   *  cache exists for: while PAUSED no new frame ever arrives to repopulate it, so a
   *  second remount would be served a zero-length buffer. The copy is one per remount. */
  getLastSnapshot(): ArrayBuffer | undefined {
    return this.lastSnapshot?.slice(0);
  }

  stop() {
    this.looping = false;
    this.pendingStdin = [];
    this.cancel();
  }

  /** Framed binary records written before Go's stdin exists, flushed on spawn (see writeStdin/run). */
  private pendingStdin: Uint8Array[] = [];

  /**
   * Write a BINARY editor→Go record to Go's stdin, FRAMED as [len:u32-LE][record]
   * (symmetric with the fd-3 content buffer). Accepts either a bare record ArrayBuffer
   * (framed here) or an already-framed Uint8Array. If the process is not yet spawned,
   * BUFFER the framed bytes and flush once stdin exists (in run()) — early writes must not
   * be dropped (that lost the load-time guide-vis push, which races the spawn).
   *
   * Returns void: the TS→Go send is FIRE-AND-FORGET — no await, no request/response
   * (guard: check-no-await-on-bridge.sh).
   */
  writeStdin(record: ArrayBuffer | Uint8Array): void {
    const framed = record instanceof Uint8Array ? record : frameRecord(record);
    if (!this.proc?.stdin) {
      this.pendingStdin.push(framed);
      return;
    }
    this.proc.stdin.write(framed);
  }

  dispose() {
    this.cancel();
    this.channel?.dispose();
  }
}
