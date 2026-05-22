# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

handoff.md is exempt from the 100-LOC budget.

---

## State at handoff (2026-05-22, post planning-doc-triage)

**Active branch:** `task/planning-doc-triage` — ready to merge to main.

### What landed this session (task/planning-doc-triage)

Massive doc/memory triage:

- `docs/` trimmed from ~90 files → 3 files (handoff.md,
  continuation-prompt-template.md, audits.md). All planning-doc
  archives, phase plans, and superseded audit notes removed.
- `memory/` trimmed from ~41 entries → 26 entries. Banned-vocab
  memory entries and stale substrate-r notes removed.
- Branch-local planning-doc rule added to CLAUDE.md: planning docs
  created on a task branch must be deleted before merging; only
  `handoff.md` and `audits.md` survive long-term.
- `tools/strip-branch-local-docs.sh` added: helper script that finds
  and removes docs matching the branch-local pattern before merge.
- `memory/feedback_code_self_defends.md` recorded: code structure
  that makes the wrong shape impossible beats memory entries that warn
  against drift.

### Next branch: `task/code-self-defends-poc`

Proof-of-concept that makes substrate banned-vocabulary structurally
hard to reintroduce.

**Start with:** a CI lint that scans `nodes/`, `Wire.go`,
`nodes/Wiring/` for the banned tokens listed in MODEL.md:

```
tick, round, step, schedule, ack, latch, cohort, scheduler, deadline
```

Lint fails on any match inside those paths. If the scan comes back
clean, expand to the TS substrate boundary (`tools/topology-vscode/src/webview/rf/`
excluding `pump.ts` — pump is the intentional bridge, not a drift
site).

### Other open work (parked)

- **task/diagram-animation-fixes** — auto-rerun pulse decay after
  simulation completes; branch exists, work in progress.
- **task/visual-paced-substrate** — implementation plan written, not
  started. Substrate cycles paced by the visual layer (per
  `feedback_substrate_vs_coordinator_bias.md`).

### Tests

`go test ./...` green. `npm run build` green at last verified commit
on main.

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

## Surviving kinds (4)

Input, ReadGate, ChainInhibitor, InhibitRightGate.

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
