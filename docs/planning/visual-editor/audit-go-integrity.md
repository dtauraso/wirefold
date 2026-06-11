---
branch: task/full-code-audit
---

# Go / model integrity audit

Read-only audit of the Go network (`nodes/`, `nodes/Wiring/loader.go`,
`nodes/Wiring/builders.go`) and the pump (`tools/topology-vscode/src/webview/rf/pump.ts`)
against [MODEL.md](../../../MODEL.md). No code was edited.

## Verdict

No CONFIRMED Go-model violations. Build and tests green; all five
guard scripts clean. Two low-severity doc-drift suspicions noted below.

## Build / test / guard results

- `go build ./...` — PASS (no output).
- `go test ./...` — PASS. All packages ok (Trace, nodes/Wiring,
  chaininhibitor, inhibitrightgate, input, readgate); cmd/tools have no
  test files.
- Guard scripts (all exit 0, "clean"):
  - `tools/check-go-vocabulary.sh`
  - `tools/check-trace-kind-parity.sh`
  - `tools/check-no-ts-timers.sh`
  - `tools/check-message-kind-parity.sh`
  - `tools/check-slot-phase-boundary.sh`

`scripts/stop-checks.sh` invokes exactly these five plus conditional
go-build / tsc / webview-build; it gates on a working-tree diff so it
short-circuits to exit 0 when nothing is changed.

## 1. Banned-vocabulary drift — CLEAN

`tools/check-go-vocabulary.sh:29` scans the Go network
(`nodes/**`, excluding `Trace/`, plus `Wire.go`/`main.go` if present) with
`grep -w` for: tick, round, schedule, ack, latch, cohort, scheduler,
deadline, step. Result: clean. No banned token appears as a whole word in
Go.

Caveat (suspicion, not a violation): the scan is whole-word over Go
files only. It does not cover comments containing substrings, the
TS layer, or `Trace/Trace.go` (intentionally excluded — `Step` is the
legitimate event-ordinal field, used as `step` in `pump.ts:44`). This is
by design and matches MODEL.md's allowed vocabulary, but means a banned
term smuggled into a TS comment or a Go substring would not be flagged.

## 2. Lifecycle / backpressure / gate contract — MATCHES MODEL

- **PacedWire** (`nodes/Wiring/paced_wire.go`) implements the model's three
  operations + cross-boundary signal exactly as MODEL.md §"Slot phase
  lifecycle" pins them:
  - `Send` (paced_wire.go:39) blocks until slot empty, claims slot
    (`pw.slot`/`pw.hasSend`, lines 57–58), then blocks on `myDone` until
    `Done` (lines 67–72). Stays blocked the full receiver lifetime — matches
    "Send does not return on visual delivery."
  - `Recv` (paced_wire.go:78) blocks until `deliveryCh` closes (lines
    101–107); slot is NOT cleared — matches contract.
  - `Done` (paced_wire.go:114) clears slot (`pw.slot=nil`, `pw.hasSend=false`,
    lines 118–119), broadcasts, unblocks Send.
  - `NotifyDelivered` (paced_wire.go:129) closes `deliveryCh`, the sole
    cross-boundary unblock — matches.
  - Uses `hasSend bool`, no slot-phase string literals (confirmed by
    `check-slot-phase-boundary.sh` go-scan, clean).
- **Backpressure (slot-in-node)**: enforced structurally. `Send` cannot
  refill until `Done` clears the slot (paced_wire.go:45–51 spin on
  `hasSend`). Go cannot overrun the visual layer — matches MODEL.md
  §"Cross-boundary contract."
- **Fan-in by construction**: `loader.go:88–98` allocates one `*PacedWire`
  per `destNode.destPort`; edges sharing a destination port reuse the same
  pointer. Matches MODEL.md §"Slot phase lifecycle" opening.
- **AND-gate tree / lateral inhibition / inhibitor chain**: implemented as
  local precondition gating over slots, not a global structure:
  - `nodes/inhibitrightgate/node.go:42` fires only on `HasLeft && HasRight`
    (AND gate); `Left==1 && Right==0 → 1` else 0 is the inhibit-right /
    lateral-inhibition rule (lines 44–46).
  - `nodes/readgate/node.go:40` fires only on `HasValue && HasChainInhibitor`
    (AND gate).
  - `nodes/chaininhibitor/node.go:17–38` forms the inhibitor chain: receive,
    fire, fan out `Held` to `ToNext`, then shift `Held = value`.
  All three call `Done()` after firing to release the slot — backpressure
  preserved. No global round/clock; each is a local poll loop gated on
  `ctx.Done()` and slot arrival.
