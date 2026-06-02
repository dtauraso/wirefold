# Substrate model

Read this before changing anything in the **Go substrate** (`nodes/`,
`nodes/Wiring/paced_wire.go`, `nodes/Wiring/loader.go`, `nodes/Wiring/builders.go`) or the **pump**
(`tools/topology-vscode/src/webview/three/pump.ts`),
or anything that schedules/orders work. If your
reasoning slips into banned vocabulary (below), you are in the wrong
frame. Stop, re-read this file, and re-derive from the model.

The pivot from earlier substrate versions: backpressure is NOT enforced
by the wire. `PacedWire` is pure transport — it holds one in-flight bead
at a time, reports `inFlight` (cleared on delivery), delivers
non-blockingly (deferring into the slot via `Done` when the slot is
full), exposes `WaitConsumed` (fires when the destination calls `Done`),
and has `Reset()` to drop a bead and free a parked sender when an edge is
deleted. It applies NO send policy of its own.

Each SOURCE NODE owns its send rule and applies it PER OUTGOING PORT in
its `Update` loop. Two rules exist: `consumeGated` (after sending, the
node waits via `WaitConsumed` for the destination to consume — the
default) and `fireAndForget` (NON-BLOCKING: the node places the bead only
if the wire is free, and if the wire is busy/in-flight it DROPS the send
and moves on — it never blocks and never waits for consume).
`consumeGated` uses the blocking `Send` + `WaitConsumed`; `fireAndForget`
uses `PacedWire.TryPlace` / `Out.TryEmit`. The rule lives on the SOURCE
NODE, keyed by its outgoing port name, at `node.data.sendRules` (a map:
output-port-name → rule). The EDGE carries no rule. The loader reads
`node.data.sendRules[portName]` onto each source `Out` (default
`consumeGated`); the node branches on `out.Gated()`. One node may use
different rules on different outgoing ports, and the rule survives edge
deletion because it lives on the node, not the edge. The slot lives
in Go (`PacedWire.slot`/`hasSend`); the wire's in-flight staging area
(`PacedWire.pending`/`inFlight`) is separate from the slot.
(Amended 2026-05-31: the wire no longer enforces backpressure, and the
send rule is node-owned per output port — NOT a per-edge spec field.
Prior text said "the source observes its own wire's in-flight bit", "a
full destination slot keeps the bead on the wire, so the source stays
blocked", and described `sendRule` as "a per-edge spec field (top-level
on the edge)" — those are all superseded. Send policy now lives in each
source node, keyed by outgoing port via `node.data.sendRules`, and
`fireAndForget` is non-blocking via `TryPlace`/`TryEmit`.)

## What this network computes

The diagram+Go runtime network is a concurrent dataflow system. The
topology IS the logic — behavior emerges from wiring, not procedural
code. Goroutines and channels replace conventional control flow.

Lateral inhibition, contrast detection, and competitive binding are
implemented as circuit primitives.

The visual editor is the medium for authoring and observing the
network; the network itself is the diagram+Go runtime.

## What things are

- **Slot.** A per-input cell on a destination node. Phase:
  `empty | filled(v) | consumed`. Phase is ordinal: filled happened,
  then consumed happened. No "during."
- **Wire.** Transient delivery + visual depiction. Phase:
  `empty | in-flight(v) | empty`. The wire carries a value from
  source to the destination's slot and then becomes `empty` again.
  The wire owns no parked state, no ack, no take.

## Geometry and time

- Wire geometry sets `in-flight(v)` traversal time:
  `inFlightTime = arcLength / pulseSpeed`. No other substrate effect.
- Geometry change while `in-flight(v)` re-derives remaining traversal
  time from new arc length and distance already covered. If the new
  arc length is below distance covered, the wire completes
  immediately (writes the slot).
- Phase is otherwise ordinal. The substrate tracks no other
  durations.

## Driver

**Self-scheduling nodes + one global play/pause gate.** Nodes poll
their preconditions each RAF frame and fire when they hold; a single
global gate halts or starts wire animations (not nodes). No central
walker. Node poll loops run continuously; when wires are paused,
nodes observe no new input state and produce nothing.

> **Layer boundary.** Node poll loops run in Go. The TS layer (`pump.ts`) is render-only: it consumes trace events emitted by Go and updates React Flow state for animation. No polling or firing logic lives in TS.

