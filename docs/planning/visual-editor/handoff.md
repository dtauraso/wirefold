# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-21, post slot-trace + drift-elimination + lane→midpointOffset session)

**Active branch:** `task/diagram-animation-fixes`. Not yet merged to main.

### Commits this session (newest first)

- `4fc953b` — **refactor(edges): rename `lane` → `midpointOffset`** — edge property is a pixel offset for the dogleg midpoint, not a discrete lane index. Renamed in `WireProps`, `WIRE_PROPS`, `SubstrateEdge.tsx`, `MidpointDragHandle`, `setEdgeMidpointOffset` on `EdgeActionsCtx`.
- `4176fc5` — **refactor(topology): unify to single topology.json; rename `ToOut` → `ToReadGate`** — deleted `topologies/line.json` and `topologies/` dir. `go run .` now loads root `topology.json` directly. Drift between editor file and runtime file is structurally impossible.
- `53f62cd` — chore(line.json): sync node IDs (superseded by 4176fc5 deleting that file; historical only).
- `99669db` — **fix(save): preserve view on run, not just save** — `handle-message.ts` case `"run"` now goes through `injectViewText`, same as case `"save"`.
- `b7dcd52` — fix(topology): rename `ToNext` → `ToNext0/ToNext1` in line.json (superseded).
- `7374dd8` — **fix(trace): forward slot events from extension host to webview** — `runCommand.ts` allowlist extended; probe-log fix.
- `f805ab4` — **feat(trace): slot event TS side** — pump handler for `kind:"slot"` + value badges in `GenericNode`.
- `808fba8` — **feat(trace): add slot event kind — Go side** — `Trace.Slot` helper; `ReadGate` and `ChainInhibitor` emit on slot fill/empty.

### Major capabilities added this session

1. **Slot trace badges**: Go emits `{kind:"slot", nodeId, port, phase, value?}` on slot fill/empty; webview renders monospace badges next to filled input ports on `GenericNode`. Design sketch: `docs/planning/visual-editor/slot-trace-sketch.html`.
2. **Single source of truth**: `topologies/line.json` deleted; `go run .` loads root `topology.json` directly. Editor and runtime read the same file.
3. **Run preserves view**: clicking Run no longer strips the `view` block from `topology.json`.
4. **`lane` → `midpointOffset` rename**: edge property renamed to reflect what it actually is.

### OPEN BUG — next session's primary task

**Consecutive Runs decay.** First Run animates all edges. Second Run animates only `in0→readGate1`, `bootstrap_rg→readGate1`, `i0→inhibitRight0` — the other edges fire correctly in Go but no pulse animates in the editor.

**Root cause:** `tools/topology-vscode/src/webview/rf/edges/SubstrateEdge.tsx` lines ~298, 303 — `lastPulseStep` is a `useRef<number | undefined>` that is never cleared between Go runs. The dedup guard `if (pulse.simStep === lastPulseStep.current) return` suppresses animation when the new run's step number matches a stored value from the previous run. Downstream edges have structurally-stable step ordinals across runs (goroutine scheduling for `readGate1→i0`, `i0→i1`, `i1→inhibitRight0`, `i1→readGate1` converges to the same step numbers every run: 11, 18, 19, 23–25). Early edges (`in0→readGate1`, `bootstrap_rg→readGate1`) have variable early step numbers (1–9) that differ between runs, so they animate.

**Fix shape (two options):**
- **Option A (simpler):** Extension host sends a `runStart` message to the webview before spawning Go; `pump.ts` handles it by walking RF nodes/edges and clearing per-run state (`lastPulseStep`, `data.slots`, `data.pulse`). Recommend starting here.
- **Option B (more robust):** Per-run nonce/`runId` in trace events; `SubstrateEdge` dedups on `(runId, simStep)` rather than just `simStep`. Handles orphaned events from late-terminating processes but is more invasive.

### Already-wired plumbing (don't re-grep)

- **Slot-trace pipeline:** Go `Trace.Slot` → JSONL → `runCommand.ts` allowlist → postMessage → `pump.ts` case `"slot"` → `n.data.slots` → `GenericNode` badge render.
- **View preservation:** `handle-message.ts` cases `"save"` AND `"run"` both go through `injectViewText(msg.text, extractViewText(document.getText()))`.
- **Edge midpointOffset:** `SubstrateEdge.tsx` dogleg routing reads `data.midpointOffset`; drag handle is `MidpointDragHandle`; setter is `setEdgeMidpointOffset` on `EdgeActionsCtx`.
- **Port positioning:** `setPortPosition(nodeId, portName, side, slot)` via `EdgeActionsCtx`.
- **Arrow sizing:** `MarkerDefs.tsx` sm/md filled/open markers; `SubstrateEdge.tsx` uses `finalSegmentLength()` to pick size geometrically.

### Strict-delegate rule

`memory/feedback_delegate_all_writing.md` (indexed in MEMORY.md). Main session never calls `Edit`/`Write`/`Bash` for file writes; all writes go to subagents.

### Surviving node kinds (4)

Input, ReadGate, ChainInhibitor, InhibitRightGate (terminal sink — no outputs).

## Parked

- **Spec → Go translation (branch `task/pseudocode-spec`):** pseudocode renderings of all 4 kinds + `docs/pseudocode-spec.md`. Resume by reading that doc on the branch (open questions: types, named heterogeneous outputs, send semantics, params-vs-state initializers, terminating loops, trace omission).

## Architecture summary

See MODEL.md for substrate model (inhibitor chain, edge nodes, partition nodes,
AND-gate tree, lateral inhibition, slot-in-node backpressure, round-close stepping).

- **Editor (TS / React Flow):** `GenericNode.tsx` reads `node-defs.ts` and renders all kinds. `SubstrateEdge.tsx` — single home for path routing, midpointOffset drag, arrow sizing, RF integration. TS is inert re: simulation.
- **Runtime (Go):** `go generate ./...` writes `kinds_generated.go`. Each kind's `init()` registers with `Wiring`. `Wiring.LoadTopology` parses `topology.json` (including `"view"` key); non-channel fields populated via `wire:` struct tags.
- **Trace pipeline:** Go emits JSONL on stdout → extension host reads lines → `trace-event` postMessage → webview `pump.ts` → `lastFire`/`pulse`/`slots` on RF store → `GenericNode` flashes/badges, `SubstrateEdge` animates.

## Adding a kind (2 files)

1. `nodes/<Kind>/<Kind>.go` — struct + firing rule + `init()`.
2. `nodes/<Kind>/SPEC.md` — optional; non-default view metadata.

`go generate ./...` picks up new kinds automatically.

## Dev-loop

- TS: `npm run build` from `tools/topology-vscode/` (tsc alone doesn't refresh `out/webview.js`). Reload Window after extension-host changes.
- Go: `go build ./...` from repo root. `go run .` runs `topology.json` (repo root).

## Working-tree state

Clean after this session's commits. `topology.json` at repo root may accumulate drag-test modifications; restore or commit at user discretion before merging.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored
to the state you're leaving the branch in, and commit on the active branch (main
if no task is in flight). Do not rely on chat history; the next AI may be a fresh
model with no transcript. The rendered handoff must itself contain this same ALWAYS
clause so the loop is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the
structural source of truth; update the template when an invariant changes.
