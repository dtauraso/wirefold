# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-22, task/pulses-as-instances)

**Active branch:** `task/pulses-as-instances` (not yet merged).

Continuing on wirefold, branch `task/pulses-as-instances`.

### What this branch is doing

Rebuilding pulse animation as a visual-paced wire contract.

Plan doc: `docs/planning/visual-editor/pulses-as-channel-plan.html` (open in browser).
Stages doc: `docs/planning/visual-editor/pulses-as-channel-stages.md`.

### Substrate model contract (current state)

`PacedWire` in `nodes/Wiring/paced_wire.go` has THREE operations: `Send`, `Recv`, `Done`.

- **Send:** fills slot, blocks until `Done` (not until delivery — until receiver explicitly finishes).
- **Recv:** blocks until visual delivered, returns value, does NOT clear slot.
- **Done:** clears slot, unblocks Send.
- **NotifyDelivered** (webview→host→stdin reader): unblocks Recv only.

All 4 node packages (`input`, `readgate`, `chaininhibitor`, `inhibitrightgate`) now call
`<input>.Done()` right after Fire + before downstream TrySend (not deferred until
downstream handshake), which was the fix that unblocked the ring deadlock.

One `PacedWire` is allocated per destination port (not per edge), so N senders
converging on one port share a single wire — fan-in works correctly.

### What works

- Substrate ring is healthy. `in08` emits both [0,1] values; chain cycles fully
  (readGate→i0→i1→back to readGate; i0+i1→inhibitRight0).
- Fan-in works: `bootstrap_rg` and `i1` both feed `readGate.FromChainInhibitor`
  through a shared destination-port-owned wire.
- Multi-output slice ports (`ToNext[]`) correctly propagate indexed handle names
  (`ToNext0`/`ToNext1`) so edgeId resolution for animation is non-null.

### Open / deferred

- `in08` has `init:[0,1]` with no `repeat:true`, so the ring stops after 2 input
  pulses propagate. Not a bug — design question whether to add repeat.
- **Webview pacing follow-up:** "pulse-sits-at-destination-until-Done" rendering
  still pending (Recv-Done enforced substrate-side only; no visual hold at destination).
- **Stages 4 cleanup:** `clearRunState`, `run-start`, `pulseValueRef`,
  `use-fire-flash.prev` still pending removal (inert dead code).
- Optional: remove debug `postLog("pulse.deliver", ...)` from
  `use-pulse-animation.ts:51` if no longer needed for diagnosis.
- Legacy: `loader.go` still has unused `edgeSeeds` path (dead code; `topology.json`
  has no seeds).

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
