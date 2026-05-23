# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-22, task/held-values-visual)

**Active branch:** `task/held-values-visual` (in flight, not merged).

### What just landed (task/held-values-visual)

Pulse-sits-at-destination-until-Done: the webview now holds the pulse dot at
the destination handle (t=1) after animation completes, and only clears it when
Go signals Done via a new "done" trace event.

Key commits on this branch:
- `feat(trace): add KindDone event emitted from In.Done()` — adds `KindDone = "done"` to `Trace/Trace.go`, emits from `In.Done()` in `ports.go` carrying (node, port).
- `feat(webview): hold pulse at destination until Go signals Done` — pump.ts handles "done" by clearing pulse data; use-pulse-animation.ts pins pulseT=1 after RAF completes (posts "delivered"), then a separate effect clears it when pulse data is removed.

### Substrate model contract (stable)

`PacedWire` in `nodes/Wiring/paced_wire.go` has THREE operations: `Send`, `Recv`, `Done`.

- **Send:** fills slot, blocks until `Done` (not until delivery — until receiver explicitly finishes).
- **Recv:** blocks until visual delivered, returns value, does NOT clear slot.
- **Done:** clears slot, unblocks Send.
- **NotifyDelivered** (webview→host→stdin reader): unblocks Recv only.

One `PacedWire` is allocated per destination port (not per edge), so N senders
converging on one port share a single wire — fan-in works correctly.

### What works (on main + this branch)

- Substrate ring is healthy. `in08` emits both [0,1] values; chain cycles fully.
- Fan-in works: `bootstrap_rg` and `i1` both feed `readGate.FromChainInhibitor`
  through a shared destination-port-owned wire.
- Multi-output slice ports (`ToNext[]`) correctly propagate indexed handle names
  (`ToNext0`/`ToNext1`) so edgeId resolution for animation is non-null.
- Pulse animation renders concurrent in-flight instances (per-emit simTime anchoring).
- Concurrent fan-out: all outputs fire in parallel (no head-of-line serialization).
- **[this branch]** Pulse sits at destination handle until downstream node calls Done.

### Open / deferred

- **Merge task/held-values-visual → main** once verified in live editor.
- **Stage 4 cleanup (task/stage4-cleanup):** removed `edgeSeeds` path from
  `loader.go` and debug `postLog("pulse.deliver")` from `use-pulse-animation.ts`.
  Skipped items (not dead — still live):
  - `clearRunState` — not found in codebase (already gone).
  - `run-start` — not found in codebase (already gone).
  - `pulseValueRef` — still used in `SubstrateEdge.tsx` lines 66 and 120; not dead.
  - `use-fire-flash.prev` — `prev` ref is essential to change-detection in the hook's
    `useEffect`; not dead.

### Key files

- `tools/topology-vscode/src/webview/rf/edges/use-pulse-animation.ts` — pulse animation hook
- `tools/topology-vscode/src/webview/rf/pump.ts` — event routing from host
- `nodes/Wiring/paced_wire.go` — substrate wire contract

## Dev-loop

After TS edit: `npm run build` from `tools/topology-vscode/`.
After Go change: `go build ./...` from repo root, `go test ./nodes/Wiring/...`.
To repro / inspect: clear `.probe/*.jsonl`, reload window in VS Code, Run once, inspect logs.

Check: `go test ./...`, `npm run check:loc`, `bash tools/check-substrate-vocabulary.sh`.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored
to the state you're leaving the branch in, and commit on the active branch (main
if no task is in flight). Do not rely on chat history; the next AI may be a fresh
model with no transcript. The rendered handoff must itself contain this same ALWAYS
clause so the loop is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the
structural source of truth; update the template when an invariant changes.
