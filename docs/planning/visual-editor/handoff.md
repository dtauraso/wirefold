# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

handoff.md is exempt from the 100-LOC budget.

---

## State at handoff (2026-05-22, post planning-doc-triage merge)

**Active branch:** `task/code-self-defends-poc` — just created from main, no work yet.

### Just merged: `task/planning-doc-triage`

Massive doc/memory triage:

- `docs/` trimmed from ~90 files → 3 files (handoff.md,
  continuation-prompt-template.md, audits.md). All planning-doc
  archives, phase plans, and superseded audit notes removed.
- `memory/` trimmed from ~41 entries → 26 entries. Banned-vocab
  memory entries and stale substrate-r notes removed.
- Branch-local planning-doc rule added to CLAUDE.md: planning docs
  created on a task branch must be deleted before merging; only
  `handoff.md` and `audits.md` survive long-term.
- `tools/strip-branch-local-docs.sh` added: helper that finds and
  removes branch-local docs before merge.
- `memory/feedback_code_self_defends.md` recorded: code structure
  that makes the wrong shape impossible beats memory entries that warn
  against drift.

### This branch: `task/code-self-defends-poc`

Proof-of-concept that makes substrate banned vocabulary structurally
hard to reintroduce (per `memory/feedback_code_self_defends.md`).

**Concrete first step:** write a CI lint script, e.g.
`tools/check-substrate-vocabulary.sh`:

- Scans `nodes/`, `Wire.go`, `nodes/Wiring/loader.go`,
  `nodes/Wiring/builders.go`.
- Fails (exit 1) on hits of MODEL.md banned tokens:
  `tick`, `round`, `step`, `schedule`, `ack`, `latch`, `cohort`,
  `scheduler`, `deadline`.
- Case-sensitive matching where helpful (e.g. `Ack` but not
  `background`).
- Output: `file:line` of each hit and the matching token.
- Wire into CI: check `.github/workflows/`, `package.json` scripts,
  or `Makefile` to find where existing checks run.

**After lint passes (or Go cleanups done):**

- Expand to TS substrate boundary: `pump.ts`, `SubstrateEdge.tsx`
  (scope carefully — banned vocab may appear there for legitimate UI
  reasons).
- Schema parser (`parseSpec`) — ensure no `Ack`/`Latch` types can be
  declared.

### Other open branches (parked)

- **task/diagram-animation-fixes** — auto-rerun pulse decay bug
  unresolved. Consecutive runs cause pulse restarts because each Go
  run finishes in ~50 ms and new pulses arrive on edges before
  previous animation completes. Has `slot-trace-sketch.html` tagged
  branch-local.
- **task/visual-paced-substrate** — design doc + impl plan written,
  no code yet. Will make Go wire delivery animation-gated per
  MODEL.md (currently Go delivers synchronously). Has
  `visual-paced-substrate-design.html` +
  `visual-paced-substrate-impl-plan.md` tagged branch-local.

### Tests

`go test ./...` green. `npm run build` green at last verified commit
on main.

## Architecture summary

- **Editor (TS / React Flow):** `GenericNode.tsx` reads `node-defs.ts`
  (generated from Go AST) and renders all kinds. `SubstrateEdge` for
  wires. TS is inert w.r.t. simulation — no firing rules, slot-phase,
  or backpressure logic.
- **Runtime (Go):** `go generate ./...` writes `kinds_generated.go`.
  Each kind's `init()` calls `Wiring.Register`. `Wiring.LoadTopology`
  parses `topology.json` (including `"view"` key) and uses reflection
  to build the port manifest. Non-channel fields populated via `wire:`
  struct tags.
- **Trace pipeline:** Go emits JSONL trace events (`fire` / `send`)
  on stdout. Extension host reads lines, forwards as `trace-event`
  postMessage. Webview routes to `pump.ts`, which writes `lastFire`
  (nodes) or `pulse` (edges). `GenericNode` flashes; `SubstrateEdge`
  animates pulse along the path.

## Adding a kind (2 files)

1. `nodes/<Kind>/<Kind>.go` — struct + firing rule + `init() {
   Wiring.Register("Kind", func() any { return &Struct{} }) }`.
   Struct tags: `wire:"data.<key>"` or
   `wire:"data.initialSlots.<key>"`. `go generate ./...` picks up
   automatically.
2. `nodes/<Kind>/SPEC.md` — optional; only for non-default view
   metadata (accent color, display name override).

## Surviving kinds (4)

Input, ReadGate, ChainInhibitor, InhibitRightGate.

## Dev-loop

After any TS edit: `npm run build` from `tools/topology-vscode/`
(tsc alone doesn't refresh `out/webview.js`). After extension-host
changes: Reload Window in VS Code (Cmd+R). Go: `go build ./...` from
repo root; `go run .` runs `topologies/line.json` by default.

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
