# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-25, task/inhibitright-pseudo)

**Active branch:** `task/inhibitright-pseudo`. Freshly branched from main after chaininhibitor merge; no work commits yet. NOT merged to main.

### What just merged to main (task/chaininhibitor-pseudo)

**ChainInhibitor pseudo-text projection landed**, mirroring the ReadGate pattern.

Grammar — per-neighbor lines:
```
send held -> <neighbor>
send held -> <neighbor2>
keep input
```

Semantics: "send prev, hold current" — on input v, emit the previously-held value to all ToNext targets, then set held = v.

Key changes:
- `tools/pseudo/chaininhibitor.go` + `chaininhibitor_test.go` — new pseudo package
- `cmd/pseudo/main.go` — chaininhibitor dispatch with `--out-neighbors` (plural)
- `nodes/chaininhibitor/SPEC.md` — `hasPseudo: true`
- `tools/topology-vscode/src/handle-message.ts` — `handleChainInhibitorRender` + `handleChainInhibitorSave`; suffix-stripped OutMulti handle matching (ToNext0/ToNext1 → base ToNext)
- `tools/topology-vscode/src/webview/rf/PseudoPanel.tsx` — clears "loading" on `m.error`; renders flush-left (`textAlign: left`)
- **Save semantics:** ChainInhibitor save regenerates only `node.go`; topology owns the broadcast edge set (unlike ReadGate which re-points its single out edge)

### Open / next (the task this branch is for)

**Goal:** InhibitRightGate pseudo-text projection — same Input/ReadGate/ChainInhibitor pattern.

- **Params:** L (left) and R (right) inputs
- **Semantic:** "L pass / R inhibit" — result = Left==1 && Right==0
- **Steps:** `cmd/pseudo` subcommand (render/save) + `nodes/inhibitrightgate/SPEC.md` hasPseudo:true + `handle-message.ts` handler + Go template regen of `node.go`
- **Watch:** apply the OutMulti handle-matching lesson from ChainInhibitor if InhibitRightGate has multiple outputs (suffix-strip to base port name)
- **Pattern reference:** `tools/pseudo/chaininhibitor.go`, `tools/pseudo/readgate.go`, `cmd/pseudo/main.go`, `handle-message.ts` handleChainInhibitor{Render,Save}, `PseudoPanel.tsx`

### Deferred (still valid)

- **`i1`-style OUTPUT backpressure deadness flag (backward rule)** — add only if real friction surfaces. Current flag follows input edges forward only; backpressure travels backward along outputs.
- **Latent UX:** pseudo panel error display is minimal (clears loading, shows empty) — improve only if needed.

### Key files

- `tools/pseudo/chaininhibitor.go` — ChainInhibitor pseudo package (pattern reference)
- `tools/pseudo/readgate.go` — ReadGate pseudo package (pattern reference)
- `nodes/inhibitrightgate/node.go` — InhibitRightGate Go source (target to regenerate)
- `nodes/inhibitrightgate/SPEC.md` — spec (needs `hasPseudo: true`)
- `cmd/pseudo/main.go` — pseudo subcommand dispatch
- `tools/topology-vscode/src/webview/rf/PseudoPanel.tsx` — double-click-to-edit panel
- `tools/topology-vscode/src/handle-message.ts` — handleChainInhibitor{Render,Save} + pseudoTable
- `tools/topology-vscode/src/schema/parse-spec.ts` — requiredInputDiagnostics fixpoint
- `tools/topology-vscode/src/extension.ts` — debounced restart listener
- `nodes/Wiring/validate.go` — parse-time validateSpec (Check 4 non-fatal)
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
