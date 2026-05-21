# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

handoff.md is exempt from the 100-LOC budget.

---

## State at handoff (2026-05-20, post navigation-tax merge)

**Active branch:** main at `62db7db`. No task in flight.

### What landed (navigation-tax audit + identifier-scatter collapses)

- **Navigation-tax audit** — grepped every kind/port/wire-prop/field call site;
  results in `docs/planning/visual-editor/navigation-tax.md`. Tax table highlights:
  node kind, port, wire-prop scalar, topology meta field, and animation field
  renames are all now ~0 to Low. Wire-prop enum and animation new-kind are Low.
- **Wire-prop collapse** — complete (`docs/planning/visual-editor/wire-prop-collapse.md`).
- **Animation-field collapse** — complete (`docs/planning/visual-editor/animation-field-collapse.md`).
- **Topology-field collapse** — complete except step 4 deferred:
  deriving `Spec` TS type from `TOPOLOGY_META_FIELDS` blocked on circular import
  between `meta-field-defs.ts` and `types-graph.ts`. Documented in
  `docs/planning/visual-editor/topology-field-collapse.md`.
- **Generator improvements** — `tools/gen-node-defs` uses `strings.Cut` over
  index slicing; edge-kind inline-literal cleanup landed.
- **Surfaces audited, found no real tax:** phase vocabulary (typed, not stringly);
  SPEC.md section parser (parameterized in `tools/gen-node-defs`).

### Surviving kinds (4)

Input, ReadGate, ChainInhibitor, InhibitRightGate.

### Tests

`go test ./...` green.


## Adding a kind (2 files)

1. `nodes/<Kind>/<Kind>.go` — struct + firing rule + `init() {
   Wiring.Register("Kind", func() any { return &Struct{} }) }`.
   Non-channel fields read from `data.*` JSON use struct tags:
   - `wire:"data.<key>"` — copies `NodeData.<Key>` (e.g. `[]int` from `data.init`)
   - `wire:"data.initialSlots.<key>"` — reads `NodeData.InitialSlots[key]` (int)
   `go generate ./...` picks it up automatically (blank import generated).
2. `nodes/<Kind>/SPEC.md` — **optional**; only needed for non-default view
   metadata (accent color, display name override). Port names and data types
   are now derived from the Go struct by `tools/gen-node-defs`.

`node-defs.ts` and `node-data-types.ts` regenerate via `npm run gen:node-defs`
(also runs as prebuild). `builders.go` is not touched.

## Architecture summary

- **Editor (TS / React Flow):** one `GenericNode.tsx` reads `node-defs.ts`
  (generated from Go AST) and renders all kinds. `SubstrateEdge` for wires.
  State via RF + thin helpers. `gen-node-defs.mjs` is gone; replaced by
  `tools/gen-node-defs` (Go program, `go run ../../tools/gen-node-defs`).
- **Runtime (Go):** `go generate ./...` writes `kinds_generated.go` at repo root.
  Each kind's `init()` calls `Wiring.Register`. `Wiring.LoadTopology` parses
  `topology.json` (including `"view"` key) and uses reflection on each
  registered struct to build the port manifest. Non-channel fields populated
  via `wire:` struct tags.

## Next options

1. **Tackle a parked follow-up with accumulated friction** — rename UX,
   inert TransportControls play button, or lastRecv visualization are the
   most likely candidates. Pick whichever the user surfaces first.
2. **Start the next thing on user prompt** — no pre-committed direction.

Investigated 2026-05-20: no current writer of `topology.view.json` exists
(sidecar code removed in 9e915f7; save path merges `view` into
`topology.json` via `injectViewText`). Revisit only if the regression
actually recurs.

## Parked follow-ups

1. **Rename UX (button-driven)** — double-click path removed in e589858;
   replacement is a button-press flow, not yet built. Sublabel editing
   still works on double-click.
2. **`TransportControls` play button** — stubbed inert.

Retired 2026-05-20: stale memory audit (1 file deleted, 1 index line
refreshed); topology-field-collapse step 4 dissolved by deleting 4
unused meta fields (`timing`, `cycleAnchor`, `runtime`, `legend`) in
93e46ef — registry now has one entry (`notes`), nothing left to derive.
`lastRecv` removed in 34eba3e — write-only field with no consumer;
pulse animation already conveys the same fact.

## Working-tree state

Clean.

## Dev-loop

After any TS edit: `npm run build` from `tools/topology-vscode/` (tsc alone
doesn't refresh `out/webview.js`). After extension-host changes: Reload
Window in VS Code (Cmd+R). Go: `go build ./...` from repo root;
`go run .` runs `topologies/line.json` (default `--topology`).

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt
tailored to the state you're leaving the branch in, and commit on the
active branch (main if no task is in flight). Do not rely on chat history;
the next AI may be a fresh model with no transcript. The rendered handoff
must itself contain this same ALWAYS clause so the loop is
self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the
structural source of truth; update the template when an invariant changes.
