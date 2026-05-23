# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-23, task/pseudo-projection-input)

**Active branch:** `task/pseudo-projection-input`. Pseudo-projection v0
for Input node is end-to-end functional; not yet merged to main.

### What landed on this branch

**Design doc** `docs/planning/visual-editor/pseudo-projection.md` —
algebraic per-instance projection, no AI, deterministic round-trip,
Input-only v0. (Branch-local; stripped before merge.)

**`tools/pseudo/` Go package** — `FromInput` / `RenderInput` /
`ParseInput` / `ToInput` + `ParseInputError` with human-readable
messages + `Suggestion()`. Five round-trip tests passing.

**`cmd/pseudo/` CLI** — `input render` and `input save` subcommands;
stderr JSON `{error, suggestion}` on parse failure (exit 2); stdout
JSON `{go, spec}` on save success.

**Extension host bridge** (`tools/topology-vscode/src/extension/handle-message.ts`)
— `pseudo-render` and `pseudo-save` handlers; shells out to
`go run ./cmd/pseudo`; reads `nodes/Input/node.go` via workspace fs;
patches the node's `data` field in topology document via `applyEdit` +
`document.save()`; auto stop+run if substrate is active.

**Webview UI:**
- `PseudoPanel.tsx` — inline pseudo on Input nodes (no ƒ toggle);
  double-click label → contentEditable edit mode; autosave on debounced
  parse (250 ms); blur flushes pending edits; styled to match node text.
- `PseudoErrorOverlay.tsx` — lower-left canvas overlay surfaces parse
  errors + fix suggestion, auto-dismiss 10 s.
- `GenericNode.tsx` — renders `PseudoPanel` when `data.type === "Input"`;
  uses `nodrag nopan` + stopPropagation so caret placement works.
- Removed init-list / repeat displays from Input node defs (pseudo line
  covers them).

### What works

Full edit loop verified by user: double-click pseudo → type → debounce
parse → topology.json + node.go written → substrate auto-restart →
animation reflects new behavior. Parse errors appear in lower-left
overlay with human wording. Caret placement, blur flush, and round-trip
on disk all verified.

### Open / next

1. **Merge to main** — run `tools/strip-branch-local-docs.sh task/pseudo-projection-input`
   to remove the branch-local design doc, then merge with explicit user
   sign-off.
2. **ReadGate pseudo projection** — follow-up branch using the same
   pattern; smaller scope.
3. **InhibitRightGate pseudo projection** — same pattern.
4. **ChainInhibitor pseudo projection** — primitive still unresolved
   ("keep prev send current"); needs spec before starting.

### Key files

- `tools/pseudo/input.go` — Go pseudo package (FromInput/RenderInput/ParseInput/ToInput)
- `cmd/pseudo/main.go` — CLI entry point
- `tools/topology-vscode/src/webview/rf/panels/PseudoPanel.tsx` — inline editor UI
- `tools/topology-vscode/src/webview/rf/panels/PseudoErrorOverlay.tsx` — error overlay
- `tools/topology-vscode/src/extension/handle-message.ts` — pseudo-render / pseudo-save handlers
- `nodes/Input/node.go` — Input node Go source (written by `pseudo-save`)
- `docs/planning/visual-editor/pseudo-projection.md` — design doc (branch-local; strip before merge)

### Substrate model contract (stable)

See [MODEL.md](../../MODEL.md#slot-phase-lifecycle).

## Dev-loop

After TS edit: `npm run build` from `tools/topology-vscode/`.
After Go change: `go build ./...` from repo root, `go test ./nodes/Wiring/...`.
After pseudo change: `go test ./tools/pseudo/...`.
To repro / inspect: clear `.probe/*.jsonl`, reload window in VS Code, Run once, inspect logs.

Check: `go test ./...`, `bash tools/check-substrate-vocabulary.sh`.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored
to the state you're leaving the branch in, and commit on the active branch (main
if no task is in flight). Do not rely on chat history; the next AI may be a fresh
model with no transcript. The rendered handoff must itself contain this same ALWAYS
clause so the loop is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the
structural source of truth; update the template when an invariant changes.
