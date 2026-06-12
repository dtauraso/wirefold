import * as cp from "child_process";
import * as fs from "fs";
import * as path from "path";

// Directories excluded from the .go-mtime walk — mirrors the repo's bash-hygiene
// exclusions so the walk stays fast and never touches node_modules.
const GO_WALK_EXCLUDE = new Set([
  "node_modules", ".git", "out", ".probe", ".wirefold-cache", "handoff-archive",
]);

// maxGoMtime walks the repo collecting the newest mtime among *.go source files.
// Returns 0 if none found. The Go tree is small, so a plain recursive walk is fine.
export function maxGoMtime(dir: string): number {
  let max = 0;
  let entries: fs.Dirent[];
  try {
    entries = fs.readdirSync(dir, { withFileTypes: true });
  } catch {
    return max;
  }
  for (const ent of entries) {
    const full = path.join(dir, ent.name);
    if (ent.isDirectory()) {
      if (GO_WALK_EXCLUDE.has(ent.name)) continue;
      const sub = maxGoMtime(full);
      if (sub > max) max = sub;
    } else if (ent.isFile() && ent.name.endsWith(".go")) {
      try {
        const m = fs.statSync(full).mtimeMs;
        if (m > max) max = m;
      } catch { /* skip */ }
    }
  }
  return max;
}

export type BuildResult =
  | { ok: true; busy?: boolean }
  | { ok: false; error: string };

// Concurrency guard: a single module-level flag serializes every caller of
// buildBinary against the same `go build -o <binPath>` target. Two concurrent
// `go build` writes to one output path can corrupt the binary, so the second
// caller coalesces (returns ok with busy:true) rather than running in parallel.
// This is a wait-free skip — no caller ever blocks waiting for the build — which
// is what keeps the lazy run() path from ever deadlocking against the eager
// watcher: if the watcher is mid-build, run() skips the build and proceeds (the
// in-flight build is producing a fresh binary anyway).
let building = false;

// buildBinary runs `go build -o binPath .` from repoRoot, creating the cache dir
// first. It is the single build entry point shared by the lazy runner
// (ensureBinaryBuilt) and the eager .go file watcher. Guarded so only one build
// runs at a time against the shared output path.
// killOrphanedSims reaps leftover wirefold sim processes from prior or crashed
// editor sessions. A normal close reaps the runner's own group, but a crash or
// hard-quit can leave the prebuilt binary (or legacy `go run .` / compiled
// `exe/wirefold -topology`) running. Those orphans pile up and, worse, keep
// accepting edits on a stale binary that no longer persists.
//
// Single-panel assumption: this kills ALL matching wirefold sims except the
// active one (exceptPid). If two editor panels were intentionally running sims
// at once, the older one would be killed. That is acceptable for this
// single-user dev tool — we do not scope per-panel.
//
// POSIX only: on win32 there is no `ps -axo` and the dev env is darwin, so we
// no-op there rather than block on Windows support.
export function killOrphanedSims(binPath: string, exceptPid?: number): { killed: number } {
  if (process.platform !== "darwin" && process.platform !== "linux") {
    return { killed: 0 };
  }
  let out: string;
  try {
    const res = cp.spawnSync("ps", ["-axo", "pid=,command="], { encoding: "utf8" });
    if (res.status !== 0 || typeof res.stdout !== "string") return { killed: 0 };
    out = res.stdout;
  } catch {
    return { killed: 0 };
  }
  const self = process.pid;
  let killed = 0;
  for (const rawLine of out.split("\n")) {
    const line = rawLine.trim();
    if (!line) continue;
    const sp = line.indexOf(" ");
    if (sp < 0) continue;
    const pid = Number(line.slice(0, sp));
    if (!Number.isInteger(pid) || pid <= 0) continue;
    const command = line.slice(sp + 1);
    // Never touch the extension host, the active sim, or our own `ps` invocation
    // (ps lists itself). The ps pid is excluded implicitly: its command line is
    // `ps -axo pid=,command=`, which does not match the wirefold matcher below.
    if (pid === self || (exceptPid !== undefined && pid === exceptPid)) continue;
    // Match only genuine wirefold sims. Require "wirefold" AND one of:
    //   - the prebuilt cache path (binPath, e.g. ".wirefold-cache/wirefold")
    //   - "-topology" (legacy `go run .` and compiled `exe/wirefold -topology`
    //     are always launched with the -topology arg)
    // The conjunction avoids killing unrelated processes that merely contain the
    // string (e.g. `vim wirefold.go`, gopls indexing the repo).
    if (!command.includes("wirefold")) continue;
    if (!command.includes(binPath) && !command.includes("-topology")) continue;
    try {
      process.kill(pid, "SIGKILL");
      killed++;
    } catch {
      // pid already exited or no permission — skip; reaping is best-effort.
    }
  }
  return { killed };
}

export function buildBinary(repoRoot: string, binPath: string): BuildResult {
  if (building) return { ok: true, busy: true };
  building = true;
  try {
    try {
      fs.mkdirSync(path.dirname(binPath), { recursive: true });
    } catch (e) {
      return { ok: false, error: (e as Error).message };
    }
    const res = cp.spawnSync("go", ["build", "-o", binPath, "."], { cwd: repoRoot, encoding: "utf8" });
    if (res.error) return { ok: false, error: res.error.message };
    if (res.status !== 0) return { ok: false, error: res.stderr || `go build exited ${res.status}` };
    return { ok: true };
  } finally {
    building = false;
  }
}