- **Self-scheduling / no central walker**: each node's `Update` is its own
  goroutine loop (`nodes/Wiring/node.go:13`); in paced mode `In.TryRecv`
  blocks on `Recv` (`nodes/Wiring/ports.go:54–62`) rather than busy-spinning.
  No central scheduler. Matches MODEL.md §"Driver."
- **Round-close stepping**: ABSENT, as required. No round/tick/simultaneity
  layer exists in Go. Coordination is via destination slot phase read
  directly by senders. Matches MODEL.md §"Driver."
- **Global gate**: model places play/pause on wire animations in the TS
  layer; Go has no gate code, consistent with "gate halts wires, not
  nodes."

## 3. Drift rule (slot/backpressure/firing logic in TS outside pump.ts) — CLEAN

Grep of the rf tree for `slotPhase|hasSend|NotifyDelivered|backpressure|
precondition|fire(|firing` outside `pump.ts`/`messages.ts` returned exactly
one hit:
- `adapter/spec-to-flow-helpers.ts:120` — a COMMENT ("flow-to-spec
  round-trips backpressure/delay config") on a verbatim data passthrough. No
  logic. Not a violation.

The single TS slot-phase write is `pump.ts:54–56` (writing
`{ phase: "filled" }` / `{ phase: "empty" }` into the RF SlotMap), which is
the canonical TS home the boundary check exempts. The "delivered" post (the
NotifyDelivered trigger) lives in the animation layer
(`pulse-state.ts:28–47`), which is render-driven, not slot-phase transition
logic — correct per model. No `setInterval`/`setTimeout` anywhere in the rf
tree (broader than the pump-only `check-no-ts-timers.sh`).

## 4. Doc-vs-code drift (SUSPICIONS, low severity)

- **`Wire.go` does not exist.** MODEL.md:4, MODEL.md:46 and CLAUDE.md's
  "Go model" pointer all name `Wire.go` as a Go file to read
  before editing. `find` across the repo (excluding node_modules) returns no
  `Wire.go`. The Go wire type is `nodes/Wiring/paced_wire.go`. The
  guard scripts already treat `Wire.go` as optional (`[[ -f ... ]]` in
  `check-go-vocabulary.sh:14` and `check-slot-phase-boundary.sh:47`),
  so this is harmless to tooling but the doc references are stale. Recommend
  repointing MODEL.md / CLAUDE.md to `nodes/Wiring/paced_wire.go`.
- **`pump.ts` is 110 lines and references handlers by comment marker**
  (`PUMP_SLOT_HANDLER`, `PUMP_DONE_HANDLER`, `PUMP_DONE_HANDLER`) exactly as
  MODEL.md:142/155 cite. The `pump.ts handles "done"` claim in MODEL.md:142
  is accurate (pump.ts:91 clears the pulse; the actual `delivered` post is
  in pulse-state.ts — MODEL.md:142's parenthetical correctly attributes the
  stdin send to the extension host). No drift here; noted for completeness.

## Files reviewed

- `/Users/David/Documents/github/wirefold/MODEL.md`
- `/Users/David/Documents/github/wirefold/nodes/Wiring/paced_wire.go`
- `/Users/David/Documents/github/wirefold/nodes/Wiring/loader.go`
- `/Users/David/Documents/github/wirefold/nodes/Wiring/builders.go`
- `/Users/David/Documents/github/wirefold/nodes/Wiring/ports.go`
- `/Users/David/Documents/github/wirefold/nodes/Wiring/node.go`
- `/Users/David/Documents/github/wirefold/nodes/chaininhibitor/node.go`
- `/Users/David/Documents/github/wirefold/nodes/inhibitrightgate/node.go`
- `/Users/David/Documents/github/wirefold/nodes/readgate/node.go`
- `/Users/David/Documents/github/wirefold/nodes/input/node.go`
- `/Users/David/Documents/github/wirefold/tools/topology-vscode/src/webview/rf/pump.ts`
- `/Users/David/Documents/github/wirefold/tools/topology-vscode/src/webview/rf/pulse-state.ts`
- guard scripts under `/Users/David/Documents/github/wirefold/tools/`