There is no global round, tick, or simultaneity layer. The substrate
does not count rounds, observe round-close, or align activity to a
shared clock. Coordination between nodes happens through each source
node's per-output-port send rule (`consumeGated` waits via `WaitConsumed`;
`fireAndForget` is non-blocking and drops the send if the wire is busy),
not through a shared time concept and not
through any backpressure enforced by the wire. Any reasoning that treats activity as a sequence of
globally-aligned rounds is drift; re-derive from local rules over
slots and wires.

## Firing rule and slot writes

**Firing is precondition-gated, observed at RAF cadence.** A node
fires only when its precondition holds — firing is not an arbitrary
scheduled callback or a clock interrupt. The *observation* of
preconditions runs at RAF cadence (each body polls its slot state
each animation frame), but the *firing decision* is still purely
precondition-gated: a node's `run()` is idempotent when the
precondition is unmet and returns immediately. Delivery (wire → slot)
is triggered by animation completion, not by the RAF clock directly.
Every cross-wire hop is gated on the previous wire's delivery; the
cascade is a procession of precondition-gated firings, each paced by
wire geometry. "Atomic cascade" holds only in tests where
`arcLength: 0` collapses visible duration to a single RAF frame (the
event still happens; its visible duration is zero). RAF pacing is the
observation window, not an independent clock competing for authority.

## Editor surface realization

The substrate model lives entirely in Go. The TS/React layer is
render-only: it animates trace events received from Go.

- **Go runtime** owns all slot state, firing rules, wire delivery, and
  backpressure. It emits trace events as JSON lines on stdout.
- **`pump.ts`** (`tools/topology-vscode/src/webview/three/pump.ts`) is the
  sole translator: it reads trace events from the extension bridge and
  updates state stores (pulse-state, three/store) so the 3D renderer can
  animate them. Pump is the boundary — no slot-phase or backpressure logic
  may live outside it on the TS side.
- **`GraphNode`** (in `tools/topology-vscode/src/webview/three/scene-content.tsx`)
  renders all nodes generically as a sphere mesh + border ring, keyed off
  `node.data.fill`/`node.data.stroke` from `NODE_DEFS`. There are no
  per-kind component files.
- **`SingleEdgeTube`** (in `tools/topology-vscode/src/webview/three/ThreeView.tsx`) renders wire
  animations driven by trace events written by the pump. It owns no
  delivery logic.
- **Global gate** is a play/pause signal sent to the Go process.
  While paused, Go stops firing; the editor reflects the last known
  state.
- **Bridge surface** carries spec I/O only — `ready`, `spec`, `view`,
  `save`, `view-safe`. Nothing about
  slot phases, animation internals, or controls crosses the bridge.

## Slot phase lifecycle

One `PacedWire` (`nodes/Wiring/paced_wire.go`) is allocated per destination
input port. All senders converging on that port share the single wire; fan-in
is correct by construction.

**Go operations on `PacedWire`:**

- **`Send`** — blocks while `inFlight` is true (wire occupied by a prior
  bead not yet delivered), then places value into the staging area
  (`pending`), sets `inFlight=true`, and returns. It does not wait for
  Done and does not observe `hasSend`. This is transport occupancy, not a
  send policy: the source node, not the wire, decides whether to wait for
  consumption (see `WaitConsumed` below).
- **`WaitConsumed`** — blocks until the destination calls `Done` on the
  most recent bead, or ctx is canceled. The source node calls this only
  when its per-output-port rule is `consumeGated`; `fireAndForget` skips it
  (it uses `TryPlace`/`TryEmit` and never reaches `WaitConsumed`).
- **`Reset`** — drops any bead in flight and frees a parked sender. Called
  when an edge is deleted so the wire returns to a fresh `inFlight=false`
  state and the source's `Send`/`WaitConsumed` unblocks.
- **`Recv`** — blocks until `slotReadyCh` is closed (i.e. NotifyDelivered
  delivered into the slot), then returns the value. Slot is NOT cleared;
  caller must call Done.
- **`Done`** — receiver signals it has finished with the value. Clears the
  slot (`hasSend=false`), creates a fresh `slotReadyCh` for the next delivery
  cycle, and broadcasts so a waiting `NotifyDelivered` can proceed.

**`NotifyDelivered`** is called by the TS layer (via the extension bridge →
stdin reader) when the pulse animation completes. It waits until the
destination slot is empty (`!hasSend`), moves `pending→slot`, sets
`hasSend=true`, clears `inFlight=false`, and closes `slotReadyCh` to unblock
`Recv`. If the slot is still full, it keeps waiting — this is what keeps
`inFlight=true` and blocks the source. This is the only cross-boundary signal
in the lifecycle.

**Four slot phases and their transition triggers:**

