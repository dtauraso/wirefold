# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

handoff.md is exempt from the 100-LOC budget.

---

## State at handoff (2026-05-21, post tidy session)

**Active branch:** main at `a5859c6`. No task in flight. Working tree clean.

### What landed this session (2026-05-20 → 2026-05-21)

- **`topology.view.json` regression investigation** — no current writer
  exists; sidecar code removed in 9e915f7. Recorded in handoff, dropped
  from next-options. Revisit only if it recurs.
- **Stale memory audit** — deleted `feedback_run_is_input_only.md`
  (described pre-RF substrate-r RAF/wire.load model); refreshed index
  line for `feedback_edge_seed_required_for_rings.md` (now points to
  `data.initialSlots` rather than `wire.data.seed`). Commit `bc2aeaf`.
- **Topology-field-collapse step 4 dissolved** — instead of solving the
  registry→Spec derivation, deleted the 4 unused meta fields (`timing`,
  `cycleAnchor`, `runtime`, `legend`). Only `notes` survives. Commit
  `93e46ef`. Nothing left to derive.
- **`lastRecv` removed** — write-only field with no consumer; pulse
  animation already conveys the same fact. Commit `34eba3e`.
- **Double-click rename UI removed** — unreliable interaction; future
  replacement is button-driven. Sublabel double-click editing still
  works. Underlying rename op in `state/ops/rename.ts` preserved.
  Commit `e589858`.
- **TS-execution-drift audit** — created and deleted branch
  `task/ts-nodes-inert` after audit returned clean. The TS side is
  already inert with respect to simulation semantics: `pump.ts` is the
  only execution bridge and writes only `lastFire` (node) and `pulse`
  (edge); everything else is editor / viewer / readout.

### Surviving kinds (4)

Input, ReadGate, ChainInhibitor, InhibitRightGate.

### Tests

`go test ./...` green. `npm run build` green.

## Architecture summary

- **Editor (TS / React Flow):** one `GenericNode.tsx` reads `node-defs.ts`
  (generated from Go AST) and renders all kinds. `SubstrateEdge` for
  wires. State via RF + thin helpers. TS is **inert** with respect to
  simulation — no firing rules, slot-phase, or backpressure logic.
- **Runtime (Go):** `go generate ./...` writes `kinds_generated.go` at
  repo root. Each kind's `init()` calls `Wiring.Register`.
  `Wiring.LoadTopology` parses `topology.json` (including `"view"` key)
  and uses reflection on each registered struct to build the port
  manifest. Non-channel fields populated via `wire:` struct tags.
- **Trace pipeline:** Go emits JSONL trace events (`fire` / `send`,
  `recv` is no-op) on stdout. Extension host (`runCommand.ts`) reads
  lines, forwards as `trace-event` postMessage. Webview routes to
  `pump.ts`, which writes `lastFire` (nodes) or `pulse` (edges).
  Renderers (`GenericNode`, `SubstrateEdge`) animate from those fields.

## Adding a kind (2 files)

1. `nodes/<Kind>/<Kind>.go` — struct + firing rule + `init() {
   Wiring.Register("Kind", func() any { return &Struct{} }) }`.
   Non-channel fields read from `data.*` JSON use struct tags:
   - `wire:"data.<key>"` — copies `NodeData.<Key>` (e.g. `[]int` from `data.init`)
   - `wire:"data.initialSlots.<key>"` — reads `NodeData.InitialSlots[key]` (int)
   `go generate ./...` picks it up automatically (blank import generated).
2. `nodes/<Kind>/SPEC.md` — **optional**; only needed for non-default view
   metadata (accent color, display name override). Port names and data
   types are derived from the Go struct by `tools/gen-node-defs`.

## Next options

1. **Trace replay (the `TransportControls` play button)** —
   conceptually-grounded design from this session: TS nodes are inert
   visual projections, so replay is just re-feeding buffered Go trace
   events through the same pump on a TS-side clock. Build sketch:
   (a) buffer events as pump receives them; (b) scheduler module walks
   buffer at a clock; (c) wipe `lastFire`/`pulse` on every node/edge
   for loop reset (no initial-state snapshot needed); (d) enable the
   play button when run is terminal and buffer non-empty. No Go work.
   Pacing question (preserve wall-clock gaps vs fixed cadence per
   simStep) still open.
2. **Optional guardrail** — add a lint/test that fails if any file
   outside `pump.ts` writes `lastFire` or `pulse`, pinning the
   substrate boundary mechanically. Speculative; only if friction
   appears.
3. **Start the next thing on user prompt** — no pre-committed direction.

## Parked follow-ups

1. **Trace replay** (option 1 above) — paused mid-design before
   pacing decision.

## Working-tree state

Clean.

## Dev-loop

After any TS edit: `npm run build` from `tools/topology-vscode/` (tsc
alone doesn't refresh `out/webview.js`). After extension-host changes:
Reload Window in VS Code (Cmd+R). Go: `go build ./...` from repo root;
`go run .` runs `topologies/line.json` (default `--topology`).

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt
tailored to the state you're leaving the branch in, and commit on the
active branch (main if no task is in flight). Do not rely on chat
history; the next AI may be a fresh model with no transcript. The
rendered handoff must itself contain this same ALWAYS clause so the
loop is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as
the structural source of truth; update the template when an invariant
changes.
