# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-21, post diagram + animation fixes session — round 3)

**Active branch:** `task/diagram-animation-fixes`. Not yet merged to main.

### What landed this session (in order)

- `d1fb5c5` — **InhibitRightGate: remove `ToPassed` output** — node is now a terminal sink.
- `5656877` — regenerate `node-defs.ts`: drops sources/outputs for `inhibitRightGate`.
- `7603268` — `topology.json`: drop stale `outputs` block on `inhibitRight0`.
- `df5c8c2` — handoff: park spec→Go translation work on `task/pseudocode-spec`.

Prior-session commits still on this branch (for history):

- `8bbcc0c` — geometric arrow sizing: short final segment → small head.
- `96fa96b` — fix snake/snake-v axis swap in `pickShape`.
- `e0d1d47` — **ChainInhibitor enumerated outputs `ToNext0`/`ToNext1`** (replaces single `ToNext`). `topology.json` updated.
- `0ed5933` — ChainInhibitor height settled at 60.
- (earlier commits on branch: see prior handoff entries)

### Already-wired plumbing (don't re-grep!)

- **Edge lane:** `lane?: number` in `WireProps`/`WIRE_PROPS`; `setEdgeLane(edgeId, lane)`
  in `_use-edge-handlers.ts`; `EdgeActionsCtx` provided by `app.tsx`.
  Details: `memory/project_edge_lane_plumbing.md`.
- **Port positioning:** `setPortPosition(nodeId, portName, side, slot)` exposed via
  `EdgeActionsCtx`. Per-port `side`/`slot` auto-flows through `spec-to-flow.ts`
  via `n.inputs ?? def?.inputs ?? []`.
- **View save merge:** spec-save goes through `injectViewText` in `handle-message.ts`
  — view is preserved across spec saves.
- **Arrow sizing:** `MarkerDefs.tsx` renders sm/md filled/open markers per EdgeKind;
  `SubstrateEdge.tsx` uses `finalSegmentLength()` to pick size geometrically.

### Strict-delegate rule

`memory/feedback_delegate_all_writing.md` (indexed in MEMORY.md). Main Opus session
may not call Edit/Write/Bash for file writes; all writes go to subagents.

### Surviving kinds (4)

Input, ReadGate, ChainInhibitor, InhibitRightGate (now terminal sink — no outputs).

## Parked

- **Spec → Go translation (branch `task/pseudocode-spec`):** pseudocode renderings of all 4 kinds plus `docs/pseudocode-spec.md` describing the proto-DSL conventions. Goal: spec text becomes the human-editable source of truth inside each node; a constrained DSL maps spec ↔ Go. Resume by reading `docs/pseudocode-spec.md` on that branch (open questions section lists what to pin next — types, named heterogeneous outputs, send semantics, params-vs-state initializers, terminating loops, trace omission).

## Architecture summary

See MODEL.md for substrate model (inhibitor chain, edge nodes, partition nodes,
AND-gate tree, lateral inhibition, slot-in-node backpressure, round-close stepping).

- **Editor (TS / React Flow):** `GenericNode.tsx` reads `node-defs.ts` and renders
  all kinds. `SubstrateEdge.tsx` — single home for path routing, lane drag,
  arrow sizing, RF integration. TS is inert re: simulation.
- **Runtime (Go):** `go generate ./...` writes `kinds_generated.go`. Each kind's
  `init()` registers with `Wiring`. `Wiring.LoadTopology` parses `topology.json`
  (including `"view"` key); reflection on registered structs builds port manifest;
  non-channel fields populated via `wire:` struct tags.
- **Trace pipeline:** Go emits JSONL on stdout → extension host reads lines →
  `trace-event` postMessage → webview `pump.ts` → `lastFire`/`pulse` on RF store
  → `GenericNode` flashes / `SubstrateEdge` animates.

## Adding a kind (2 files)

1. `nodes/<Kind>/<Kind>.go` — struct + firing rule + `init()`.
2. `nodes/<Kind>/SPEC.md` — optional; non-default view metadata.

`go generate ./...` picks up new kinds automatically.

## Dev-loop

- TS: `npm run build` from `tools/topology-vscode/` (tsc alone doesn't refresh
  `out/webview.js`). Reload Window after extension-host changes.
- Go: `go build ./...` from repo root. `go run .` runs `topology.json` (repo root).

## Working-tree state

`topology.json` at repo root may show extra modifications from drag-tests.
Restore or commit at user discretion before merging.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored
to the state you're leaving the branch in, and commit on the active branch (main
if no task is in flight). Do not rely on chat history; the next AI may be a fresh
model with no transcript. The rendered handoff must itself contain this same ALWAYS
clause so the loop is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the
structural source of truth; update the template when an invariant changes.
