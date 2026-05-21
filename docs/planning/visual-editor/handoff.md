# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-21, post diagram + animation fixes session)

**Active branch:** `task/diagram-animation-fixes`. Not yet merged to main.

### What landed this session (in order)

- `eab08bc` — first pass at pre-RF Input styling; uniformly white/gray
  on all kinds (collapsed too far — superseded next commit).
- `2e310a2` — per-kind pre-RF palette restored: ChainInhibitor orange,
  ReadGate purple, InhibitRightGate pink, Input light-gray with green
  accent. `GenericNode` container now uses `def.bg/border/text`;
  Input's SPEC.md updated to pre-RF values.
- `996f1c0` — edge channel-name labels hidden (`data.label`); pulse
  value labels (`data.valueLabel`) still appear during animation.
- `6f1d898` — drag-stop now snapshots **all** RF node positions into
  `viewerState.nodes`, not just the dragged one. Fixed the
  partial-view bug that caused stacked nodes on reload after a topology
  loaded with empty/partial view.
- (next commit) — `.react-flow__edges` elevated above nodes via CSS
  (was visually under node boxes when crossing).
- (next commit) — pre-RF "snake edge" feature restored. Single
  `lane?: number` per edge; routes auto-picked (`snake` H-V-H,
  `snake-v` V-H-V, `below` corridor, `line` bezier fallback); midpoint
  drag handle (ew-resize / ns-resize). All path + drag logic lives
  in `SubstrateEdge.tsx` split into named functions.
- (next commit) — **200-LOC file-size budget removed.** CLAUDE.md
  section deleted, `npm run check:loc` script + `scripts/check-loc.mjs`
  deleted, `feedback_file_size_budget.md` deleted, settings.json
  allowlist entries removed.

### Already-wired plumbing (don't re-grep!)

Recorded in `memory/project_edge_lane_plumbing.md`. Summary:

- `lane?: number` is in `WireProps` / `WIRE_PROPS`; `pickWireProps` in
  `spec-to-flow.ts` auto-threads it into RF edge `data`.
- `setEdgeLane(edgeId, lane)` lives in `_use-edge-handlers.ts` and
  calls `scheduleSave()`.
- `EdgeActionsCtx` is provided by `app.tsx`; `useEdgeActions()` is
  the consumer hook.

If you're extending edges with a new scalar like `lane`, copy this
pattern; assume the schema/adapter/mutation pipeline already
auto-threads anything in `WIRE_PROPS`.

### Surviving kinds (4)

Input, ReadGate, ChainInhibitor, InhibitRightGate.

### Tests

`go test ./...` green at last check. `npm run build` green on every
commit above.

## Architecture summary

- **Editor (TS / React Flow):** one `GenericNode.tsx` reads
  `node-defs.ts` (generated from Go AST) and renders all kinds.
  `SubstrateEdge.tsx` for wires — now contains pickShape +
  snake/snake-v/below/line path builders + LaneDragHandle. TS is
  **inert** with respect to simulation — no firing rules, slot-phase,
  or backpressure logic.
- **Runtime (Go):** `go generate ./...` writes `kinds_generated.go` at
  repo root. Each kind's `init()` calls `Wiring.Register`.
  `Wiring.LoadTopology` parses `topology.json` (including `"view"`
  key) and uses reflection on each registered struct to build the
  port manifest. Non-channel fields populated via `wire:` struct
  tags.
- **Trace pipeline:** Go emits JSONL trace events on stdout.
  Extension host (`runCommand.ts`) reads lines, forwards as
  `trace-event` postMessage. Webview routes to `pump.ts`, which writes
  `lastFire` (nodes) or `pulse` (edges). `GenericNode` flashes;
  `SubstrateEdge` animates pulse along the path.

## Adding a kind (2 files)

1. `nodes/<Kind>/<Kind>.go` — struct + firing rule + `init() {
   Wiring.Register("Kind", func() any { return &Struct{} }) }`.
   Non-channel fields read from `data.*` JSON use struct tags:
   - `wire:"data.<key>"` — copies `NodeData.<Key>`
   - `wire:"data.initialSlots.<key>"` — reads
     `NodeData.InitialSlots[key]` (int)
   `go generate ./...` picks it up automatically.
2. `nodes/<Kind>/SPEC.md` — optional; only for non-default view
   metadata (accent color, display name override, bg/border/text).

## Next options

1. **Merge `task/diagram-animation-fixes` to main** once the snake-edge
   drag is verified end-to-end (drag a midpoint, save, reload, confirm
   lane persists). Requires sign-off per workflow.
2. **Start the next thing on user prompt** — no pre-committed direction.

## Parked follow-ups

None.

## Working-tree state

`topology.json` (repo root) may show an extra modification from
drag-tests. Restore or commit at user discretion.

## Dev-loop

After any TS edit: `npm run build` from `tools/topology-vscode/` (tsc
alone doesn't refresh `out/webview.js`). After extension-host
changes: Reload Window in VS Code (Cmd+R). Go: `go build ./...` from
repo root; `go run .` runs `topologies/line.json` (default
`--topology`).

## Delegation hygiene (lesson from this session)

When dispatching a sonnet/haiku subagent for a task that touches an
existing pipeline, **pre-load known facts in the dispatch prompt**:
cite file:line for hooks/contexts/fields that already exist. Subagents
start cold and will re-grep the codebase otherwise. This session paid
~20 tool uses re-discovering that `lane`/`setEdgeLane`/`EdgeActionsCtx`
were already wired. The fix: keep `memory/project_*_plumbing.md`
entries up to date and reference them in delegate prompts.

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
