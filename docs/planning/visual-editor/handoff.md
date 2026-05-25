# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-25, task/readgate-input-label)

**Active branch:** `task/readgate-input-label`. HEAD: `f369142`. Pushed to origin. In sync with origin. NOT merged to main.

**Rebase note (2026-05-25):** This branch was rebased onto main today. The old
in-branch merge commit (`159e3c3`) is gone — rebase superseded it. The stray
`topology.json` working-tree change from prior sessions was discarded during the
rebase. Working tree is clean.

### What's on this branch

1. **ReadGate guard-term rename** (commit `3050868`, formerly `ded1a35` pre-rebase):
   the canonical first guard term changed from the two-word phrase `"input value"` to
   the single word `"input"` in `tools/pseudo/readgate.go` — grammar, parser drops the
   `"value"` word on both guard and send lines, `valueTerm()` return, and guard detector.
   All 9 affected sites in `tools/pseudo/readgate_test.go` updated to match.
   `nodes/readgate/node.go` is byte-identical to main (term is a pseudo-layer label only;
   generated Go embeds identifiers, not the label). `go test ./tools/pseudo/...` and
   `go build ./...` green.

2. **Delegate-hook threshold change** (commit `db94f80`): `scripts/force-delegate-hook.py`
   `THRESHOLD` lowered to `1`. This commit also lives on main (absorbed before the rebase).

### What main absorbed (now under this branch via rebase)

`task/logs-ai-readable` work: 4-file `.probe/` logging scheme —
`go.jsonl`, `go-errors.jsonl`, `ts.jsonl`, `ts-errors.jsonl` — with a shared
`ts_ms+src+step` envelope per line, plus `tools/probe-merge.sh` for interleaved
inspection. See memory `project_probe_log_layout` for the full layout.

### Branch sweep (2026-05-24, historical one-liner)

`task/readgate-or-gate` merged to main (AND-only refactor + boundary audit + delegation consolidation); stale branches deleted.

### Open / next

- Merge `task/readgate-input-label` to main (fast-forward-able) and delete the branch, OR continue work on the branch.
- Next friction-driven work: log in session-log.md, open a fresh `task/<short-kebab>` branch.

Deferred from prior sessions (still valid if friction surfaces):
1. **InhibitRightGate pseudo projection** — same pattern as Input/ReadGate, has L/R params.
2. **ChainInhibitor pseudo projection** — blocked on unresolved "keep prev send current" spec.
3. **Live-verify ReadGate edit loop in VS Code** — full edit-in-canvas UX not verified live.

### Key files

- `tools/pseudo/readgate.go` — ReadGate pseudo package (AND-only)
- `nodes/readgate/node.go` — ReadGate Go source (written by readgate-save)
- `nodes/Wiring/validate.go` — parse-time `validateSpec`
- `scripts/stop-checks.sh` — Stop hook; runs all five guard scripts
- `tools/check-trace-kind-parity.sh`, `tools/check-no-ts-timers.sh`, `tools/check-message-kind-parity.sh`, `tools/check-slot-phase-boundary.sh` — four boundary guards
- `tools/check-substrate-vocabulary.sh` — banned-vocabulary guard (5th stop-hook check)
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
