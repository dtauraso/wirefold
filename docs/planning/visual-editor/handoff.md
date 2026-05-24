# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-23, task/pseudo-projection-readgate)

**Active branch:** `task/pseudo-projection-readgate`. ReadGate pseudo-projection
v0 is functionally complete end-to-end; NOT yet merged to main. Built by
mirroring the Input pseudo pattern.

### What landed this session

**`tools/pseudo/readgate.go`** — `FromReadGate` / `RenderReadGate` /
`ParseReadGate` / `ToReadGate` + `ParseReadGateError` with `Suggestion()`.
`ToReadGate` signature: `(newGoSrc []byte, newOutNeighbor string, removedPorts []string, err error)`.
Dropping a guard term regenerates `node.go` WITHOUT that port (no dead ports)
and reports it in `removedPorts`. Tests in `readgate_test.go` green.

**`cmd/pseudo/main.go`** — `readgate render|save` subcommands (require
`--go-file`, `--out-neighbor`; save also `--pseudo`). Save stdout JSON:
`{go, outNeighbor, removedPorts}`.

**`tools/topology-vscode/src/extension/handle-message.ts`** —
`readgate-render` / `readgate-save` handlers. Save applies `node.go` rewrite
+ topology mutations (re-point ToChainInhibitor output edge to new neighbor;
prune edges feeding `removedPorts`) as ONE `vscode.WorkspaceEdit` → single
Cmd-Z undoes everything. Resolves out-neighbor via `findOutNeighbor`
(`source === nodeId && sourceHandle === "ToChainInhibitor"`).

**`src/messages.ts`** — `readgate-render` / `readgate-save` + result/error
message types.

**Webview:** `PseudoPanel.tsx` gained a `kind: "Input" | "ReadGate"` prop
selecting which message types it sends/receives. `GenericNode.tsx` renders
the panel for both Input and ReadGate nodes. PseudoPanel text now word-wraps
at word boundaries (`whiteSpace: pre-wrap` + `wordBreak: keep-all` +
`overflowWrap: normal`), no mid-word splits.

**Input pseudo also changed this session** (`input.go` + `cmd/pseudo` +
`handle-message.ts`): Input now names its downstream neighbor node id as the
target (was generic "OutputField"). `FromInput` signature gained `outNeighbor`
param; input render/save now REQUIRE `--out-neighbor` and the extension
resolves it via `findInputOutNeighbor` (first edge with `source === nodeId`).
Text evolved to: `↺ ([0, 1] -> readGate1)` with repeat, `[0, 1] -> readGate1`
without — dropped "send"/"each of", repeat shown as leading `↺` with the
body parenthesized.

### Current rendered text

- **ReadGate:** `if input value and signal` / (3-space indent) `input value -> i0`
- **Input:** `↺ ([0, 1] -> readGate1)` (repeat) or `[0, 1] -> readGate1`

### ReadGate editable surface

1. Drop `signal` from guard → fires on value alone; removes `FromChainInhibitor`
   port + prunes its wires.
2. Change target node after `->` → re-points output edge.

Both save as one undoable `WorkspaceEdit`. The literal vocabulary
(`input value` / `signal` / `if` / `->`) is fixed, not renamable.

### Status

Go tests + `tsc --noEmit` + `npm run build` all green. The edit-in-canvas
loop has **NOT** been verified live in VS Code this session (UI/integration).

### Open / next

1. **Live-verify in VS Code** — reload window, double-click a ReadGate pseudo,
   edit guard (drop signal) and target, confirm save writes `node.go` + prunes
   wires + re-points edge, and one Cmd-Z reverts all; same for Input.
2. **Merge to main** — run `tools/strip-branch-local-docs.sh task/pseudo-projection-readgate`
   (check for any branch-local docs first), merge with explicit user sign-off,
   delete branch local+remote.
3. **InhibitRightGate pseudo projection** — same pattern, has L/R params.
4. **ChainInhibitor pseudo projection** — still blocked on unresolved
   "keep prev send current" spec.

### Key files

- `tools/pseudo/readgate.go` — Go pseudo package (FromReadGate/RenderReadGate/ParseReadGate/ToReadGate)
- `tools/pseudo/input.go` — Input pseudo package (updated this session)
- `cmd/pseudo/main.go` — CLI entry point (readgate + input subcommands)
- `tools/topology-vscode/src/extension/handle-message.ts` — readgate-render / readgate-save / input handlers
- `tools/topology-vscode/src/webview/rf/panels/PseudoPanel.tsx` — inline editor UI (kind prop)
- `tools/topology-vscode/src/webview/rf/nodes/GenericNode.tsx` — renders PseudoPanel for Input + ReadGate
- `nodes/readgate/node.go` — ReadGate node Go source (written by readgate-save)
- `nodes/Input/node.go` — Input node Go source (written by input-save)

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
