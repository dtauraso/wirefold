# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

handoff.md is exempt from the 100-LOC budget.

---

## State at handoff (2026-05-21, post tidy + replay-abandoned session)

**Active branch:** main at `e4e5f99`. No task in flight.

### What landed this session

Six small refactor/cleanup commits, all on main:

- `bc2aeaf` — memory cleanup (deleted stale substrate-r RAF entry;
  refreshed edge-seed index line).
- `93e46ef` — dropped 4 unused topology meta fields (`timing`,
  `cycleAnchor`, `runtime`, `legend`). Only `notes` survives.
- `34eba3e` — removed write-only `lastRecv` field from pump and
  `NodeData`.
- `e589858` — removed double-click rename UI (no replacement queued).
- `7ec3e34` — removed inert `TransportControls` play button +
  `TimelinePanel` wrapper + its mount in `app.tsx`.
- `e4e5f99` — handoff updated; parked list emptied.

### Key conceptual finding

TS-execution-drift audit (branch `task/ts-nodes-inert`, since deleted)
returned clean by direct inspection: simulation logic has **only ever
lived in Go**. The deleted `<Kind>Node.tsx` files (commit `9feed85`)
were visual-only; per-kind firing rules were never in TS. The current
per-kind TS surface is narrow:
- `schema/node-data-types.ts` — generated per-kind data validators
  (mirror of Go `wire:` struct tags).
- `node-defs.ts` — one hand-authored `defaultData: { init: [0,1] }`
  for Input (editor convenience, not simulation).

The substrate boundary holds: TS = editor + viewer + readout; Go =
execution; `pump.ts` is the one bridge.

### Live regression observed (2026-05-21)

`topology.view.json` reappeared at repo root and `topology.json`
showed modifications during this session — exactly the regression
documented in the prior handoff as "investigated and gone." It is
**not gone**. The static save-path audit (`injectViewText` /
`extractViewText` merge logic looks correct) misses the actual
writer. Working tree at session end has:
- modified `topology.json`
- untracked `topology.view.json`
Neither committed. Next session should reproduce and find the writer
empirically — instrument the save path or watch the filesystem during
a save round-trip. The static read of the source code is insufficient.

### Related symptom

`topologies/line.json` loads with all nodes stacked at `{0,0}`. Root
cause is the same family: that file has no `view` key, so
`spec-to-flow.ts:107` falls through to the default position for every
node. Either restore positions from git history, lay them out by hand
and save, or fix the regression so saves don't strip the `view` key.

### Surviving kinds (4)

Input, ReadGate, ChainInhibitor, InhibitRightGate.

### Tests

`go test ./...` green. `npm run build` green at `e4e5f99`.

## Architecture summary

- **Editor (TS / React Flow):** one `GenericNode.tsx` reads
  `node-defs.ts` (generated from Go AST) and renders all kinds.
  `SubstrateEdge` for wires. TS is **inert** with respect to
  simulation — no firing rules, slot-phase, or backpressure logic.
- **Runtime (Go):** `go generate ./...` writes `kinds_generated.go` at
  repo root. Each kind's `init()` calls `Wiring.Register`.
  `Wiring.LoadTopology` parses `topology.json` (including `"view"`
  key) and uses reflection on each registered struct to build the
  port manifest. Non-channel fields populated via `wire:` struct
  tags.
- **Trace pipeline:** Go emits JSONL trace events (`fire` / `send`;
  `recv` is no-op in the pump) on stdout. Extension host
  (`runCommand.ts`) reads lines, forwards as `trace-event`
  postMessage. Webview routes to `pump.ts`, which writes `lastFire`
  (nodes) or `pulse` (edges). `GenericNode` flashes; `SubstrateEdge`
  animates pulse along the path.

## Adding a kind (2 files)

1. `nodes/<Kind>/<Kind>.go` — struct + firing rule + `init() {
   Wiring.Register("Kind", func() any { return &Struct{} }) }`.
   Non-channel fields read from `data.*` JSON use struct tags:
   - `wire:"data.<key>"` — copies `NodeData.<Key>`
   - `wire:"data.initialSlots.<key>"` — reads
     `NodeData.InitialSlots[key]` (int)
   `go generate ./...` picks it up automatically.
2. `nodes/<Kind>/SPEC.md` — **optional**; only for non-default view
   metadata (accent color, display name override). Port names and
   data types are derived from the Go struct by
   `tools/gen-node-defs`.

## Next options

1. **Reproduce + fix the `topology.view.json` regression** — highest
   priority because it's actively breaking saved layouts (stacked
   nodes in `line.json`). Static audit didn't find the writer;
   instrument the save path or use `fs.watch` to catch it
   empirically.
2. **Optional guardrail** — lint/test that fails if any file outside
   `pump.ts` writes `lastFire` or `pulse`, pinning the substrate
   boundary mechanically. Speculative; only if drift appears.
3. **Start the next thing on user prompt** — no pre-committed
   direction.

## Parked follow-ups

None.

## Working-tree state

- `topology.json` modified, `topology.view.json` untracked at repo
  root — regression artifacts; do not commit. See "Live regression
  observed" above.

## Dev-loop

After any TS edit: `npm run build` from `tools/topology-vscode/` (tsc
alone doesn't refresh `out/webview.js`). After extension-host
changes: Reload Window in VS Code (Cmd+R). Go: `go build ./...` from
repo root; `go run .` runs `topologies/line.json` (default
`--topology`).

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered
prompt tailored to the state you're leaving the branch in, and
commit on the active branch (main if no task is in flight). Do not
rely on chat history; the next AI may be a fresh model with no
transcript. The rendered handoff must itself contain this same
ALWAYS clause so the loop is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md)
as the structural source of truth; update the template when an
invariant changes.
