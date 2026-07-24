import * as vscode from "vscode";
import * as cp from "child_process";
import * as fs from "fs";
import * as path from "path";
import type { HostToWebviewMsg } from "./messages";
import { buildBinary, maxGoMtime, killOrphanedSims } from "./goBuild";
import { frameRecord } from "./schema/input-layout";
import { PROBE_DIR, PROBE_FILES } from "./probe-files";
import { decodeBufferLog, decodeStreamFrameEvents } from "./buffer-log";
import { decodeNodeStreamFrame, decodeEdgeStreamFrame, decodeInteriorStreamFrame } from "./webview/three/buffer-decode";
import { BUF_BLOCK_TAG_VIEW, BUF_BLOCK_TAG_EDGE_STREAM, BUF_BLOCK_TAG_NODE_STREAM, BUF_BLOCK_TAG_INTERIOR_STREAM } from "./schema/frame-tags";

// The fd-ALLOCATION contract (mirrors Buffer/stream_fds.go's doc comment): the ext host
// knows the topology from the spec it holds, computes a base fd PER STREAM KIND, and
// passes it to Go via WIREFOLD_STREAM_FDS = "kind:baseFd,kind:baseFd,…". VIEW_FD is the
// base (and, since view is a singleton stream — one gesture/MoveDispatch goroutine
// network-wide — also the ONLY) fd for the "view" kind: fd = baseFd["view"] + rowIndex,
// rowIndex always 0 for this singleton. This is the FIRST stream migrated off fd 3 onto
// its own dedicated inherited pipe (memory/feedback_no_single_writer_bridge.md).
const VIEW_FD = 4;

// EDGE_BASE_FD: the base fd for the "edge" stream kind — one dedicated fd PER EDGE ROW,
// fd = EDGE_BASE_FD + edgeRow (edgeRow = that edge's stable seed-order row, matching
// Buffer's Edge block row order — see nodes/Wiring's MoveDispatch.SetEdgeStreams). Sits
// right after the view fd. Layout today: fd 0-2 stdin/stdout/stderr, fd 3 = fd3 (node/
// interior/port dual-path — see the module doc), fd 4 = view (singleton), fd 5..5+E-1 =
// one per edge (E = countEdges(topologyPath) below).
const EDGE_BASE_FD = 5;

// MAX_EDGE_STREAMS bounds the per-edge fd range: one dedicated pipe PER EDGE (see
// EDGE_BASE_FD's doc comment) — fine for current graph sizes (this is a scaling bound the
// no-single-writer-bridge migration accepts explicitly, not an oversight). A topology with
// more edges than this falls back entirely to the shared fd-3 Edge/Bead path (edgeCount is
// clamped, WIREFOLD_STREAM_FDS omits "edge", Go never calls SetEdgeStreamActive).
const MAX_EDGE_STREAMS = 256;

// NODE_BASE_FD / INTERIOR_BASE_FD: the base fds for the "node" and "interior" stream
// kinds — one dedicated fd PER NODE ROW each, fd = base + nodeRow (nodeRow = that node's
// stable seed-order row, matching Buffer's Node block row order — see nodes/Wiring's
// MoveDispatch.SetNodeStreams / SetInteriorStreams and main.go). Sit right after the edge
// range, computed PER-SPAWN (not module-level constants) since they depend on edgeCount:
// nodeBase = EDGE_BASE_FD + edgeCount, interiorBase = nodeBase + nodeCount. Go's
// stream_fds.go requires BOTH "node" and "interior" WIREFOLD_STREAM_FDS entries present
// together (main.go only wires either when both resolve) — see run() below.

// MAX_NODE_STREAMS bounds the per-node fd range (mirrors MAX_EDGE_STREAMS) — one
// dedicated pipe PER NODE for EACH of node/interior. A topology with more nodes than this
// falls back entirely to the shared fd-3 Node/Interior/Port path.
const MAX_NODE_STREAMS = 256;

