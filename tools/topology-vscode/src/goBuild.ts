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
