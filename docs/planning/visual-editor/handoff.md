# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-22, main)

**Active branch:** `main` (post-merge of `task/i0-concurrent-fanout`).

No task branch in flight. The visual-hold work (`task/held-values-visual`) is set aside.

### What just landed (task/i0-concurrent-fanout)

Concurrent fan-out: `Fire` in the substrate now emits to all output channels
concurrently rather than sequentially, so multi-output nodes don't serialize
their downstream paths.

### Substrate model contract (stable)

`PacedWire` in `nodes/Wiring/paced_wire.go` has THREE operations: `Send`, `Recv`, `Done`.

- **Send:** fills slot, blocks until `Done` (not until delivery — until receiver explicitly finishes).
- **Recv:** blocks until visual delivered, returns value, does NOT clear slot.
- **Done:** clears slot, unblocks Send.
- **NotifyDelivered** (webview→host→stdin reader): unblocks Recv only.

One `PacedWire` is allocated per destination port (not per edge), so N senders
converging on one port share a single wire — fan-in works correctly.

### What works (on main)

- Substrate ring is healthy. `in08` emits both [0,1] values; chain cycles fully.
- Fan-in works: `bootstrap_rg` and `i1` both feed `readGate.FromChainInhibitor`
  through a shared destination-port-owned wire.
- Multi-output slice ports (`ToNext[]`) correctly propagate indexed handle names
  (`ToNext0`/`ToNext1`) so edgeId resolution for animation is non-null.
- Pulse animation renders concurrent in-flight instances (per-emit simTime anchoring).
- Concurrent fan-out: all outputs fire in parallel (no head-of-line serialization).

### Set aside (task/held-values-visual)

Branch exists but is parked. Work: pulse should sit at destination handle from
`Recv` until `Done` is called. Substrate enforces this; webview does not yet
mirror it (pulses disappear immediately on delivery).

### Open / deferred

- **Webview pacing (held-values-visual, set aside):** pulse-sits-at-destination-until-Done.
- **Stages 4 cleanup (deferred):** `clearRunState`, `run-start`, `pulseValueRef`,
  `use-fire-flash.prev` still pending removal (inert dead code).
- Optional: remove debug `postLog("pulse.deliver", ...)` from
  `use-pulse-animation.ts:51` if no longer needed for diagnosis.
- Legacy: `loader.go` still has unused `edgeSeeds` path (dead code; `topology.json`
  has no seeds).

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
