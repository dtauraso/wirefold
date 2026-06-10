import * as vscode from "vscode";
import * as cp from "child_process";
import * as fs from "fs";
import * as path from "path";
import type { RunStatus, TraceEvent } from "./messages";
import { TRACE_EVENT_KINDS } from "./webview/three/trace-kinds";

export type { RunStatus };

// Set of every trace-event kind Go can emit, sourced from the GENERATED
// TRACE_EVENT_KINDS (Trace/Trace.go is the single source of truth). Using it here
// keeps the stdout filter from drifting when a kind is added — a new kind flows to
// the pump automatically instead of being silently dropped by a hardcoded list.
const TRACE_EVENT_KIND_SET: ReadonlySet<string> = new Set(TRACE_EVENT_KINDS);

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
    const obj = JSON.parse(line);
    if (
      typeof obj === "object" && obj !== null &&
      typeof obj.step === "number" &&
      typeof obj.kind === "string" &&
      TRACE_EVENT_KIND_SET.has(obj.kind)
    ) {
      return obj as TraceEvent;
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
    const obj = JSON.parse(line);
    if (typeof obj === "object" && obj !== null && obj.kind === "breadcrumb") {
      return obj as Record<string, unknown>;
    }
  } catch { /* not JSON */ }
  return undefined;
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
  private probeFile: string | undefined;
  private goErrorsFile: string | undefined;
  private tsFile: string | undefined;
  private tsErrorsFile: string | undefined;

  constructor(
    private readonly post: (s: RunStatus) => void,
    private readonly onTraceEvent?: (e: TraceEvent) => void,
  ) {}

  run() {
    if (this.proc) return;
    const folder = vscode.workspace.workspaceFolders?.[0];
    if (!folder) return;
    const probeDir = path.join(folder.uri.fsPath, ".probe");
    fs.mkdirSync(probeDir, { recursive: true });
    this.probeFile = path.join(probeDir, "go.jsonl");
    this.goErrorsFile = path.join(probeDir, "go-errors.jsonl");
    this.tsFile = path.join(probeDir, "ts.jsonl");
    this.tsErrorsFile = path.join(probeDir, "ts-errors.jsonl");
    if (!this.channel) this.channel = vscode.window.createOutputChannel("topology run");
    this.channel.clear();
    this.channel.show(true);
    this.channel.appendLine("$ go run .");
    this.cancelled = false;
    this.looping = true;
    this.post({ state: "running" });
    // detached: true makes the child the leader of a new process group, so
    // a kill(-pid) reaches the inner binary `go run` spawned. Without this,
    // SIGTERM hits the `go` driver but leaves the compiled binary orphaned
    // on macOS.
    this.proc = cp.spawn("go", ["run", "."], { cwd: folder.uri.fsPath, detached: true, stdio: ["pipe", "pipe", "pipe"] });
    this.proc.stdout?.on("data", (d: Buffer) => this.handleStdout(d.toString()));
    this.proc.stderr?.on("data", (d: Buffer) => {
      const msg = d.toString();
      this.channel!.append(msg);
      if (this.goErrorsFile) {
        try {
          fs.appendFileSync(this.goErrorsFile, JSON.stringify({ ts_ms: Date.now(), src: "go", kind: "error", message: msg }) + "\n", "utf8");
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
            fs.appendFileSync(this.goErrorsFile, JSON.stringify({ ts_ms: Date.now(), src: "go", kind: "error", message }) + "\n", "utf8");
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
          fs.appendFileSync(this.goErrorsFile, JSON.stringify({ ts_ms: Date.now(), src: "go", kind: "error", message: err.message }) + "\n", "utf8");
        } catch { /* swallow */ }
      }
      this.post({ state: "error", message: err.message });
    });
  }

  private handleStdout(chunk: string) {
    this.stdoutBuf += chunk;
    let nl: number;
    while ((nl = this.stdoutBuf.indexOf("\n")) !== -1) {
      const line = this.stdoutBuf.slice(0, nl);
      this.stdoutBuf = this.stdoutBuf.slice(nl + 1);
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
        const _evNode = 'node' in ev ? ev.node : ('nodeId' in ev ? (ev as { nodeId?: string }).nodeId : undefined);
        const _evPort = 'port' in ev ? (ev as { port?: string }).port : undefined;
        console.log(`[ext] trace-event step=${ev.step} kind=${ev.kind} node=${_evNode} port=${_evPort ?? "-"}`);
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

  cancel() {
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

  pause() {
    if (!this.proc || this.proc.pid === undefined) return;
    try {
      process.kill(-this.proc.pid, "SIGSTOP");
    } catch {
      this.proc.kill("SIGSTOP");
    }
    this.post({ state: "paused" });
  }

  resume() {
    if (!this.proc || this.proc.pid === undefined) return;
    try {
      process.kill(-this.proc.pid, "SIGCONT");
    } catch {
      this.proc.kill("SIGCONT");
    }
    this.post({ state: "running" });
  }

  isRunning(): boolean {
    return this.proc !== undefined;
  }

  stop() {
    this.looping = false;
    this.cancel();
  }

  /** Stop the runner and resolve when the process has fully exited. Resolves
   *  immediately if no process is active. */
  stopAndAwait(): Promise<void> {
    if (!this.proc) return Promise.resolve();
    return new Promise<void>((resolve) => {
      this.proc!.once("close", () => resolve());
      this.stop();
    });
  }

  /** Write a JSON line to the running process's stdin (no-op if not running). */
  writeStdin(line: string): void {
    if (!this.proc?.stdin) return;
    this.proc.stdin.write(line + "\n");
  }

  dispose() {
    this.cancel();
    this.channel?.dispose();
  }
}
