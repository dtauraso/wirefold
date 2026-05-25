# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-25, task/logs-ai-readable — NOT merged to main)

**Active branch:** `task/logs-ai-readable` (branched from main). NOT merged.
**Status:** 6 commits landed on branch; ready to merge or continue.

### What landed (task/logs-ai-readable — 6 commits)

1. **post.ts envelope** — Extension-side disk-write boundary adds `{ts_ms, src, step?}` envelope to every log line written to `.probe/`. `ts_ms` is Date.now() wall-clock (cross-process comparable on one machine); `step` is substrate event ordinal, present only on substrate-derived lines. Go's `marshalEvent`/canonical form is untouched — contract fixture `trace-events.jsonl` still pins it.

2. **webview-log split into ts/ts-errors** — `webview-log.jsonl` retired; webview+ext logs now go to `ts.jsonl` (src:"ts-webview"/"ts-ext") and window/unhandled/render errors to `ts-errors.jsonl`.

3. **runCommand relay to go.jsonl/go-errors** — Go stdout substrate trace relayed to `go.jsonl` (src:"go"); Go failures to `go-errors.jsonl`. Retired filename: `phase4-pump.jsonl` → `go.jsonl`.

4. **console-diag mirror** — Webview console.log/warn/error mirrored into the ts.jsonl stream for unified AI-readable diagnosis.

5. **probe-merge.sh** — `tools/probe-merge.sh`: no-arg merges all four files sorted by ts_ms; flags `--errors`, `--step N`, `--go`, `--ts` for filtered views.

6. **Settings cleanup** — Deleted two stale allowlist entries from `.claude/settings.local.json` (the two `awk -F'"ts":'` ... `webview-log.jsonl` entries for `i1.out->readGate`, now obsolete since webview-log.jsonl is retired).

**Go left untouched on purpose.** `marshalEvent` canonical form and contract fixture `trace-events.jsonl` unchanged; the envelope is extension-side only.

**Test baseline:** 25 passed / 13 failed on BOTH main and this branch. Failures are pre-existing and unrelated (topology edge mismatches + trace-kind fixture gap). This change added zero new failures.

### Open / next

- **Merge to main + delete branch** (needs sign-off per workflow rules), OR continue adding diagnostics.
- After merge: retire `task/logs-ai-readable` locally and on remote.

Deferred from prior sessions (still valid if friction surfaces):
1. **InhibitRightGate pseudo projection** — same pattern as Input/ReadGate, has L/R params.
2. **ChainInhibitor pseudo projection** — blocked on unresolved "keep prev send current" spec.
3. **Live-verify ReadGate edit loop in VS Code** — full edit-in-canvas UX not verified live.

### Key files

- `tools/probe-merge.sh` — unified log viewer (all four .probe/ files)
- `.probe/go.jsonl` — substrate trace (src:"go")
- `.probe/go-errors.jsonl` — Go failures
- `.probe/ts.jsonl` — webview+ext logs (src:"ts-webview"/"ts-ext")
- `.probe/ts-errors.jsonl` — window/unhandled/render errors
- `memory/project_probe_log_layout.md` — memory entry for log layout
- `scripts/stop-checks.sh` — Stop hook; runs all five guard scripts
- `tools/topology-vscode/src/webview/rf/nodes/registry.ts` — NODE_DEFS (PascalCase keys)

### Substrate model contract (stable)

See [MODEL.md](../../MODEL.md#slot-phase-lifecycle).

## Dev-loop

After TS edit: `npm run build` from `tools/topology-vscode/`.
After Go change: `go build ./...` from repo root, `go test ./nodes/Wiring/...`.
After pseudo change: `go test ./tools/pseudo/...`.
To repro / inspect: clear `.probe/*.jsonl`, reload window in VS Code, Run once, inspect logs.

Check: `go test ./...`. All five guard scripts — the four boundary guards plus
`check-substrate-vocabulary` — run automatically via the Stop hook (`scripts/stop-checks.sh`).

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored
to the state you're leaving the branch in, and commit on the active branch (main
if no task is in flight). Do not rely on chat history; the next AI may be a fresh
model with no transcript. The rendered handoff must itself contain this same ALWAYS
clause so the loop is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the
structural source of truth; update the template when an invariant changes.
