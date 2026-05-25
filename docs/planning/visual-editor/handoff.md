# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-25, task/chaininhibitor-pseudo)

**Active branch:** `task/chaininhibitor-pseudo`. Freshly branched from main, no work commits yet. NOT merged to main.

### What just merged to main (task/readgate-input-label, fast-forward, branch deleted)

1. **ReadGate guard-term rename:** the canonical first guard term changed from `"input value"` to `"input"` in `tools/pseudo/readgate.go` — grammar, parser, `valueTerm()` return, and guard detector. All affected sites in `readgate_test.go` updated.

2. **ReadGate pseudo-save edge re-sync:** pseudo-save now re-points the canvas edge AND posts a load so the edge re-sync is visible; single-undo preserved.

3. **validate.go Check 4 demoted to non-fatal:** a node with an unfed required port now loads and stays inert via precondition-gating instead of aborting the whole load. Memory `feedback_enforce_required_inputs` reversed to match (substrate lenient at load; editor flags instead of rejecting).

4. **Editor dead-node legibility:** `parseSpec` is tolerant (no throw on missing required wire); `requiredInputDiagnostics` computes a per-node flag inside `specToFlow` (survives rebuilds); dead-node status propagates along input edges to a fixpoint (a node fed only by dead nodes is also flagged). Flagged nodes render a 3px red border + light-red (`#ffe5e5`) background with a hover tooltip. Newly-dropped palette nodes with required inputs also flag.

5. **Run model — SNAPSHOT + Tier-1 auto-restart:** a run reflects the graph as of Run time. Undo/redo + external `topology.json` edits now restart the running process (debounced 300ms in `extension.ts` `onDidChangeTextDocument`), consistent with pseudo-save's existing auto-restart. Live incremental update (Tier 2) explicitly rejected as not worth the cost.

### Open / next (the task this branch is for)

- **Goal:** pseudo-text projection for the ChainInhibitor node (the `i0`/`i1` node kind), following the same pattern as Input and ReadGate: a `cmd/pseudo` subcommand (render/save) + PseudoPanel double-click-to-edit + Go template regeneration of `nodes/chaininhibitor/node.go`.
- **Prior blocker to resolve with David first:** the ChainInhibitor pseudo was previously deferred as "blocked on unresolved 'keep prev send current' spec" — clarify the intended pseudo grammar/semantics for ChainInhibitor (it holds a value, waits for a chain signal, fires forward) before implementing. **Do not start coding the projection until the grammar is agreed.**
- **Pattern reference:** `tools/pseudo/readgate.go` (FromReadGate/RenderReadGate/ParseReadGate/ToReadGate), `cmd/pseudo/main.go` (subcommand dispatch), `handle-message.ts` (handleReadgateSave + pseudoTable), `PseudoPanel.tsx`.

### Deferred (still valid)

- **InhibitRightGate pseudo projection** — same pattern as Input/ReadGate, has L/R params; "L pass / R inhibit" semantic: result = Left==1 && Right==0.
- **Known non-issue (do NOT treat as a bug):** undo/redo + pause/unpause on flipping valid/invalid nodes cannot revive a deadlocked Go process — only re-Run rebuilds. This is the intended snapshot model; the Tier-1 auto-restart now makes a fixed graph resume automatically.
- **Latent:** `i1`-style OUTPUT backpressure deadness is NOT flagged (the flag follows input edges forward only; backpressure travels backward along outputs). Add a backward rule only if it surfaces as real friction.

### Key files

- `tools/pseudo/readgate.go` — ReadGate pseudo package (pattern reference)
- `nodes/chaininhibitor/node.go` — ChainInhibitor Go source (target to regenerate)
- `cmd/pseudo/main.go` — pseudo subcommand dispatch
- `tools/topology-vscode/src/webview/rf/PseudoPanel.tsx` — double-click-to-edit panel
- `tools/topology-vscode/src/handle-message.ts` — handleReadgateSave + pseudoTable
- `tools/topology-vscode/src/schema/parse-spec.ts` — requiredInputDiagnostics fixpoint
- `tools/topology-vscode/src/extension.ts` — debounced restart listener
- `nodes/Wiring/validate.go` — parse-time validateSpec (Check 4 now non-fatal)
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
