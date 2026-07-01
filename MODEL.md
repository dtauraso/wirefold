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

Behavior emerges from wiring — the topology is the logic.

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
- **Clock.** The system monotonic clock Go reads — there is exactly one clock. All other timing is arithmetic in code on its readings: each wire converts clock deltas into bead advancement (`distanceCovered += pulseSpeed × Δ`), scaling traversal to human-visible speed; `inFlightTime = arcLength / pulseSpeed` is derived, not a second timer. The play/pause gate stops the arithmetic, not the clock. Nodes do not read the clock — firing is predicate-gated on held state. The ~16 ms position emit is a render cadence, not a clock.

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

## Sending

A node places a bead on its outgoing wire whenever its own rule says to. It does not check the wire's state and does not wait on the destination — there is no clear/busy state, no acknowledgment, no back-pressure. A wire may carry more than one bead at once, each its own value; it transports whatever the source emits, and the destination reads whatever arrives. Coordination between nodes is the topology and each node's local rule, not a delivery guarantee between the two.

> **Needs confirmation.** How a node times a firing window (whether a
> firing rule may span a duration, or fires purely on held-state
> predicate) is not yet specified. The model above treats firing as
> predicate-gated on held state with no timed window.

## Geometry and time

- Wire geometry sets in-flight traversal time:
  `inFlightTime = arcLength / pulseSpeed`. Geometry has no other effect.
- A geometry edit re-derives traversal time. While a bead is in flight,
  the in-flight revision PRESERVES the bead's FRACTIONAL progress `t` (its
  proportion along the wire) — NOT the absolute distance covered. On the
  edit the bead stays at the same fraction `t`, and the remaining time is
  recomputed from the NEW arc length at the uniform pulse speed:
  `remaining = (1−t)·newArc/pulseSpeed`. So the bead rides smoothly at the
  same proportion as the wire reshapes (no t-swing race as a node is
  dragged), and a longer or shorter wire still traverses at constant
  world-speed. (Preserving distance instead would let `t` jump as the arc
  length changes.)
- Go owns the bead's PROGRESS (the fraction `t`, timed on the one clock);
  the editor owns the live node positions during a drag and PLACES the
  bead at `lerp(liveStart, liveEnd, t)` on its local node-port endpoints.
  Go emits `t` on the position trace event for this placement.
- Traversal time is the only duration the network tracks.
## Driver

**Self-scheduling goroutines + one global play/pause gate.** Each node
and wire is a goroutine; they coordinate through channels. A single
global gate halts or resumes wire animation. There is no central walker.

Each wire times its OWN delivery on the one clock: when `inFlightTime`
has elapsed the wire puts the bead on the channel to the destination
node. Delivery is not triggered by the renderer — there is no
cross-boundary delivery signal. The editor is told where each bead is;
it is never asked when a bead has arrived.

There is no global round, tick, or simultaneity layer. The network does
not count rounds or align activity to a shared clock. Coordination
between nodes happens through the values nodes place on wires and the
topology — not through a shared time concept or a delivery handshake. Any
reasoning that treats activity as a sequence of globally-aligned rounds
is drift; re-derive from local rules over channels and wires.

## Editor surface (TS)

The model lives entirely in Go. The TS/React layer is **render-only**:
it receives bead positions from Go and draws them. It never sets node
state and never tells Go when a bead has arrived — Go owns the clock.

- **Go runtime** owns all node-local held state, firing rules, wire
  traversal timing, node positions, per-edge curve geometry, and shading
  parameters. It emits trace events — bead positions, node events, edge
  curves, and shading params — as JSON lines on stdout.
- Each node's goroutine emits its own node + port world positions/dirs on
  startup as the `node-geometry` trace event (the node owns its geometry
  emission; wires still own bead-position emission).
- **`pump.ts`** (`tools/topology-vscode/src/webview/three/pump.ts`) is a
  position-stream plotter, not a delivery driver: it reads Go's trace
  events from the extension bridge and writes them into state stores
  (pulse-state, three/store) so the 3D renderer can draw what Go is
  doing. It computes no positions, no geometry, and no traversal timing.
  Pump is the boundary — no firing-rule or timing logic may live outside
  it on the TS side.
- **`GraphNode`** (in
  `tools/topology-vscode/src/webview/three/scene-graph.tsx`) renders
  all nodes generically as a sphere mesh + border ring, keyed off
  `node.data.fill`/`node.data.stroke` from `NODE_DEFS`. There are no
  per-kind component files.
- **`SingleEdgeTube`** (in
  `tools/topology-vscode/src/webview/three/scene-graph.tsx`) renders wire
  animation driven by the positions Go emits. It owns no traversal
  timing.
- **Global gate** is a play/pause signal sent to the Go process (the one
  clock's halt/resume). While halted, Go stops advancing beads and
  firing; the editor reflects the last known state.
- **Bridge surface** is two channels and nothing else. **Go → TS:** the
  trace stream (bead positions, node events, edge curves, shading
  params) — Go reporting what it is doing. **TS → Go:** spec I/O for
  save/load (`save`, `view-save`, `load`) plus a single geometry-CRUD
  `edit` message (`op` = create / update / delete — exactly three ops;
  fading is not an op but an edge attribute set via `op=update, kind=edge,
  attr=faded`) and the
  play/pause control signal. The TS → Go send is fire-and-forget: the
  editor places an edit and never blocks on Go, never asks when a bead
  arrived, and there is no delivery signal — Go times its own delivery.
  Nothing about node-local state or animation internals crosses the
  bridge.

## Drift rule

Traversal-timing or firing-rule logic outside the Go node
and wire goroutines (or `pump.ts` for render translation) is drift —
move it back into Go.

## Allowed vocabulary

- bead, in-flight, held (node-local) state
- channel, input port, output port, fan-in
- arc length, pulse speed, in-flight traversal time (the one permitted
  duration)
- clock (the one system monotonic clock Go reads)
- node receives, node holds, node fires, wire advances, wire delivers,
  wire emits position
- halt, resume, global gate