// countNodes reads the topology spec's node count WITHOUT the full Go-side validate/build
// pipeline — just enough structure to size the pre-spawn stdio pipe array (mirrors
// countEdges' doc comment and reasoning exactly, substituting the "nodes" directory /
// spec array). Unlike edges/*.json (one flat file per edge), a directory-tree topology's
// nodes/ holds one SUBDIRECTORY per node id (nodes/<id>/{data,meta}.json, inputs/, outputs/
// — see nodes/Wiring/loader.go's parseSpec dispatch and headless_node_row_order_test.go's
// wantNodeRowOrder, the Go-side counterpart this mirrors), so this counts subdirectories,
// not `.json`-suffixed entries. Returns 0 (⇒ no dedicated node/interior fds; the fd-3
// fallback stays active) on any read/parse failure.
export function countNodes(topologyPath: string): number {
  try {
    const st = fs.statSync(topologyPath);
    if (st.isDirectory()) {
      const nodesDir = path.join(topologyPath, "nodes");
      if (!fs.existsSync(nodesDir)) return 0;
      return fs.readdirSync(nodesDir, { withFileTypes: true }).filter((e) => e.isDirectory()).length;
    }
    const raw = fs.readFileSync(topologyPath, "utf8");
    const spec: unknown = JSON.parse(raw);
    if (spec && typeof spec === "object" && Array.isArray((spec as { nodes?: unknown }).nodes)) {
      return (spec as { nodes: unknown[] }).nodes.length;
    }
    return 0;
  } catch {
    return 0;
  }
}

// countEdges reads the topology spec's edge count WITHOUT the full Go-side validate/build
// pipeline — just enough structure to size the pre-spawn stdio pipe array (the ext host
// must know the fd RANGE before spawning Go, so it cannot ask Go for this). Mirrors
// nodes/Wiring/loader.go's parseSpec dispatch: a directory tree (one file per
// `<root>/edges/<label>.json`) or a monolithic topology.json (`{"edges":[...]}`). Returns
// 0 (⇒ no dedicated edge fds; the fd-3 fallback stays active) on any read/parse failure —
// a missing/malformed spec is Go's error to report, not this sizing probe's.
export function countEdges(topologyPath: string): number {
  try {
    const st = fs.statSync(topologyPath);
    if (st.isDirectory()) {
      const edgesDir = path.join(topologyPath, "edges");
      if (!fs.existsSync(edgesDir)) return 0;
      return fs.readdirSync(edgesDir).filter((f) => f.endsWith(".json")).length;
    }
    const raw = fs.readFileSync(topologyPath, "utf8");
    const spec: unknown = JSON.parse(raw);
    if (spec && typeof spec === "object" && Array.isArray((spec as { edges?: unknown }).edges)) {
      return (spec as { edges: unknown[] }).edges.length;
    }
    return 0;
  } catch {
    return 0;
  }
}


/** Format a Go-side error as a probe JSONL line (src="go", kind="error"). */
function goErrorLine(message: string): string {
  return JSON.stringify({ ts_ms: Date.now(), src: "go", kind: "error", message }) + "\n";
}

/** Append a Go-side error line to goErrorsFile, swallowing write failures (the same
 *  best-effort append repeated at every stderr/build-error/close/error call site below). */
function appendGoError(goErrorsFile: string | undefined, message: string): void {
  if (!goErrorsFile) return;
  try {
    fs.appendFileSync(goErrorsFile, goErrorLine(message), "utf8");
  } catch { /* swallow */ }
}

/** The probe-path set derived once per run() from the workspace folder — see
 *  probePathsFor. */
interface ProbePaths {
  probeFile: string;
  probeNodeFile: string;
  probeEdgeFile: string;
  probeInteriorFile: string;
  goErrorsFile: string;
  tsFile: string;
  tsErrorsFile: string;
}

/** Computes (and ensures on disk) the probe-directory file paths for one run. Pure w.r.t.
 *  the runner — returns a plain object rather than writing `this.*` fields, so a caller can
 *  use goErrorsFile to report a build failure BEFORE deciding whether to arm the runner's
 *  own fields (see run()). */