```
[wire empty]  ──Send places bead──▶  [inFlight=true]  ──NotifyDelivered (when slot empty)──▶  [slot filled, inFlight=false]  ──Done──▶  [wire empty]
```

1. `inFlight=false` → `inFlight=true`: `Send` places value into `pending`,
   sets `inFlight=true`. Go emits `{"kind":"slot","phase":"filled"}` via
   Trace/Trace.go (send event triggers the visual pulse animation).
2. Slot `empty` → `filled(v)`: `NotifyDelivered` moves `pending→slot`,
   sets `hasSend=true`, clears `inFlight`, closes `slotReadyCh` to unblock
   `Recv`. Pump posts it from the `"done"` animation callback (see
   `PUMP_DONE_HANDLER` in pump.ts — clears pulse; extension host sends
   `notifyDelivered` to stdin).
3. Recv returns value, slot still `filled(v)` — no phase change. Receiver
   uses the value and calls `Done`.
4. `filled(v)` → `empty`: `Done` clears slot, resets `slotReadyCh`. Go emits
   `{"kind":"slot","phase":"empty"}`.

**Cross-boundary contract:**

- The wire enforces no send policy. `Send` only waits for the wire to be
  clear (`inFlight=false`) before placing the next bead; `inFlight` clears
  when `NotifyDelivered` delivers into an empty slot (deferring through
  `Done` if the slot is full). Whether the source pauses after sending is
  the SOURCE NODE's decision, keyed by outgoing port via
  `node.data.sendRules`: `consumeGated` calls `WaitConsumed` (waits for the
  destination's `Done`); `fireAndForget` is non-blocking — it uses
  `TryPlace`/`TryEmit`, placing the bead only if the wire is free and
  dropping the send otherwise. The default is `consumeGated`. The rule is
  node-owned (per output port), not edge-carried, so it survives edge
  deletion.
- **Edge delete.** The editor posts a `deleteEdge` message
  (target + targetHandle) to the substrate, which calls `Reset()` on that
  wire: it drops the in-flight bead and frees a parked sender so the source
  unblocks. (Known limitation: re-adding an edge sends nothing to the
  substrate today — the live graph is not rebuilt on add.)
- TS never sets slot state directly. It only sends `notifyDelivered` (triggers
  `NotifyDelivered` in Go); it does not render anything from `"slot"` trace events
  (the `"slot"` case is a no-op — see below).
- `pump.ts` `"slot"` case is a no-op `return` — it does not write any state.
  There is no slot badge and no `SlotEntry`/`SlotMap` type. Held-value display
  comes from pulse-state, not from slot events.

**Drift rule:** slot-phase transition logic outside `paced_wire.go` (Go) or
`pump.ts` (TS) is drift — move it to one of those two files.

## Banned vocabulary (in substrate context)

If you find yourself writing or reasoning with these words while
working on the substrate, you have drifted.

Banned-vocabulary check: `bash tools/check-substrate-vocabulary.sh` — substrate code must pass.
Trace-kind parity check: `bash tools/check-trace-kind-parity.sh` — TRACE_EVENT_KINDS and pump.ts switch must match.
Message-kind parity check: `bash tools/check-message-kind-parity.sh` — Go stdin_reader.go discriminators must be present in TS WEBVIEW_TO_HOST_TYPES.
No-TS-timers check: `bash tools/check-no-ts-timers.sh` — setInterval/setTimeout/while must not appear in pump.ts.
Slot-phase boundary check: `bash tools/check-slot-phase-boundary.sh` — slot-phase transition logic must not appear outside pump.ts (TS) or paced_wire.go (Go).

These tokens belong to the renderer, to legacy code being retired, or to
prior substrate models that have been superseded. None of them
describe the current substrate.

## Allowed vocabulary

- empty, in-flight(v), filled(v), consumed
- halt, resume, snap, global gate
- arc length, pulse speed, in-flight traversal time (the one permitted
  duration)
- `fill(slotId, v)`, `slotPhase(slotId)` — the substrate operations
- auto destination, manually-gated destination, destination policy
- node fires, wire delivers, slot fills, slot consumes
- arrives, observes

## Why this file exists

The same model gap has been routed around through 5–7 substrate
rewrites. Each rewrite imported industry-default timing vocabulary
(game loops, schedulers, animation frames) and treated wires as
plumbing. That is the **wrong answer for the substance** of this
project (see CLAUDE.md "Medium vs. substance"). This file pins the
model so a fresh AI session cannot launder it back into a
conventional frame.

If a request seems to require banned vocabulary to fulfill, the
request is in the wrong frame — name the gap explicitly to David
before writing code. Do not substitute a near-miss.
