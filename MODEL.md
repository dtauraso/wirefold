# Substrate model

Read this before changing anything in the **Go substrate** (`nodes/`,
`Wire.go`, `nodes/Wiring/loader.go`, `nodes/Wiring/builders.go`) or the **pump**
(`tools/topology-vscode/src/webview/rf/pump.ts`),
or anything that schedules/orders work. If your
reasoning slips into banned vocabulary (below), you are in the wrong
frame. Stop, re-read this file, and re-derive from the model.

The pivot from earlier substrate versions: a wire no longer owns a
parked slot. The wire is transient — it carries a value to the
destination and becomes empty on arrival. The slot lives on the
destination node in Go. Source nodes observe destination slot phase
directly, not through wire phase.

## What this network computes

The diagram+Go runtime network is a concurrent dataflow system. The
topology IS the logic — behavior emerges from wiring, not procedural
code. Goroutines and channels replace conventional control flow.

Lateral inhibition, contrast detection, and competitive binding are
implemented as circuit primitives.

The visual editor is the medium for authoring and observing the
network; the network itself is the diagram+Go runtime.

## What things are

- **Node.** Owns its firing rule, its output references (each = a
  pointer to a destination node + that destination's slot id), and a
  map of input slots indexed by slot id. Slots are the node's own
  state. A node's identity *is* its firing rule (e.g. ReadGate's
  identity is AND-of-3-slots; XOR's is inequality).
- **Slot.** A per-input cell on a destination node. Phase:
  `empty | filled(v) | consumed`. Phase is ordinal: filled happened,
  then consumed happened. No "during."
- **Wire.** Transient delivery + visual depiction. Phase:
  `empty | in-flight(v) | empty`. The wire carries a value from
  source to the destination's slot and then becomes `empty` again.
  The wire owns no parked state, no ack, no take.

## Who does what

- **Source node loads** by calling `dest.fill(slotId, v)` through its
  output reference. The wire enters `in-flight(v)` and animates. On
  animation completion the wire writes
  `dest.slot[slotId] = filled(v)` and returns to `empty`.
- **Source node observes readiness** by reading
  `dest.slotPhase(slotId)` directly through its output reference. It
  is free to load again when that returns `empty`. No wires involved
  in this observation. Backpressure lives in the slot's empty/filled
  state, observed by the source.
- **Destination node consumes** its own slot by transitioning
  `filled(v) → consumed → empty`. Consumption is local: the firing
  rule reads the node's own slot map, decides to fire, transitions
  its slots, and calls `fill` on its own outputs.
- **Auto vs. manually-gated destinations.** An auto destination's
  firing rule fires the moment its precondition holds (e.g. all
  slots filled). A manually-gated destination's firing rule
  additionally waits for a user click. The slot stays `filled(v)`
  until the rule fires.

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

There is no global round, tick, or simultaneity layer. The substrate
does not count rounds, observe round-close, or align activity to a
shared clock. Coordination between nodes happens through destination
slot phases, read directly by source nodes — never through a shared
time concept. Any reasoning that treats activity as a sequence of
globally-aligned rounds is drift; re-derive from local rules over
slots and wires.

## Firing rule and slot writes

A wire's destination is `(node N, slot s_k)`, established at
construction time. On arrival, the wire carries its bound slot id;
the destination node sees the id and writes `slots[s_k] := filled(v)`
and re-evaluates its rule over the slot map — see
[diagrams/firing-rule-and-slot-ownership.svg](diagrams/firing-rule-and-slot-ownership.svg).
One incoming wire per slot id — two wires cannot share a slot, so
"right slot ↔ right wire" is deterministic by construction.
Mis-wiring is caught at parseSpec, not at runtime. No subscription
layer; slots are passive state.

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

> **Output-readiness precondition.** Before consuming any slot, a node
> body must verify two conditions locally: (a) every input slot it
> intends to consume is in `filled` phase, and (b) every destination
> wire it intends to load reports `canAccept === true`. Both checks
> are read-only observations of local state — no signal crosses to
> another node. If either condition is unmet, the body returns
> without consuming anything and re-observes on the next poll frame.
> A body that consumes a slot before verifying `canAccept` loses the
> value silently when the wire no-ops the load; this is a contract
> violation, not a retry. The precondition is all-or-nothing: partial
> consumption is not permitted.

## Editor surface realization

The substrate model lives entirely in Go. The TS/React layer is
render-only: it animates trace events received from Go.

- **Go runtime** owns all slot state, firing rules, wire delivery, and
  backpressure. It emits trace events as JSON lines on stdout.
- **`pump.ts`** (`tools/topology-vscode/src/webview/rf/pump.ts`) is the
  sole translator: it reads trace events from the extension bridge and
  updates React Flow node/edge data so components can animate them.
  Pump is the boundary — no slot-phase or backpressure logic may live
  outside it on the TS side.
- **Per-kind node components** (`rf/nodes/<Kind>Node.tsx`) render the
  current RF node data as static React Flow custom nodes. They read
  phase indicators from the data the pump has written; they own no
  substrate state.
- **`SubstrateEdge.tsx`** (`rf/edges/SubstrateEdge.tsx`) renders wire
  animations driven by trace events written by the pump. It owns no
  delivery logic.
- **Global gate** is a play/pause signal sent to the Go process.
  While paused, Go stops firing; the editor reflects the last known
  state.
- **Bridge surface** carries spec I/O only — `ready`, `spec`, `view`,
  `save`, `view-safe`. Nothing about
  slot phases, animation internals, or controls crosses the bridge.

## Banned vocabulary (in substrate context)

If you find yourself writing or reasoning with these words while
working on the substrate, you have drifted:

- duration, ms, milliseconds, seconds — **except** the one allowed
  duration: a wire's `in-flight` traversal time derived from geometry.
- speed, px/ms, pixel rate — **except** the single global pulse speed
  constant used to derive `in-flight` traversal time.
- schedule, scheduler, deadline, timeout, setTimeout, setInterval
- wall-clock, Date.now, performance.now (for substrate scheduling;
  the renderer may still use `performance.now` for animation)
- tick, round, step, round-close, tick-close, simultaneity layer,
  lap, cohort, cohort index — any framing that treats activity as a
  global sequence of rounds or counts edges across nodes. There is
  no global round in this substrate; coordination is local via slot
  phases.
- "wire parks the value permanently" — the wire is transient; parked
  state lives on the destination node's slot. (Exception: the
  deferred-deliver safety net holds the value in-flight past animation
  end only until the destination slot empties — delivery completes on
  the next RAF step that finds the slot empty, then the wire returns
  to `empty`. This is bounded transient deferral, not parking.)
- `loaded(v)`, `taken(v)`, wire `ack`, wire `take` — replaced by slot
  phases (`filled(v)`, `consumed`) on the destination node
- inbox queue, edge queue, slot ledger, buffered values
- central walker, setup pass, separate budget counter

These belong to the renderer, to legacy code being retired, or to
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
