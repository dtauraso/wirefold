# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-25, main — no task in flight)

**Active branch:** `main`. HEAD: `b85f977`. Pushed to origin.
**No task in flight.** Repo is now MAIN-ONLY — every task branch deleted (local + remote).

**Stray working-tree change:** `topology.json` has a 1-line uncommitted
modification that predates the last session. Deliberately left untouched.
Decide whether to keep or `git checkout topology.json` before starting new work.

### Branch sweep (2026-05-24)

**Merged to main then deleted:**
- `task/readgate-or-gate` — ReadGate AND-only refactor + boundary audit + delegation consolidation.

**Deleted as redundant (0 commits ahead of main):**
- `task/boundary-audit`
- `task/diagram-animation-fixes`
- `task/spec-phase2-generic-renderer`

**Force-deleted as DISCARDED unmerged work (gone everywhere, redo fresh if wanted):**
- `task/pulses-as-events` — complete WAAPI/DOM-pulse + pause-resume rewrite; conflicted structurally with main's later pump rewrite — NOT merged, behavior no longer exists in repo.
- `task/visual-paced-substrate` — design + plan docs only, discarded.
- `task/pseudocode-spec` — pseudocode spec docs for Input/ReadGate/ChainInhibitor/InhibitRightGate, discarded.

The `task/pulses-as-events` and `task/pseudocode-spec` design thinking is gone, not just unmerged — next session shouldn't go looking for those branches.

### What landed (task/readgate-or-gate → main, merged 2026-05-24)

**ReadGate is AND-only.** Removed the entire OR-gate pathway from
`tools/pseudo/readgate.go`: the `ReadGateView.Gate` field, `gateWord()` "or" branch,
the `||` operator mapping and `IsOr` template branch in `ToReadGate`, and `||`
recognition in the guard detector. `parseReadGatePseudo` now REJECTS `"or"` with an
"AND-only" parse error; `"and"` still parses. Generated `nodes/readgate/node.go` is
byte-identical to before (AND output unchanged). OR-specific tests removed; a
parser-rejects-"or" test added.

**Boundary self-defense audit.** Converted hand-maintained cross-boundary invariants
into free, deterministic checks (no AI tokens needed to re-verify):
- `tools/check-trace-kind-parity.sh` — pump.ts trace switch vs generated `TRACE_EVENT_KINDS`.
- `tools/check-no-ts-timers.sh` — no setInterval/setTimeout/while in pump.ts (enforces MODEL.md "no firing logic in TS").
- `tools/check-message-kind-parity.sh` — Go `"delivered"` discriminator (stdin_reader.go) ⊆ TS `WEBVIEW_TO_HOST_TYPES` (src/messages.ts).
- `tools/check-slot-phase-boundary.sh` — slot-phase literals only in paced_wire.go / pump.ts.
- All four wired into the Stop hook via `scripts/stop-checks.sh` (block-on-fail).

### What landed (2026-05-25)

**Vocab check wired into Stop hook.** `tools/check-substrate-vocabulary.sh` previously
ran advisory-only (manual). Commit `b85f977` added it to the `for chk in ...` loop in
`scripts/stop-checks.sh:58`, making it the 5th guard to block the Stop hook fail-closed.
The four boundary guards + `check-substrate-vocabulary` now all run automatically on
every Stop. Passes clean against current substrate.

**Parse-time validation.** `nodes/Wiring/validate.go` — `validateSpec` runs at parse
time (after JSON unmarshal, before substrate build); aggregates spec-shape errors
previously runtime-only in loader.go: unknown kind, empty edge label, bad
source/target handle, missing required input, missing `data.state` keys.

**Delegation-guidance consolidation.** MODEL.md rotting pump.ts line-number refs
replaced with anchor comments (`PUMP_DONE_HANDLER`, `PUMP_SLOT_HANDLER`).

### Open / next

No active task. Next work should be friction-driven (log friction in session-log.md,
open a fresh `task/<short-kebab>` branch).

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
