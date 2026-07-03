import * as vscode from "vscode";
import * as cp from "child_process";
import * as fs from "fs";
import * as path from "path";
import type { RunStatus, TraceEvent, HostToWebviewMsg } from "./messages";
import { TRACE_EVENT_KINDS } from "./schema/trace-kinds";
import { validateNodeStatusFields } from "./schema/trace-event-fields";
import { buildBinary, maxGoMtime, killOrphanedSims } from "./goBuild";
import { encodePlay, encodePause, encodeResend, frameRecord } from "./schema/input-layout";
import { PROBE_DIR, PROBE_FILES } from "./probe-files";
import {
  BUF_HEADER_SIZE,
  BEAD_STRIDE,
  NODE_STRIDE,
  NODE_COL_EV_RECV,
  NODE_COL_EV_FIRE,
  NODE_COL_EV_SEND,
  NODE_COL_EV_ARRIVE,
  NODE_COL_EV_DONE,
} from "./schema/buffer-layout";

export type { RunStatus };

// Set to true locally to log every Go trace event to the extension console (~60Hz/wire).
const DEBUG_TRACE = false;

// Set of every trace-event kind Go can emit, sourced from the GENERATED
// TRACE_EVENT_KINDS (Trace/Trace.go is the single source of truth). Using it here
// keeps the stdout filter from drifting when a kind is added — a new kind flows to
// the pump automatically instead of being silently dropped by a hardcoded list.
const TRACE_EVENT_KIND_SET: ReadonlySet<string> = new Set(TRACE_EVENT_KINDS);

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

// decodeSnapshotEvents reads the transient per-node event columns (EvRecv/Fire/Send/
// Arrive/Done) from a snapshot payload and returns human-readable log lines — one
// entry per set event flag across all nodes. Returns an empty array when no events
// are flagged. Used to append to the .probe log.
export function decodeSnapshotEvents(ab: ArrayBuffer): string[] {
  const view = new DataView(ab);
  if (ab.byteLength < BUF_HEADER_SIZE) return [];
  const tick = view.getUint32(0, true);
  const beadCount = view.getUint32(4, true);
  const nodeCount = view.getUint32(8, true);
  const nodeBlockOff = BUF_HEADER_SIZE + beadCount * BEAD_STRIDE;
  const lines: string[] = [];
  const eventCols: [number, string][] = [
    [NODE_COL_EV_RECV, "recv"],
    [NODE_COL_EV_FIRE, "fire"],
    [NODE_COL_EV_SEND, "send"],
    [NODE_COL_EV_ARRIVE, "arrive"],
    [NODE_COL_EV_DONE, "done"],
  ];
  for (let i = 0; i < nodeCount; i++) {
    const rowOff = nodeBlockOff + i * NODE_STRIDE;
    if (rowOff + NODE_STRIDE > ab.byteLength) break;
    for (const [col, name] of eventCols) {
      const flag = view.getUint8(rowOff + col);
      if (flag !== 0) {
        lines.push(JSON.stringify({ ts_ms: Date.now(), src: "buf", kind: "buf-event", tick, node: i, event: name }) + "\n");
      }
    }
  }
  return lines;
}

// Go stdout relay: trace events are written to .probe/go.jsonl with a
// shared envelope { ts_ms, src:"go", step?, ...ev }. Errors (stderr,
// non-zero exit, spawn failure) are written to .probe/go-errors.jsonl.
//
// tryParseTraceEvent: a stdout line is a trace event when it's valid JSON with a
// numeric `step` and a `kind` in the generated TRACE_EVENT_KINDS set. Validating
// against the generated set (not a hardcoded literal list) means every Go kind —
// recv/fire/send/done/position/geometry/pulse-cancelled and any future addition —
// is recognized and forwarded to the pump without per-kind edits here.
function tryParseTraceEvent(line: string): TraceEvent | undefined {
  if (!line.startsWith("{")) return undefined;
  try {
    const obj: unknown = JSON.parse(line);
    if (typeof obj === "object" && obj !== null) {
      const rec = obj as Record<string, unknown>;
      if (
        typeof rec.step === "number" &&
        typeof rec.kind === "string" &&
        TRACE_EVENT_KIND_SET.has(rec.kind)
      ) {
        // Per-kind FIELD validation: the kind-string set only proves the discriminant.
        // node-status carries a typed payload (generated from Trace.go); drop a
        // malformed one rather than casting it through and painting garbage.
        if (rec.kind === "node-status" && !validateNodeStatusFields(rec)) return undefined;
        return obj as TraceEvent;
      }
    }
  } catch { /* not JSON */ }
  return undefined;
}

// tryParseSpecLine recognizes the Go startup spec line {"kind":"spec","nodes":[...],...}.
// Spec lines carry no step ordinal and are not in TRACE_EVENT_KINDS; they are handled
// specially: intercepted before tryParseTraceEvent and forwarded via onSpecEvent.
function tryParseSpecLine(line: string): { nodes: unknown[]; edges: unknown[]; view?: unknown } | undefined {
  if (!line.startsWith("{")) return undefined;
  try {
    const obj: unknown = JSON.parse(line);
    if (typeof obj === "object" && obj !== null) {
      const rec = obj as Record<string, unknown>;
      if (rec.kind === "spec" && Array.isArray(rec.nodes) && Array.isArray(rec.edges)) {
        return obj as { nodes: unknown[]; edges: unknown[]; view?: unknown };
      }
    }
  } catch { /* not JSON */ }
  return undefined;
}

