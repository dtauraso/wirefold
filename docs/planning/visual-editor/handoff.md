# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

handoff.md is exempt from the 100-LOC budget.

---

## State at handoff (2026-05-20, post collapse-representations merge)

**Active branch:** `task/navigation-tax-audit`.
Branched from `main` at `041bb7b`. No task was in flight before this branch.

### What landed (4-step collapse)

Planned in `docs/planning/visual-editor/collapse-representations.md`.

- **Step 1** (`9237801`) — generate `kinds_generated.go` at repo root via
  `go:generate` (generator at `tools/gen-kind-imports/`); `main.go` no longer
  needs manual blank-import maintenance.
- **Step 2** (`2af47b8`, `ebc5eb3`, `c94ea7e`) — `tools/gen-node-defs` Go program
  walks `nodes/*/*.go` AST to derive port names and node-data types; replaces the
  old `gen-node-defs.mjs` (deleted). Generates `node-defs.ts` and
  `node-data-types.ts`. SPEC.md is now optional (view metadata / accent colors only).
- **Step 3** (implicit) — hand-maintained layers dropped from 6 to 2:
  Go struct (source of truth) + `topology.json` (instance data). Generated
  artifacts: `kinds_generated.go`, `node-defs.ts`, `node-data-types.ts`.
- **Step 4** (`9e915f7`) — `topology.view.json` merged into `topology.json#view`
  and deleted. View metadata now lives under the `"view"` key of `topology.json`.

### Surviving kinds (4)

Input, ReadGate, ChainInhibitor, InhibitRightGate.

### Tests

Per-kind firing-rule unit tests at `nodes/<Kind>Node/firing_rule_test.go`.
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

1. **Run the navigation-tax audit** — see
   [navigation-tax.md](navigation-tax.md). Grep every kind/port string
   call site and land the output table in that doc. No fixes until the
   table exists.

## Parked follow-ups

1. **Rename UX** — double-click-to-rename was unreliable post-Phase 3.
2. **`TransportControls` play button** — stubbed inert.
3. **`lastRecv` visualization** — pump writes it; no kind renders it.
4. **Stale memory entries** — several `feedback_*`/`project_*` files
   reference substrate-r concepts pre-dating the RF migration.
5. **`spec-to-flow`/`flow-to-spec` adapters** — may be near-identity
   functions worth collapsing post-RF migration.

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
