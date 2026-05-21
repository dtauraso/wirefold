# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-21, post diagram + animation fixes session — round 2)

**Active branch:** `task/diagram-animation-fixes`. Not yet merged to main.

### What landed this session (in order)

- `eab08bc` — first pass at pre-RF palette (superseded).
- `2e310a2` — per-kind pre-RF colors restored: ChainInhibitor orange,
  ReadGate purple, InhibitRightGate pink, Input light-gray/green accent.
- `996f1c0` — edge channel-name labels hidden; pulse value labels still show.
- `6f1d898` — drag-stop snapshots all RF node positions (not just dragged).
- `092e0d0` — `.react-flow__edges` elevated above nodes via CSS z-index.
- `9478d92` — **200-LOC file-size budget removed** (CLAUDE.md, script, memory).
- `f863fab` — snake/snake-v/below routes restored with lane drag handle.
- `db917d7` — edge-lane plumbing + delegation lesson recorded in memory.
- `ad73709` — guard against writing `"view": null` to topology.json.
- `671d014` — restore view positions from 9e915f7.
- `49385fb` — per-port side+slot positioning with drag-to-move.
- `79cdf59` — spec-save preserves view; strict-delegate memory indexed.
- `dbc292e` — ChainInhibitor height bumped to 90 (later reverted).
- `49a7780` — apply `def.height` to GenericNode container.
- `0ed5933` — ChainInhibitor height settled at 60.
- `e0d1d47` — **ChainInhibitor enumerated outputs `ToNext0`/`ToNext1`**
  (replaces single fanout `ToNext`). topology.json updated; `topologies/line.json` was NOT.
- `96fa96b` — fix snake/snake-v axis swap in `pickShape`.
- `8bbcc0c` — geometric arrow sizing: short final segment → small head.

### Open bug — NEXT SESSION'S TASK

**`topologies/line.json` still uses `sourceHandle: "ToNext"`.**

The ChainInhibitor struct (commit `e0d1d47`) replaced the single `ToNext chan<- int`
with `ToNext0 chan<- int` and `ToNext1 chan<- int`. The Go loader binds channels by
field name; the old handle name `"ToNext"` finds nothing, channels stay nil,
nil-guards in the firing rule skip sends silently, and the cascade dies after
`i0` receives the seed.

Symptom — `go run .` prints only:
```
readGate: value=0 → 0
i0: received 0 (old=0)
[ok]
```
then hangs.

Fix: open `topologies/line.json`, find every ChainInhibitor edge with
`"sourceHandle": "ToNext"`, and rename each to `"ToNext0"` or `"ToNext1"`
to match the actual struct fields. Cross-reference the working-tree
`topology.json` at repo root (already correct) to confirm the i0→target
and i1→target mappings before editing.

After the fix: `go run .` should cascade all the way through the line and exit.

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

Input, ReadGate, ChainInhibitor, InhibitRightGate.

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
- Go: `go build ./...` from repo root. `go run .` runs `topologies/line.json`.

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
