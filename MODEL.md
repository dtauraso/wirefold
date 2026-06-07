# Model

Read this before changing anything in the **Go network** (`nodes/`,
`nodes/Wiring/paced_wire.go`, `nodes/Wiring/loader.go`,
`nodes/Wiring/builders.go`) or the **pump**
(`tools/topology-vscode/src/webview/three/pump.ts`), or anything that
schedules/orders work. If your reasoning slips into retired vocabulary,
you are in the wrong frame. Stop, re-read this file, and re-derive from
the model.

## The network

The network is **nodes and wires**. Each node and each wire is a Go
goroutine. They are connected by Go channels. The network is
self-scheduling: there is no central runner, no walker, no underlying
layer that "runs" the nodes. The network IS the running program.

Behavior emerges from wiring — the topology is the logic. Lateral
inhibition, contrast detection, and competitive binding are implemented
as circuit primitives.

The visual editor is the medium for authoring and observing the network;
the network itself is the nodes-and-wires Go runtime.

## What things are

- **Bead.** A value in transit from a source node to a destination node.
- **Wire (`PacedWire` goroutine).** Transport plus visual depiction. A
  wire polls its inbound channel for a bead the source placed, times the
  traversal on Go's clock, advances the bead, emits the bead's position
  for the renderer, and on traversal-complete puts the bead on the
  channel to the destination node. The wire owns no parked state and
  applies no send policy.
- **Node goroutine.** Receives beads off its input channel(s), holds
  them in node-local state until its firing rule is satisfied, then
  fires. There is no slot — node-local held state replaces it.
- **Channel.** A Go channel connecting a source to a wire, or a wire to a
  destination node. One input port is one channel.
- **Clock.** Go's monotonic runtime time filtered through the global
  play/pause gate — sim-time is wall time that elapses while the gate is
  open. Not a component: no clock goroutine, no tick channel, no round
  counter. Each wire goroutine reads it locally to elapse
  `inFlightTime = arcLength / pulseSpeed` and advance distance-covered;
  distance-covered is the durable state, and remaining time is re-derived
  from it. Nodes do not read the clock — firing is predicate-gated on held
  state (see the firing-window "Needs confirmation"). The ~16 ms position
  emit is a render cadence, not the clock.

## Wire lifecycle

A bead crosses a wire in one direction:

1. The source node places a bead on the wire's inbound channel.
2. The wire goroutine reads the bead and times its traversal on Go's
   clock: `inFlightTime = arcLength / pulseSpeed`.
3. While in flight, the wire advances the bead and emits its position
   (~16 ms cadence) on the trace stream for the renderer.
4. On traversal-complete, the wire puts the bead on the channel to the
   destination node.

Go times its own delivery. There is no TS-driven delivery signal — the
renderer is told where the bead is, not asked when it has arrived.

## Node lifecycle and fan-in

- A destination node receives beads off its input channel(s) and holds
  them in node-local state.
- When the node's firing rule is satisfied, it fires.
- **Fan-in:** several wires send to one input-port channel; the node
  reads that one channel and accumulates received beads in node-local
  state. Distinct inputs are distinct channels.

## Send gating (ack wire)

A source places a bead on a forward wire only when the matching **ack
wire** has delivered an "ok". An "ok" is itself a backward bead — a
consume-ack — that travels its own ack wire from the destination back to
the source. A **seed** grants the first ok so the source can start.

Two send rules exist, node-owned, per output port, at
`node.data.sendRules` (a map: output-port-name → rule):

- **`consumeGated`** (default) — after sending, the source waits for the
  ack wire's ok before sending the next bead.
- **`fireAndForget`** — the source sends without waiting; if it cannot
  place the bead, it drops the send and moves on.

The rule lives on the source node, keyed by outgoing port name. The wire
carries no rule. One node may use different rules on different outgoing
ports, and the rule survives edge deletion because it lives on the node,
not the edge.

> **Needs confirmation.** The exact ack-bead protocol (the "ok" payload,
> whether one ack wire pairs one forward wire, how the ack wire is
> declared in the topology) and the seed/bootstrap mechanism (where the
> first ok originates, its shape in `node.data`) are stated here at the
> level the new model fixes; the wire-level details are not yet pinned in
> code.

> **Needs confirmation.** How a node times a firing window (whether a
> firing rule may span a duration, or fires purely on held-state
> predicate) is not yet specified. The model above treats firing as
> predicate-gated on held state with no timed window.

## Geometry and time

- Wire geometry sets in-flight traversal time:
  `inFlightTime = arcLength / pulseSpeed`. Geometry has no other effect.
- A geometry edit re-derives traversal time. While a bead is in flight,
  the new arc length and the distance already covered give the remaining
  time. If the new arc length is below the distance covered, the
  traversal completes immediately.
- Traversal time is the only duration the network tracks.

## Driver

**Self-scheduling goroutines + one global play/pause gate.** Each node
and wire is a goroutine; they coordinate through channels. A single
global gate halts or resumes wire animation. There is no central walker.

There is no global round, tick, or simultaneity layer. The network does
not count rounds or align activity to a shared clock. Coordination
between nodes happens through each source node's per-output-port send
rule and the ack wires — not through a shared time concept. Any
reasoning that treats activity as a sequence of globally-aligned rounds
is drift; re-derive from local rules over channels and wires.

## Editor surface (TS)

The model lives entirely in Go. The TS/React layer is **render-only**:
it receives bead positions from Go and draws them. It never sets node
state and never tells Go when a bead has arrived — Go owns the clock.

- **Go runtime** owns all node-local held state, firing rules, wire
  traversal timing, and send gating. It emits trace events (bead
  positions and node events) as JSON lines on stdout.
- **`pump.ts`** (`tools/topology-vscode/src/webview/three/pump.ts`) is
  the sole translator: it reads trace events from the extension bridge
  and updates state stores (pulse-state, three/store) so the 3D renderer
  can animate them. Pump is the boundary — no firing-rule or send-gating
  logic may live outside it on the TS side.
- **`GraphNode`** (in
  `tools/topology-vscode/src/webview/three/scene-content.tsx`) renders
  all nodes generically as a sphere mesh + border ring, keyed off
  `node.data.fill`/`node.data.stroke` from `NODE_DEFS`. There are no
  per-kind component files.
- **`SingleEdgeTube`** (in
  `tools/topology-vscode/src/webview/three/ThreeView.tsx`) renders wire
  animation driven by the positions Go emits. It owns no traversal
  timing.
- **Global gate** is a play/pause signal sent to the Go process. While
  paused, Go stops advancing beads and firing; the editor reflects the
  last known state.
- **Bridge surface** carries spec I/O only — `ready`, `spec`, `view`,
  `save`, `view-safe`. Nothing about node state or animation internals
  crosses the bridge.

> **Needs confirmation.** With Go owning delivery, the previous
> `notifyDelivered` stdin message no longer drives delivery. Whether it
> is removed outright or repurposed is a code change not yet made; this
> doc describes the target (Go-timed delivery, no TS delivery signal).

## Drift rule

Send-gating, traversal-timing, or firing-rule logic outside the Go node
and wire goroutines (or `pump.ts` for render translation) is drift —
move it back into Go.

## Allowed vocabulary

- bead, in-flight, held (node-local) state
- channel, input port, output port, fan-in
- ack wire, ok, consume-ack, seed
- send rule, `consumeGated`, `fireAndForget`, `node.data.sendRules`
- arc length, pulse speed, in-flight traversal time (the one permitted
  duration)
- clock, sim-time (monotonic time while playing)
- node receives, node holds, node fires, wire advances, wire delivers,
  wire emits position
- halt, resume, global gate
