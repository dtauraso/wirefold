# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-22, main)

**Active branch:** `main`. No task branch in flight.

### Recently merged (in order)

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

### Substrate model contract (stable)

`PacedWire` in `nodes/Wiring/paced_wire.go` has THREE operations: `Send`, `Recv`, `Done`.

- **Send:** fills slot, blocks until `Done` (not until delivery — until receiver explicitly finishes).
- **Recv:** blocks until visual delivered, returns value, does NOT clear slot.
- **Done:** clears slot, unblocks Send.
- **NotifyDelivered** (webview→host→stdin reader): unblocks Recv only.

One `PacedWire` is allocated per destination port (not per edge), so N senders
converging on one port share a single wire — fan-in works correctly.

### What works

- Substrate ring is healthy. `in08` emits both [0,1] values; chain cycles fully.
- Fan-in works: `bootstrap_rg` and `i1` both feed `readGate.FromChainInhibitor`.
- Multi-output slice ports propagate indexed handle names correctly.
- Pulse animation renders concurrent in-flight instances.
- Concurrent fan-out: all outputs fire in parallel.
- Held-value sticky badges show last value per input port; pulse clears on delivery.

### Open / deferred

Nothing currently blocked. New work should be friction-driven — log friction in
`session-log.md` as it surfaces in live editor use.

### Key files

- `tools/topology-vscode/src/webview/rf/edges/use-pulse-animation.ts` — pulse animation hook
- `tools/topology-vscode/src/webview/rf/pump.ts` — event routing from host
- `tools/topology-vscode/src/webview/rf/held-values-state.ts` — held-values imperative bridge
- `tools/topology-vscode/src/webview/rf/held-values-ctx.ts` — held-values React context
- `tools/topology-vscode/src/webview/rf/nodes/GenericNode.tsx` — held-value badge rendering
- `nodes/Wiring/paced_wire.go` — substrate wire contract

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
