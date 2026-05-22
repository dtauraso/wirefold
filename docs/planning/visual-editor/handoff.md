# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-22, post-merge of task/go-ts-leverage)

**Active branch:** `task/required-input-parser-check` (just created from main; no commits yet beyond the merge).

### What just landed on main

Merge commit: `task/go-ts-leverage` → main

**Doc fixes:**
- `canAccept` removed from MODEL.md (was stale vocabulary).
- `pump.ts` clarified as render-only in MODEL.md.
- PascalCase precondition added to CLAUDE.md substrate primitive landing rule.

**Kind registry derivation:**
- `NODE_TYPES` keys in `node-types.ts` are now derived from `RUNTIME_IMPLEMENTED_KINDS` rather than maintained as a separate parallel list.

**Parser guards (commit `b6ff9b3`):**
- TS parser rejects unknown `node.type` values at parse time with a clear error.
- Empty edge label check was already present; kind check is new.

### Surviving node kinds (4)

Input, ReadGate, ChainInhibitor, InhibitRightGate.

### Next task: `task/required-input-parser-check`

Deferred half of audit Finding #2. Goal: generator emits per-kind `requiredInputs`; TS parser rejects topologies missing inbound edges for required inputs. ~40–60 LOC.

**Decide first:** what counts as "required"? Options:
- All `*Wiring.In` fields on a kind are required (matches Go loader behavior).
- `edgeSeeds` on a node satisfies the requirement for its port (ring topologies use this to break startup deadlock — see `feedback_edge_seed_required_for_rings.md`).

Resolve semantics before writing any code; the answer shapes both the generator output and the parser check.

### OPEN BUG — carry forward to next task branch

**Consecutive Runs decay.** First Run animates all edges. Second Run animates only a subset. Root cause: `SubstrateEdge.tsx` `lastPulseStep` ref is never cleared between Go runs; dedup guard suppresses animation when step numbers repeat.

**Fix shape (Option A — start here):** Extension host sends `runStart` message to webview before spawning Go; `pump.ts` clears per-run state (`lastPulseStep`, `data.slots`, `data.pulse`).

## Audit skill — `/audit-grep-load`

Saved at `.claude/skills/audit-grep-load/SKILL.md` (commit `f650cc9`). Runs a four-category audit for verifying-grep hotspots: string/key duplication across files, doc claims about code that could drift, runtime-only validations that could move to a parser, files claiming to be generated that aren't. Surfaces ranked findings; user picks; AI fixes. Invoke after any substantial refactor or periodically to surface code-self-defends opportunities without the user having to drive discovery.

## Dev-loop

After any TS edit: `npm run build` from `tools/topology-vscode/` (tsc alone doesn't refresh `out/webview.js`). After extension-host changes: Reload Window in VS Code.

Go: `go build ./...` from repo root. `go run .` loads `topology.json` at repo root.

Check: `go test ./...`, `npm run check:loc`, `bash tools/check-substrate-vocabulary.sh`.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored
to the state you're leaving the branch in, and commit on the active branch (main
if no task is in flight). Do not rely on chat history; the next AI may be a fresh
model with no transcript. The rendered handoff must itself contain this same ALWAYS
clause so the loop is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the
structural source of truth; update the template when an invariant changes.
