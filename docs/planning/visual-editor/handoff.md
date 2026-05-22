# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-22, post-merge of task/code-self-defends-poc)

**Active branch:** none — `task/code-self-defends-poc` merged to `main` and deleted.

### What just landed on main

Merge commit: `3228ebe`

**Substrate refactor (code-self-defends):**
- `SafeWorker` deleted. `Node` interface is `Update(ctx context.Context)` in `nodes/Wiring/node.go`.
- Port fields are `*Wiring.In`, `*Wiring.Out`, `Wiring.OutMulti` (traced wrappers).
- Each node struct has `Fire func()` — no `Trace *T.Trace`, no `Id int`, no `Name string`.
- Package layout: `nodes/<lowercase>/node.go`, `package <kind>`, `type Node struct`.
- JSON spec uses `data.state` and `data.edgeSeeds` (NOT `data.initialSlots`).
- TS schema parser validates unknown kinds and empty edge labels.
- Substrate-vocabulary lint at `tools/check-substrate-vocabulary.sh`.
- `wire:"data.state"` tag on `ChainInhibitorNode`; reflection derives JSON key from field name.
- `Fire()` called before `TrySend()` in input, readgate, inhibitrightgate — per `Trace.Fire` contract.

**Animation features (from task/diagram-animation-fixes, carried on this branch):**
1. Slot trace badges: Go `Trace.Slot` → JSONL → extension host → webview `pump.ts` → `n.data.slots` → `GenericNode` badge render.
2. Play/pause toggle, stop button, auto-rerun loop in the editor UI.
3. Uniform pulse speed (px/ms), one-shot pulse, value labels on edges.
4. `midpointOffset` (renamed from `lane`) — pixel offset for dogleg midpoint.
5. Run preserves view: clicking Run no longer strips the `view` block from `topology.json`.
6. Single `topology.json` at repo root: editor and runtime read the same file.
7. Extension host forwards slot events to webview.

### Surviving node kinds (4)

Input, ReadGate, ChainInhibitor, InhibitRightGate.

### OPEN BUG — carry forward to next task branch

**Consecutive Runs decay.** First Run animates all edges. Second Run animates only a subset. Root cause: `SubstrateEdge.tsx` `lastPulseStep` ref is never cleared between Go runs; dedup guard suppresses animation when step numbers repeat.

**Fix shape (Option A — start here):** Extension host sends `runStart` message to webview before spawning Go; `pump.ts` clears per-run state (`lastPulseStep`, `data.slots`, `data.pulse`).

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
