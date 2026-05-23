# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-22, main)

**Active branch:** `main`. No task branch in flight.

### Recently merged (in order)

**task/undo-audit** (merge 09b61cf) — undo/redo coverage broadened and
stabilized against in-flight pulses. Key changes:
- history.ts deep-clones (structuredClone) snapshots + snapshots viewerState alongside RF nodes/edges.
- Transient run-state moved into dedicated stores: `pulse-state.ts`, `fire-flash-state.ts`, `slots-state.ts` (outside snapshot scope; undo/redo cannot wipe them).
- Pulse animation anchored at `pulse.startTime` in pulse-state, computed in pause-adjusted sim time (run-status.ts tracks `pauseAccumulatedMs + pauseStartedAt`; exports `getPauseAdjustedNow`).
- Pulse animation waits for nonzero pathLength before computing duration; remounts no longer hit duration=0.
- pump.ts send/done use `.filter()` instead of `.find()` — fan-in and fan-out both correctly set/clear pulses on every matching edge.
- Held-value badge updates on pulse arrival (t=1 in animation), not on substrate send.

**task/stage4-cleanup** — removed `edgeSeeds` loader path and `pulse.deliver`
debug log. `pulseValueRef` and `use-fire-flash.prev` were confirmed live and
kept. `clearRunState`/`run-start` were already gone before this branch.

**task/held-values-visual** — held-value sticky badges showing last value per
input port in node boxes. Key changes:
- Go emits `KindDone = "done"` from `In.Done()` in `ports.go` (carrying node, port).
- `tryParseTraceEvent` in pump.ts accepts `"done"`.
- Pulse animation clears immediately on RAF completion (no more pin at t=1).
- `held-values-state.ts` + `held-values-ctx.ts`: module-level Map, keyed
  `${nodeId}:${port}` (destination), set on "send", **not cleared on "done"**
  (badges are sticky — overwritten only by next send).
- GenericNode renders a purple badge (`#4a148c`) at the input handle while a
  value is held; hidden if a slot-filled badge is already shown.
- pump.ts "done" routes to clear the pulse only (not the badge).

**task/reading-trip-economy** — five navigation-tax hotspots addressed.
- Removed the 200-LOC file size rule from CLAUDE.md and `check:loc` npm script.
- Collapsed three state+ctx triads into single files: `held-values.ts`, `run-status.ts`, `dimmed.ts`.
- Pinned slot-phase lifecycle contract in MODEL.md; handoff.md "Substrate model contract" now points there.
- Added `tools/topology-vscode/src/webview/rf/nodes/registry.ts` as the single TS edit point for node kinds.
- pump.ts now documents the send→pulse→delivered→done lifecycle with file:line cites.

### Substrate model contract (stable)

See [MODEL.md](../../MODEL.md#slot-phase-lifecycle).

### What works

- Substrate ring is healthy. `in08` emits both [0,1] values; chain cycles fully.
- Fan-in works: `bootstrap_rg` and `i1` both feed `readGate.FromChainInhibitor`.
- Multi-output slice ports propagate indexed handle names correctly.
- Pulse animation renders concurrent in-flight instances, fully derived from shared pulse-state per-frame.
- Concurrent fan-out: all outputs fire in parallel.
- Held-value sticky badges show last value per input port; pulse clears on delivery.
- Undo/redo is robust: pause → drag node → canvas click → undo → node stays put. Delete, edge kind change, port swap, multi-mutation undo trees all verified.
- Pulse animation anchored in pause-adjusted sim time; pause-aware accounting in run-status.ts.

### Open / deferred

Nothing currently blocked. New work should be friction-driven — log friction in
`session-log.md` as it surfaces in live editor use.

### Key files

- `tools/topology-vscode/src/webview/rf/edges/use-pulse-animation.ts` — pulse animation hook (anchored at pulse-state)
- `tools/topology-vscode/src/webview/rf/pump.ts` — event routing from host (send/done fan-in/out)
- `tools/topology-vscode/src/webview/rf/pulse-state.ts` — pulse transient state (outside history snapshot)
- `tools/topology-vscode/src/webview/rf/fire-flash-state.ts` — fire-flash transient state
- `tools/topology-vscode/src/webview/rf/slots-state.ts` — slot phase transient state
- `tools/topology-vscode/src/webview/rf/run-status.ts` — pause accounting (pauseAccumulatedMs, pauseStartedAt, getPauseAdjustedNow)
- `tools/topology-vscode/src/webview/rf/held-values-state.ts` — held-values imperative bridge
- `tools/topology-vscode/src/webview/rf/nodes/GenericNode.tsx` — held-value badge rendering
- `nodes/Wiring/paced_wire.go` — substrate wire contract (see [MODEL.md](../../MODEL.md#slot-phase-lifecycle) slot-phase lifecycle)

## Dev-loop

After TS edit: `npm run build` from `tools/topology-vscode/`.
After Go change: `go build ./...` from repo root, `go test ./nodes/Wiring/...`.
To repro / inspect: clear `.probe/*.jsonl`, reload window in VS Code, Run once, inspect logs.

Check: `go test ./...`, `bash tools/check-substrate-vocabulary.sh`.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored
to the state you're leaving the branch in, and commit on the active branch (main
if no task is in flight). Do not rely on chat history; the next AI may be a fresh
model with no transcript. The rendered handoff must itself contain this same ALWAYS
clause so the loop is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the
structural source of truth; update the template when an invariant changes.