function probePathsFor(folder: vscode.WorkspaceFolder): ProbePaths {
  const probeDir = path.join(folder.uri.fsPath, PROBE_DIR);
  fs.mkdirSync(probeDir, { recursive: true });
  return {
    probeFile: path.join(probeDir, PROBE_FILES.go),
    probeNodeFile: path.join(probeDir, PROBE_FILES.goNode),
    probeEdgeFile: path.join(probeDir, PROBE_FILES.goEdge),
    probeInteriorFile: path.join(probeDir, PROBE_FILES.goInterior),
    goErrorsFile: path.join(probeDir, PROBE_FILES.goErrors),
    tsFile: path.join(probeDir, PROBE_FILES.ts),
    tsErrorsFile: path.join(probeDir, PROBE_FILES.tsErrors),
  };
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

// splitFrames is the pure length-prefix framing step shared by every dedicated stream fd
// (view/edge/node/interior): given the carried-over partial Buffer and a freshly-arrived
// binary chunk, it returns every COMPLETE frame payload (as an ArrayBuffer, ready to
// transfer zero-copy) and the trailing partial `rest` to carry into the next call. Frames
// are [len:u32-LE][payload] with NO tag byte (the fd position identifies the stream — see
// Buffer/stream_fds.go). A frame split across two chunks is reassembled; multiple frames in
// one chunk all come out; a trailing partial (len header not yet complete, or payload bytes
// not yet complete) is buffered. Each handleXFd method owns dispatch; this owns only the
// framing.
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
// handleStdout below) — the .probe trace logs are now the DECODE of each per-owner
// stream's own trailing EVENTS section (decodeBufferLog/decodeStreamFrameEvents, in
// handleViewFd/handleNodeFd/handleEdgeFd/handleInteriorFd).

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

// Per-process incremental parse state for Go's output byte-streams. Each field holds a
// partial fragment straddling a chunk boundary — a partial newline-delimited stdout line,
// and a partial length-prefixed binary frame per dedicated stream fd — and is meaningful
// ONLY within a single Go process's stream: a leftover tail is a fragment of THAT process's
// output. Its lifetime is therefore the process's, not the runner's. fd 3 itself carries no
// frames anymore (WIREFOLD_STREAM_FDS is mandatory — the fd-3 SnapshotState accumulator and
// its fallback frames were deleted, memory/feedback_no_single_writer_bridge.md's final step); the pipe slot
// stays allocated (see run()) purely to keep the remaining fd numbering unchanged.
interface StreamParseState {
  stdoutBuf: string;
  // Partial-frame carry-over for the dedicated VIEW fd (VIEW_FD).
  viewBuf: Buffer;
  // Partial-frame carry-over PER EDGE fd (index = edge row), same role as viewBuf but one
  // per dedicated edge pipe — each is its OWN pipe with its own chunk boundaries.
  edgeBufs: Buffer[];
  // Partial-frame carry-over PER NODE fd / PER INTERIOR fd (index = node row), same role
  // as edgeBufs — one per dedicated node/interior pipe.
  nodeBufs: Buffer[];
  interiorBufs: Buffer[];
}

// freshStreamState mints empty parse state for a newly spawned process. run() calls it
// at every spawn so no respawn/restart path can carry a dead process's tail bytes into
// the next process — concatenating them would make splitFrames read a frame length from
// inside stale bytes and freeze (or silently starve) the scene. Binding the reset to the
// spawn, not to each exit handler, makes "start a process with leftover bytes" impossible
// to express rather than a rule every exit path must remember. edgeCount/nodeCount size
// edgeBufs/nodeBufs+interiorBufs to this spawn's fd ranges (0 when that dedicated path is
// off).
function freshStreamState(edgeCount: number, nodeCount: number): StreamParseState {
  return {
    stdoutBuf: "",
    viewBuf: Buffer.alloc(0),
    edgeBufs: Array.from({ length: edgeCount }, () => Buffer.alloc(0)),
    nodeBufs: Array.from({ length: nodeCount }, () => Buffer.alloc(0)),
    interiorBufs: Array.from({ length: nodeCount }, () => Buffer.alloc(0)),
  };
}

export class BuildAndRunRunner {
  private proc: cp.ChildProcess | undefined;
  // Explicit cancel flag — distinguishing cancellation by signal name races
  // against natural exits, since a process that happened to die from SIGTERM
  // on its own would be misreported as "cancelled".
  private cancelled = false;
  // looping: when true, respawn automatically on natural exit. Set by run(). A
  // deliberate teardown (cancel(), from dispose) does not clear this — it sets
  // `cancelled`, and the close handler's cancelled branch suppresses the respawn.
  private looping = false;
  private channel: vscode.OutputChannel | undefined;
  // Per-process partial-frame parse state (stdout line + each dedicated stream's binary
  // frame). Rebuilt at every spawn by run() (freshStreamState), so its lifetime tracks the
  // Go process, not this long-lived runner — see freshStreamState for why that reset is at
  // the spawn.
  private stream: StreamParseState = freshStreamState(0, 0);
  private probeFile: string | undefined;
  private probeNodeFile: string | undefined;
  private probeEdgeFile: string | undefined;
  private probeInteriorFile: string | undefined;
  private goErrorsFile: string | undefined;
  private tsFile: string | undefined;
  private tsErrorsFile: string | undefined;
  // Last VIEW-stream frame (camera+overlay+scene), kept so a REMOUNTED webview (which holds
  // no state) can be handed a full frame instantly on "ready" without round-tripping to Go —
  // see getLastViewFrame(). This is a keyframe cache of Go's own bytes, not authored state.
  // MUST be a COPY, not the ArrayBuffer instance handed to onSnapshot/postMessage: VS Code's
  // webview.postMessage TRANSFERS (does not clone) ArrayBuffers to the webview process on
  // engines >=1.57 (see the @types/vscode postMessage doc comment — "will be more
  // efficiently transferred to the webview"), which DETACHES the source buffer (byteLength
  // -> 0) once posted. Caching the same reference would silently hand a later "ready" an
  // empty buffer. See runCommand.test.ts for the byteLength assertion.
  private lastViewFrame: ArrayBuffer | undefined;

  // Last frame PER EDGE ROW from the dedicated per-edge streams (see StreamParseState.
  // edgeBufs) — the per-edge analogue of lastViewFrame, keyed by edge row instead of a
  // singleton. Same COPY-before-cache reasoning as lastViewFrame (postMessage transfers/
  // detaches ArrayBuffers). Cleared on every spawn.
  private lastEdgeFrames: Map<number, ArrayBuffer> = new Map();
  // Current spawn's edge-fd count. Recomputed at every run() call from the topology spec.
  private edgeCount = 0;

  // Last frame PER NODE ROW from the dedicated per-node NODE streams, and separately from
  // the dedicated per-node INTERIOR streams — the per-node analogues of lastEdgeFrames, one
  // map per stream kind since a node's geometry/ports/label and its interior beads are
  // written by two DIFFERENT goroutines (memory/feedback_no_single_writer_bridge.md) onto
  // two different fds. Same COPY-before-cache reasoning as lastViewFrame. Cleared on every
  // spawn.
  private lastNodeFrames: Map<number, ArrayBuffer> = new Map();
  private lastInteriorFrames: Map<number, ArrayBuffer> = new Map();
  // Current spawn's node-fd count. Recomputed at every run() call from the topology spec.
  private nodeCount = 0;

  private topologyPath: string | undefined;

  constructor(
    private readonly onSnapshot?: (msg: HostToWebviewMsg & { type: "buffer-snapshot" }) => void,
  ) {}

  run(topologyPath?: string) {
    if (this.proc) {
      // Already spawned: return silently, posting nothing. A webview that remounts
      // (reopened file, hot reload) re-learns liveness via the "ready" handler, which
      // replays the ext host's cached snapshot instead (see handle-message.ts's
      // wasRunning branch / BuildAndRunRunner.getLastSnapshot).
      return;
    }
    if (topologyPath) this.topologyPath = topologyPath;
    const folder = vscode.workspace.workspaceFolders?.[0];
    if (!folder) return;
    // Channel setup happens here (not folded into the post-build block below) because the
    // build-failure branch needs a visible place to report the error to the user, same as
    // before this refactor.
    if (!this.channel) this.channel = vscode.window.createOutputChannel("topology run");
    this.channel.clear();
    this.channel.show(true);
    const repoRoot = folder.uri.fsPath;
    const binPath = path.join(repoRoot, ".wirefold-cache", "wirefold");
    const topArgs = this.topologyPath ? ["-topology", this.topologyPath] : [];
    // probePathsFor computes the probe-directory paths as PLAIN LOCALS (not yet written to
    // `this.*`) so the build-failure branch below can log to goErrorsFile without arming
    // any of the runner's own fields — see the ProbePaths/probePathsFor doc comment.
    const probePaths = probePathsFor(folder);
    // Build the binary once (and only rebuild when a .go source changed) instead of
    // relinking a throwaway binary via `go run .` on every start/restart.
    const built = ensureBinaryBuilt(repoRoot, binPath);
    if (!built.ok) {
      this.channel.appendLine(`\n[build error: ${built.error}]`);
      appendGoError(probePaths.goErrorsFile, built.error);
      this.looping = false;
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

    // Build (and orphan-reap) succeeded: from here on, arm every receiver field this run()
    // call touches in ONE uninterrupted run immediately before cp.spawn, so no early return
    // inserted above this point can ever leave probeFile/.../stream/lastSnapshot half-set —
    // see the ProbePaths doc comment for why probePaths was computed as locals above.
    this.probeFile = probePaths.probeFile;
    this.probeNodeFile = probePaths.probeNodeFile;
    this.probeEdgeFile = probePaths.probeEdgeFile;
    this.probeInteriorFile = probePaths.probeInteriorFile;
    this.goErrorsFile = probePaths.goErrorsFile;
    this.tsFile = probePaths.tsFile;
    this.tsErrorsFile = probePaths.tsErrorsFile;
    if (killed > 0) {
      this.channel.appendLine(`[cleanup] killed ${killed} orphaned sim process(es)`);
    }
    this.channel.appendLine("$ " + binPath + " " + topArgs.join(" "));
    this.cancelled = false;
    this.looping = true;
    // Size the dedicated per-edge fd range from the topology spec BEFORE spawning (the
    // ext host must know the range up front — see countEdges' doc comment). Clamped to
    // MAX_EDGE_STREAMS; 0 (spec unreadable, or more edges than the bound) falls back
    // entirely to the shared fd-3 Edge/Bead path — see MAX_EDGE_STREAMS's doc comment.
    const edgeCountRaw = countEdges(this.topologyPath ?? path.join(repoRoot, "topology"));
    this.edgeCount = edgeCountRaw > MAX_EDGE_STREAMS ? 0 : edgeCountRaw;
    // Size the dedicated per-node NODE + INTERIOR fd ranges the same way, right after the
    // edge range (nodeBase = EDGE_BASE_FD + edgeCount, interiorBase = nodeBase + nodeCount —
    // see NODE_BASE_FD's doc comment). Clamped to MAX_NODE_STREAMS; 0 falls back entirely to
    // the shared fd-3 Node/Interior/Port path.
    const nodeCountRaw = countNodes(this.topologyPath ?? path.join(repoRoot, "topology"));
    this.nodeCount = nodeCountRaw > MAX_NODE_STREAMS ? 0 : nodeCountRaw;
    const nodeBaseFd = EDGE_BASE_FD + this.edgeCount;
    const interiorBaseFd = nodeBaseFd + this.nodeCount;
    // Fresh parse state for this spawn: a prior process's leftover partial frame must
    // never prefix this one's stream (see freshStreamState). This is the single reset
    // point every restart path funnels through, including the looping respawn.
    this.stream = freshStreamState(this.edgeCount, this.nodeCount);
    // Also drop the cached keyframes: they belong to the PRIOR process. Without this,
    // a webview remounting in the window between "ready" and the new process's first
    // frame would be replayed the previous process's frames via getLastViewFrame()/
    // getLastEdgeFrames()/etc. The freshly spawned Go emits its full state again, so
    // continuity is preserved by that emit — not by re-serving one process's bytes as
    // another's.
    this.lastViewFrame = undefined;
    this.lastEdgeFrames.clear();
    this.lastNodeFrames.clear();
    this.lastInteriorFrames.clear();
    // detached: true makes the child the leader of a new process group; the
    // prebuilt binary is the sole group member, so kill(-pid) reaches it
    // directly. Without this, SIGTERM could leave it orphaned on macOS.
    // stdio index 3 is a RESERVED, UNUSED pipe slot: Go no longer writes anything to fd 3
    // (Buffer.SnapshotState — the central accumulator that used to write it, plus its
    // fallback frames — was deleted entirely; memory/feedback_no_single_writer_bridge.md's final step). The
    // slot stays allocated purely so the remaining fd numbering (VIEW_FD=4, edge/node/
    // interior ranges after it) matches this file's existing constants unchanged. stdio
    // index VIEW_FD (4) = the dedicated VIEW-stream pipe (WIREFOLD_STREAM_FDS=
    // "view:<VIEW_FD>"). stdio indices EDGE_BASE_FD..EDGE_BASE_FD+edgeCount-1 are one
    // dedicated pipe PER EDGE (see EDGE_BASE_FD's doc comment); the next nodeCount indices
    // are one dedicated pipe PER NODE (the "node" stream — geometry + ports + label); the
    // FOLLOWING nodeCount indices are one dedicated pipe PER NODE again (the "interior"
    // stream — that node's own interior beads, a SEPARATE goroutine's fd — see
    // NODE_BASE_FD's doc comment). Any of these ranges is omitted (and its kind left out
    // of WIREFOLD_STREAM_FDS) when its count is 0 (e.g. a topology with no edges) — Go
    // simply never streams that kind. "pipe" opens a readable pipe at each index; the
    // existing stdin(0)/stdout(1)/stderr(2) are unchanged.
    const stdio: Array<"pipe"> = ["pipe", "pipe", "pipe", "pipe", "pipe"];
    for (let i = 0; i < this.edgeCount; i++) stdio.push("pipe");
    for (let i = 0; i < this.nodeCount; i++) stdio.push("pipe");
    for (let i = 0; i < this.nodeCount; i++) stdio.push("pipe");
    const streamFDsEnvParts = [`view:${VIEW_FD}`];
    if (this.edgeCount > 0) streamFDsEnvParts.push(`edge:${EDGE_BASE_FD}`);
    // Go's stream_fds.go / main.go only wires the per-node node+interior streams when
    // BOTH "node" and "interior" env entries resolve — always emit them together.
    if (this.nodeCount > 0) {
      streamFDsEnvParts.push(`node:${nodeBaseFd}`, `interior:${interiorBaseFd}`);
    }
    const streamFDsEnv = streamFDsEnvParts.join(",");
    this.proc = cp.spawn(binPath, [...topArgs], {
      cwd: repoRoot,
      detached: true,
      stdio,
      env: {
        ...process.env,
        WIREFOLD_BUF_OUT_FD: "3",
        WIREFOLD_STREAM_FDS: streamFDsEnv,
      },
    });
    // Flush any framed binary records buffered before this spawn (writeStdin queued them).
    if (this.pendingStdin.length > 0) {
      for (const rec of this.pendingStdin) this.proc.stdin?.write(rec);
      this.pendingStdin = [];
    }
    this.proc.stdout?.on("data", (d: Buffer) => this.handleStdout(d.toString()));
    // stdio index 3 is a reserved, unused pipe (see the stdio comment above) — nothing
    // reads it; Go writes nothing to it.
    // VIEW_FD: the dedicated view-stream pipe. Cast needed because Node's ChildProcess
    // types only narrow stdio[0..2]; higher indices are typed as Readable|null via the
    // array form.
    const viewFd = (this.proc.stdio as (NodeJS.ReadableStream | null)[])[VIEW_FD];
    if (viewFd) {
      viewFd.on("data", (d: Buffer) => this.handleViewFd(d));
    }
    // Per-edge dedicated pipes: EDGE_BASE_FD..EDGE_BASE_FD+edgeCount-1, one per edge row.
    for (let row = 0; row < this.edgeCount; row++) {
      const fdIdx = EDGE_BASE_FD + row;
      const edgeFd = (this.proc.stdio as (NodeJS.ReadableStream | null)[])[fdIdx];
      if (edgeFd) {
        edgeFd.on("data", (d: Buffer) => this.handleEdgeFd(row, d));
      }
    }
    // Per-node dedicated pipes: nodeBaseFd..nodeBaseFd+nodeCount-1 (NODE stream, geometry+
    // ports+label) and interiorBaseFd..interiorBaseFd+nodeCount-1 (INTERIOR stream, that
    // node's own interior beads — a separate goroutine's fd, see NODE_BASE_FD's doc comment).
    for (let row = 0; row < this.nodeCount; row++) {
      const nodeFdIdx = nodeBaseFd + row;
      const nodeFd = (this.proc.stdio as (NodeJS.ReadableStream | null)[])[nodeFdIdx];
      if (nodeFd) {
        nodeFd.on("data", (d: Buffer) => this.handleNodeFd(row, d));
      }
      const interiorFdIdx = interiorBaseFd + row;
      const interiorFd = (this.proc.stdio as (NodeJS.ReadableStream | null)[])[interiorFdIdx];
      if (interiorFd) {
        interiorFd.on("data", (d: Buffer) => this.handleInteriorFd(row, d));
      }
    }
    this.proc.stderr?.on("data", (d: Buffer) => {
      const msg = d.toString();
      this.channel!.append(msg);
      appendGoError(this.goErrorsFile, msg);
    });
    this.proc.on("close", (code) => {
      const cancelled = this.cancelled;
      const looping = this.looping;
      this.proc = undefined;
      this.cancelled = false;
      if (cancelled) {
        this.channel!.appendLine("\n[cancelled]");
      } else if (looping) {
        // Natural exit while looping — respawn immediately.
        this.channel!.appendLine(code === 0 ? "\n[ok — restarting]" : `\n[exit ${code} — restarting]`);
        this.run();
      } else if (code === 0) {
        this.channel!.appendLine("\n[ok]");
      } else {
        const message = `exit code ${code}`;
        this.channel!.appendLine(`\n[${message}]`);
        appendGoError(this.goErrorsFile, message);
      }
    });
    this.proc.on("error", (err) => {
      this.proc = undefined;
      this.cancelled = false;
      this.channel!.appendLine(`\n[spawn error: ${err.message}]`);
      appendGoError(this.goErrorsFile, err.message);
    });
  }

  private handleStdout(chunk: string) {
    const { lines, rest } = splitJsonlLines(this.stream.stdoutBuf, chunk);
    this.stream.stdoutBuf = rest;
    for (const line of lines) {
      // Trace events (and, since task/breadcrumbs-binary-buffer, DEBUG BREADCRUMBs too)
      // are NO LONGER emitted on stdout: Go's JSON-trace emitter was removed and the
      // .probe log is now the DECODE of each per-owner stream's own trailing EVENTS
      // section (see handleViewFd/handleNodeFd/handleEdgeFd/handleInteriorFd below,
      // and buffer-log.ts's "breadcrumb" case). The ext host therefore no longer parses
      // any structured line from stdout; any stdout line here is just process output.
      this.channel!.appendLine(line);
    }
  }

  // handleViewFd parses the dedicated VIEW-stream pipe (VIEW_FD): frames are
  // [len:u32][payload] with NO tag byte (the fd position already identifies the stream —
  // see Buffer/stream_fds.go / frame-tags.ts). Relayed to the webview under the
  // "buffer-snapshot" message shape, tagged BUF_BLOCK_TAG_VIEW (a synthetic ext-host-side
  // tag, never a wire byte).
  private handleViewFd(chunk: Buffer) {
    const { frames, rest } = splitFrames(this.stream.viewBuf, chunk);
    this.stream.viewBuf = rest;
    for (const ab of frames) {
      // Decode this frame's OWN trailing EVENTS section (camera/overlay/scene events —
      // every other trace kind is decentralized to its own owner fd; memory/
      // feedback_no_single_writer_bridge.md). Written to its OWN .probe file (go.jsonl) —
      // N separate logs, never merged on write.
      if (this.probeFile) {
        const lines = decodeBufferLog(ab);
        if (lines.length > 0) {
          try {
            fs.appendFileSync(this.probeFile, lines, "utf8");
          } catch { /* swallow */ }
        }
      }
      // Cache a COPY before handing `ab` off — see the lastViewFrame field comment for why
      // the reference itself cannot be cached (postMessage may transfer/detach it).
      this.lastViewFrame = ab.slice(0);
      if (this.onSnapshot) {
        this.onSnapshot({ type: "buffer-snapshot", buffer: ab, tag: BUF_BLOCK_TAG_VIEW });
      }
    }
  }

  // handleEdgeFd parses ONE dedicated per-edge stream pipe (fd = EDGE_BASE_FD + row):
  // frames are [len:u32][payload] with NO tag byte (the fd position already identifies
  // WHICH edge — see Buffer/stream_fds.go / Buffer/edge_stream_frame.go). splitFrames is
  // reused as-is, same as handleViewFd. Each decoded frame is relayed to the webview under
  // the SAME "buffer-snapshot" shape as the other tags, tagged BUF_BLOCK_TAG_EDGE_STREAM
  // (synthetic, never a wire byte) PLUS `row` so the webview routes it to the right
  // per-edge cell (there are many edge streams, unlike view's singleton).
  private handleEdgeFd(row: number, chunk: Buffer) {
    const carry = this.stream.edgeBufs[row] ?? Buffer.alloc(0);
    const { frames, rest } = splitFrames(carry, chunk);
    this.stream.edgeBufs[row] = rest;
    for (const ab of frames) {
      // Decode this edge's OWN trailing EVENTS section (Geometry/Position/Arrive — this
      // goroutine's own row-resolved events; memory/feedback_no_single_writer_bridge.md).
      // Written to its OWN .probe file (go-edge.jsonl) — N separate logs, never merged.
      if (this.probeEdgeFile) {
        const decoded = decodeEdgeStreamFrame(row, ab);
        if (decoded && decoded.eventCount > 0) {
          const lines = decodeStreamFrameEvents(decoded.eventCount, decoded.eventView, decoded.eventTextView);
          if (lines.length > 0) {
            try {
              fs.appendFileSync(this.probeEdgeFile, lines, "utf8");
            } catch { /* swallow */ }
          }
        }
      }
      // Cache under this edge row (same copy-before-hand-off reasoning as lastViewFrame).
      this.lastEdgeFrames.set(row, ab.slice(0));
      if (this.onSnapshot) {
        this.onSnapshot({ type: "buffer-snapshot", buffer: ab, tag: BUF_BLOCK_TAG_EDGE_STREAM, row });
      }
    }
  }

  // handleNodeFd parses ONE dedicated per-node NODE stream pipe (fd = nodeBaseFd + row):
  // frames are [len:u32][payload] with NO tag byte (the fd position already identifies
  // WHICH node — see Buffer/stream_fds.go / Buffer/node_stream_frame.go). splitFrames is
  // reused as-is, same as handleEdgeFd. Each decoded frame is relayed to the webview under
  // the SAME "buffer-snapshot" shape, tagged BUF_BLOCK_TAG_NODE_STREAM (synthetic, never a
  // wire byte) PLUS `row` so the webview routes it to the right per-node cell.
  private handleNodeFd(row: number, chunk: Buffer) {
    const carry = this.stream.nodeBufs[row] ?? Buffer.alloc(0);
    const { frames, rest } = splitFrames(carry, chunk);
    this.stream.nodeBufs[row] = rest;
    for (const ab of frames) {
      // Decode this node's OWN trailing EVENTS section (NodeGeometry — this nodeMover
      // goroutine's own row-resolved event; memory/feedback_no_single_writer_bridge.md).
      // Written to its OWN .probe file (go-node.jsonl) — N separate logs, never merged.
      if (this.probeNodeFile) {
        const decoded = decodeNodeStreamFrame(row, ab);
        if (decoded && decoded.eventCount > 0) {
          const lines = decodeStreamFrameEvents(decoded.eventCount, decoded.eventView, decoded.eventTextView);
          if (lines.length > 0) {
            try {
              fs.appendFileSync(this.probeNodeFile, lines, "utf8");
            } catch { /* swallow */ }
          }
        }
      }
      // Cache under this node row (same copy-before-hand-off reasoning as lastViewFrame).
      this.lastNodeFrames.set(row, ab.slice(0));
      if (this.onSnapshot) {
        this.onSnapshot({ type: "buffer-snapshot", buffer: ab, tag: BUF_BLOCK_TAG_NODE_STREAM, row });
      }
    }
  }

  // handleInteriorFd parses ONE dedicated per-node INTERIOR stream pipe (fd =
  // interiorBaseFd + row) — that node's OWN Update goroutine (a SEPARATE goroutine from
  // its nodeMover), same framing/relay shape as handleNodeFd, tagged
  // BUF_BLOCK_TAG_INTERIOR_STREAM.
  private handleInteriorFd(row: number, chunk: Buffer) {
    const carry = this.stream.interiorBufs[row] ?? Buffer.alloc(0);
    const { frames, rest } = splitFrames(carry, chunk);
    this.stream.interiorBufs[row] = rest;
    for (const ab of frames) {
      // Decode this node's OWN trailing EVENTS section (NodeBead — this node's own
      // Update-loop goroutine's row-resolved events; memory/feedback_no_single_writer_bridge.md).
      // Written to its OWN .probe file (go-interior.jsonl) — N separate logs, never merged.
      if (this.probeInteriorFile) {
        const decoded = decodeInteriorStreamFrame(row, ab);
        if (decoded && decoded.eventCount > 0) {
          const lines = decodeStreamFrameEvents(decoded.eventCount, decoded.eventView, decoded.eventTextView);
          if (lines.length > 0) {
            try {
              fs.appendFileSync(this.probeInteriorFile, lines, "utf8");
            } catch { /* swallow */ }
          }
        }
      }
      this.lastInteriorFrames.set(row, ab.slice(0));
      if (this.onSnapshot) {
        this.onSnapshot({ type: "buffer-snapshot", buffer: ab, tag: BUF_BLOCK_TAG_INTERIOR_STREAM, row });
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

  isRunning(): boolean {
    return this.proc !== undefined;
  }

  /** The most recent VIEW-stream frame (camera+overlay+scene), or undefined if none has
   *  arrived yet. Used by the "ready" handler to hand a remounted webview the cached frame
   *  instantly (see the lastViewFrame field comment).
   *
   *  The returned buffer is a FRESH COPY, because the caller posts what it gets and
   *  webview.postMessage TRANSFERS ArrayBuffers — handing out the cached reference
   *  would detach our own cache on the first serve. That breaks the exact case this
   *  cache exists for: while PAUSED no new frame ever arrives to repopulate it, so a
   *  second remount would be served a zero-length buffer. The copy is one per remount. */
  getLastViewFrame(): ArrayBuffer | undefined {
    return this.lastViewFrame?.slice(0);
  }

  /** The most recent frame for EVERY cached edge row (see lastEdgeFrames), or an empty
   *  array if none has arrived yet. Used by the "ready" handler to hand a remounted webview
   *  every edge's last frame instantly, the per-edge analogue of getLastViewFrame(). Same
   *  fresh-copy-per-remount reasoning. */
  getLastEdgeFrames(): Array<{ row: number; buffer: ArrayBuffer }> {
    return Array.from(this.lastEdgeFrames, ([row, buffer]) => ({ row, buffer: buffer.slice(0) }));
  }

  /** The most recent frame for EVERY cached node row from the dedicated NODE stream, the
   *  per-node analogue of getLastEdgeFrames. */
  getLastNodeFrames(): Array<{ row: number; buffer: ArrayBuffer }> {
    return Array.from(this.lastNodeFrames, ([row, buffer]) => ({ row, buffer: buffer.slice(0) }));
  }

  /** The most recent frame for EVERY cached node row from the dedicated INTERIOR stream. */
  getLastInteriorFrames(): Array<{ row: number; buffer: ArrayBuffer }> {
    return Array.from(this.lastInteriorFrames, ([row, buffer]) => ({ row, buffer: buffer.slice(0) }));
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