// tryParseBreadcrumb recognizes the Go Trace.Breadcrumb line shape
// ({"kind":"breadcrumb","label":...}). Breadcrumbs are logging-only and
// carry no step ordinal, so tryParseTraceEvent rejects them.
function tryParseBreadcrumb(line: string): Record<string, unknown> | undefined {
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
  const maxAttempts = 50;
  for (let attempt = 0; attempt < maxAttempts; attempt++) {
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
  return { ok: false, error: `binary ${binPath} not built after ${maxAttempts} attempts` };
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
  private tsFile: string | undefined;
  private tsErrorsFile: string | undefined;

  private topologyPath: string | undefined;

  constructor(
    private readonly post: (s: RunStatus) => void,
    private readonly onTraceEvent?: (e: TraceEvent) => void,
    private readonly onSpecEvent?: (spec: { nodes: unknown[]; edges: unknown[]; view?: unknown }) => void,
    private readonly onSnapshot?: (msg: HostToWebviewMsg & { type: "buffer-snapshot" }) => void,
  ) {}

  run(topologyPath?: string) {
    if (this.proc) return;
    if (topologyPath) this.topologyPath = topologyPath;
    const folder = vscode.workspace.workspaceFolders?.[0];
    if (!folder) return;
    const probeDir = path.join(folder.uri.fsPath, PROBE_DIR);
    fs.mkdirSync(probeDir, { recursive: true });
    this.probeFile = path.join(probeDir, PROBE_FILES.go);
    this.goErrorsFile = path.join(probeDir, PROBE_FILES.goErrors);
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
    // Do NOT post "running" here — Go starts HALTED. The clock state drives status:
    // idle-on-spawn → running-on-play() → paused-on-pause().
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
      // Spec line — Go startup message carrying the full topology spec. Intercepted
      // before the trace-event check (no step ordinal, not in TRACE_EVENT_KINDS).
      const spec = tryParseSpecLine(line);
      if (spec) {
        this.onSpecEvent?.(spec);
        continue;
      }
      // Breadcrumb lines (logging-only; no step ordinal, outside the closed
      // trace vocabulary) are relayed to go.jsonl but never dispatched to the
      // pump — the pump's assertNever would throw on an unknown kind.
      const crumb = tryParseBreadcrumb(line);
      if (crumb) {
        if (this.probeFile) {
          try {
            fs.appendFileSync(this.probeFile, JSON.stringify({ ts_ms: Date.now(), src: "go", ...crumb }) + "\n", "utf8");
          } catch { /* swallow */ }
        }
        continue;
      }
      const ev = tryParseTraceEvent(line);
      if (ev && this.onTraceEvent) {
        const _evNode = 'node' in ev ? ev.node : undefined;
        const _evPort = 'port' in ev ? (ev as { port?: string }).port : undefined;
        if (DEBUG_TRACE) console.log(`[ext] trace-event step=${ev.step} kind=${ev.kind} node=${_evNode} port=${_evPort ?? "-"}`);
        if (this.tsFile) {
          try {
            fs.appendFileSync(this.tsFile, JSON.stringify({ ts_ms: Date.now(), src: "ts-ext", label: "ext.trace-event", kind: ev.kind, node: _evNode, port: _evPort ?? null }) + "\n", "utf8");
          } catch { /* swallow */ }
        }
        if (this.probeFile) {
          try {
            fs.appendFileSync(this.probeFile, JSON.stringify({ ts_ms: Date.now(), src: "go", ...(typeof ev.step === "number" ? { step: ev.step } : {}), ...ev }) + "\n", "utf8");
          } catch { /* swallow */ }
        }
        this.onTraceEvent(ev);
      } else {
        this.channel!.appendLine(line);
      }
    }
  }

  private handleFd3(chunk: Buffer) {
    const { frames, rest } = splitFrames(this.fd3Buf, chunk);
    this.fd3Buf = rest;
    for (const ab of frames) {
      // Decode transient event columns and append to the .probe log.
      if (this.probeFile) {
        const lines = decodeSnapshotEvents(ab);
        if (lines.length > 0) {
          try {
            fs.appendFileSync(this.probeFile, lines.join(""), "utf8");
          } catch { /* swallow */ }
        }
      }
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

  /** Send play to Go's stdin — resumes the clock gate. Fire-and-forget. */
  play(): void {
    if (!this.proc) return;
    this.writeStdin(encodePlay());
    this.post({ state: "running" });
  }

  /** Send pause to Go's stdin — halts the clock gate. Fire-and-forget. */
  pause(): void {
    if (!this.proc) return;
    this.writeStdin(encodePause());
    this.post({ state: "paused" });
  }

  /** Alias for play() — retained so existing handle-message case "resume" still works. */
  resume(): void {
    this.play();
  }

  isRunning(): boolean {
    return this.proc !== undefined;
  }

  /** Ask the running Go to re-emit its full current node + edge geometry. Used after a
   *  webview remount (e.g. hot-reload), which resets the TS edge-geometry store but
   *  leaves Go running. Fire-and-forget; no-op if not running. */
  resend(): void {
    if (!this.proc) return;
    this.writeStdin(encodeResend());
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
