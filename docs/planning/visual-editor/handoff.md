# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-22, task/held-values-visual)

**Active branch:** `task/held-values-visual` (in flight, not merged).

### What just landed (task/held-values-visual)

Held-value visual redesign: instead of holding the pulse dot at the destination
handle until Done, the pulse now clears immediately on RAF completion, and the
held value is displayed as a badge inside the destination node component at the
relevant input handle.

Key commits on this branch:
- `feat(trace): add KindDone event emitted from In.Done()` — adds `KindDone = "done"` to `Trace/Trace.go`, emits from `In.Done()` in `ports.go` carrying (node, port).
- `revert(webview): clear pulse dot immediately on RAF completion` — use-pulse-animation.ts posts "delivered" and clears pulseT in the same RAF tick; no more pin at t=1.
- `feat(webview): add held-values store for in-transit input port values` — `held-values-state.ts` (imperative bridge, `Map<"nodeId:port", value>`), `held-values-ctx.ts` (React context). pump.ts sets held value on "send" (from edge target/targetHandle) and clears on "done". app.tsx wires HeldValuesCtx.Provider.
- `feat(webview): render held-value badge at input handle in GenericNode` — GenericNode calls `useHeldValuesCtx()` and renders a purple badge next to each input handle while a value is held (between send and done). Only shows when no slot-filled badge is already visible.

### Substrate model contract (stable)

`PacedWire` in `nodes/Wiring/paced_wire.go` has THREE operations: `Send`, `Recv`, `Done`.

- **Send:** fills slot, blocks until `Done` (not until delivery — until receiver explicitly finishes).
- **Recv:** blocks until visual delivered, returns value, does NOT clear slot.
- **Done:** clears slot, unblocks Send.
- **NotifyDelivered** (webview→host→stdin reader): unblocks Recv only.

One `PacedWire` is allocated per destination port (not per edge), so N senders
converging on one port share a single wire — fan-in works correctly.

### Held-values design

- **Store:** `held-values-state.ts` — module-level Map, imperative setter. Key = `${nodeId}:${port}` (destination).
- **Context:** `held-values-ctx.ts` — `HeldValuesCtx` / `useHeldValuesCtx()`.
- **Set:** pump.ts "send" case looks up the matching edge, reads `edge.target` + `edge.targetHandle`, calls `setHeldValue`.
- **Clear:** pump.ts "done" case calls `clearHeldValue(node, port)`.
- **Render:** GenericNode reads the context, shows purple badge (`#4a148c`) at the input handle position while `heldValues.has("nodeId:port")` is true, only if no slot-filled badge is already shown.

### What works (on main + this branch)

- Substrate ring is healthy. `in08` emits both [0,1] values; chain cycles fully.
- Fan-in works: `bootstrap_rg` and `i1` both feed `readGate.FromChainInhibitor`.
- Multi-output slice ports propagate indexed handle names correctly.
- Pulse animation renders concurrent in-flight instances.
- Concurrent fan-out: all outputs fire in parallel.
- **[this branch]** Pulse clears immediately on delivery; held value badge shows in node until Go calls Done.

### Open / deferred

- **Merge task/held-values-visual → main** once verified in live editor.
- Stage 4 cleanup skipped items (not dead — still live):
  - `pulseValueRef` — still used in `SubstrateEdge.tsx` lines 66 and 120.
  - `use-fire-flash.prev` — essential to change-detection in the hook.

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

Check: `go test ./...`, `npm run check:loc`, `bash tools/check-substrate-vocabulary.sh`.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored
to the state you're leaving the branch in, and commit on the active branch (main
if no task is in flight). Do not rely on chat history; the next AI may be a fresh
model with no transcript. The rendered handoff must itself contain this same ALWAYS
clause so the loop is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the
structural source of truth; update the template when an invariant changes.
