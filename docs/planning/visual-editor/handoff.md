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
`<input>.Done()` after finishing a value (typically after Fire + downstream TrySend succeeds).

### What works

ReadGate consumes one value per fire cycle. Visual pacing holds: `in0` blocks between
sends until ReadGate fires. Pulses animate one-at-a-time on each wire.

### Open bug: in08 only sends 1 of its 2 init values [0, 1]

Probe logs (after one fresh Run) confirm:
- `in08` emits send for value=0 only. No 2nd fire event, no 2nd send event.
- ReadGate fires once, chain progresses one cycle (i0 fires and sends downstream), then stops.
- No `runner-errors-last.json` produced.

**Hypothesis:** `Input.Update`'s goroutine returns or blocks before its 2nd iteration.
Either `ctx.Err()` is non-nil (something is canceling ctx — possibly stdin EOF via
`RunStdinReader` returning), or Fire/TrySend on the 2nd iteration is blocking on the
new Recv+Done semantics.

**Next investigation:** check whether `RunStdinReader` is exiting (transient EOF or parse
error triggers `cancel()`). Add stderr logging to `main.go` or `nodes/input/node.go` to
confirm. `Input.Update` is at `nodes/input/node.go:15-25`; stdin reader pattern:
`main.go:32-35`.

### Deferred (not blocking)

**Webview pacing follow-up:** today pulses animate mid-flight then disappear at delivery.
The richer "pulse-sits-at-destination-until-Done" rendering is not yet implemented —
Recv-Done is enforced substrate-side only. Add once the in08 bug is resolved.

**Stages 4 cleanup:** `clearRunState`, `run-start`, `pulseValueRef`, `use-fire-flash.prev`
haven't been deleted yet. They're inert but should be removed once substrate is fully working.

### Outside-this-branch carry-forwards

`topology.json` has uncommitted working-tree drift on main (untouched by this branch).

## Dev-loop

After TS edit: `npm run build` from `tools/topology-vscode/`.
After Go change: `go build ./...` from repo root, `go test ./nodes/Wiring/...`.
To repro bug: clear `.probe/*.jsonl`, reload window in VS Code, Run once, inspect logs.

Check: `go test ./...`, `npm run check:loc`, `bash tools/check-substrate-vocabulary.sh`.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored
to the state you're leaving the branch in, and commit on the active branch (main
if no task is in flight). Do not rely on chat history; the next AI may be a fresh
model with no transcript. The rendered handoff must itself contain this same ALWAYS
clause so the loop is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the
structural source of truth; update the template when an invariant changes.
