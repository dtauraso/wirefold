# Model

Read this before changing anything in the **Go network** (`nodes/`,
`nodes/Wiring/paced_wire.go`, `nodes/Wiring/loader.go`,
`nodes/Wiring/builders.go`) or anything that schedules/orders work. If
your reasoning slips into retired vocabulary, you are in the wrong
frame. Stop, re-read this file, and re-derive from the model.

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
- **Clock (the human-speed clock).** There is exactly one clock: the system monotonic clock, read through a **scale** so it advances in integer **ticks** at human-watchable speed (`tick = ⌊(systemNow − start) × scale⌋`; the scale is the human-speed / playback-speed knob). All timing is **tick counts**, not wall-clock durations: goroutines wait on `WaitTick(k)` ("resume when tick ≥ k"). The play/pause gate freezes the tick advance — it stops incrementing the tick, not the underlying system clock. **Everything that animates runs in these ticks:** bead traveling, all in-node animations, and all node/gate processing windows. Per-update tick counts come from formulas, not literals — a bead crossing an edge takes `ticksToCross = arcLength / pulseSpeed` (pulseSpeed in world-units-per-tick, uniform across wires); node processing windows are tick counts. There is no separate render cadence — the tick IS the animation clock.

## Wire lifecycle

A bead crosses a wire in one direction:

1. The source node places a bead on the wire's inbound channel.
2. The wire goroutine reads the bead and times its traversal in ticks:
   `ticksToCross = arcLength / pulseSpeed`.
3. While in flight, the wire advances the bead one position per tick and
   emits its position on the trace stream for the renderer.
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

Nodes time their processing in **ticks**: a firing rule may span a
**tick-count window** (e.g. a gate's inhibit/processing window is K ticks),
scheduled against the human-speed clock via `WaitTick`. Firing is still
gated on held state — now optionally held across a tick window rather than
resolving instantaneously.

## Geometry and time

- Wire geometry sets traversal in ticks:
  `ticksToCross = arcLength / pulseSpeed`. Geometry has no other effect.
- A geometry edit re-derives traversal time. While a bead is in flight,
  the in-flight revision PRESERVES the bead's FRACTIONAL progress `t` (its
  proportion along the wire) — NOT the absolute distance covered. On the
  edit the bead stays at the same fraction `t`, and the remaining ticks are
  recomputed from the NEW arc length at the uniform pulse speed:
  `remainingTicks = (1−t)·newArc/pulseSpeed`. So the bead rides smoothly at the
  same proportion as the wire reshapes (no t-swing race as a node is
  dragged), and a longer or shorter wire still traverses at constant
  world-speed. (Preserving distance instead would let `t` jump as the arc
  length changes.)
- Go owns the bead's PROGRESS (the fraction `t`, timed in ticks on the human-speed clock);
  the editor owns the live node positions during a drag and PLACES the
  bead at `lerp(liveStart, liveEnd, t)` on its local node-port endpoints.
  Go emits `t` on the position trace event for this placement.
- Durations are tick counts: bead traversal (`ticksToCross`) and node processing windows.
## Driver

**Self-scheduling goroutines + one global play/pause gate.** Each node
and wire is a goroutine; they coordinate through channels. A single
global gate halts or resumes wire animation. There is no central walker.

Each wire times its OWN delivery on the human-speed clock: when its
`ticksToCross` have elapsed (via `WaitTick`) the wire puts the bead on the
channel to the destination node. Delivery is not triggered by the renderer — there is no
cross-boundary delivery signal. The editor is told where each bead is;
it is never asked when a bead has arrived.

There is one tick clock (the human-speed clock) but no lockstep round or
simultaneity layer: goroutines schedule independently against the shared
tick via `WaitTick` — they are not aligned into global rounds, and the
network does not count rounds. Coordination between nodes happens through
the values nodes place on wires and the topology — not through
round-alignment or a delivery handshake. Any reasoning that treats
activity as a sequence of globally-aligned lockstep rounds is drift;
re-derive from local rules that wait on ticks over channels and wires.

## Editor surface (TS)

The model lives entirely in Go. The TS/React layer is **render + forward
only**: it decodes the binary content buffer Go streams and draws it, and
forwards raw input to Go. It holds NO domain state — no render stores, no
spec store, no camera store — never sets node state and never tells Go
when a bead has arrived. Go owns the clock.

- **Go runtime** owns all node-local held state, firing rules, wire
  traversal timing, node positions, per-edge curve geometry, shading
  parameters, camera pose, selection, and overlay visibility. It packs
  the whole scene into a **binary content buffer** (`Buffer/`) and streams
  it as length-prefixed frames on fd 3 every change.
- **Go → TS is the binary content buffer** (`buffer-snapshot`) ALONE — no
  sidecar. Each node's kind is a numeric `KindId` column (TS maps it to
  `NODE_DEFS` colors), its label rides the buffer's self-sizing label section,
  and its identity is the buffer ROW INDEX (Go resolves row → node for hits).
  The webview decodes the latest snapshot (`buffer-decode.ts`) and renders it;
  row-keyed reflect resources (`snapshot-buffer.ts`, `overlay-flags.ts`)
  mirror Go — they author nothing. There is **no JSON-trace render path
  and no `pump.ts`**; Go still emits the JSON trace on stdout as the
  `.probe` log source, but the webview does not consume it for rendering.
- **`BufferScene`** (`tools/topology-vscode/src/webview/three/buffer-scene.tsx`)
  draws ALL geometry from the buffer: node bodies (sphere mesh + ring,
  keyed off `node.data.fill`/`node.data.stroke` from `NODE_DEFS`), ports,
  edge tubes, transit + interior beads, selection highlight, and the
  camera (`BufferCamera` maps the buffer Camera row onto the three.js
  camera). It owns no traversal timing, no positions, no geometry.
- **Global gate** is a play/pause signal sent to the Go process (freezes
  the human-speed clock's tick advance). While halted the tick does not
  advance, so beads, in-node animations, and node windows all freeze; the
  editor reflects the last known state.
- **Bridge surface — binary BOTH ways.** **Go → TS:** the binary content
  buffer ALONE (`buffer-snapshot` on fd 3; no sidecar — node id is the row
  index, label rides the buffer's label section, kind is the numeric `KindId`
  column). **TS → Go:** framed binary records on stdin (`[len:u32-LE][record]`,
  symmetric with fd 3) — `raw-input` (raw pointer/wheel + the stateless raycast
  hit as numeric rows; Go's gesture FSM decides what each gesture MEANS), the
  geometry-CRUD `edit` (`op` = create / update / delete — exactly three ops;
  `update` sets a numeric attribute on a typed entity, e.g. overlays toggle/set
  as a flag-id / bitfield), a bare `save` command (Go persists its OWN current
  state — camera + overlays — the editor sends no scene payload), a bare
  fade-toggle command (`f` on the selected element; Go owns the selection and
  the fade fixpoint), and the play/pause control signal. There is NO JSON on
  either wire. The TS → Go send is fire-and-forget: the editor places a record
  and never blocks on Go, never asks when a bead arrived, and there is no
  delivery signal — Go times its own delivery. Nothing about node-local state
  or animation internals crosses the bridge.

## Drift rule

Traversal-timing or firing-rule logic outside the Go node and wire
goroutines is drift — move it back into Go. Likewise any domain state
(node/edge/pulse/geometry/camera/selection) authored on the TS side, or
any TS-side geometry/position/timing computation, is drift: Go owns the
model and streams it as the content buffer; TS decodes and draws.

## Node positions & movement locks (the polar model)

Editor-time node geometry and lock propagation are **pure polar**. The ONLY cartesian value
in the system is the scene sphere's own center (the world anchor).

- **Scene sphere** — a first-class, persisted reference (NOT the derived content-sphere
  centroid, which moves with the nodes and is circular). It has a **cartesian center** (the
  one world anchor, in `scene.json`) and a **radius** that fits the diagram and re-fits on
  pan. It is a SEPARATE entity from the camera pivot: camera **orbit** must not move it;
  camera **pan** does (`PanViewpoint` → `PanSceneSphere`, same delta), holding node world
  positions fixed while their scene polars recompute about the new center.
- **Two polars per node.** (1) **Scene polar** `(r,θ,φ)` about the scene-sphere center — the
  node's POSITION, persisted (`meta.json` `scenePolar*`; cartesian `x/y/z` kept only for
  back-compat, and only used at load when no sphere is persisted yet). World = `sceneCenter +
  polar2cart(scenePolar)`. (2) **Local polar** — the node's stored OFFSET about the local
  node it is doubly-linked to (its lock center). This is the constraint frame.
- **Locks are offsets.** A node-node lock nudges ONE component of a node's **stored local
  polar** offset (a bounded copy of a neighbor's owned component), carried node-to-node in the
  decentralized cascade message (`FromLocalPolar`). The offset is taken from the existing
  movement-link value on first touch (`localPolarOf`) — there is no separate seed step.
- **No blow-up, by construction.** The offset is STORED and only carried through the
  composition or nudged one component — it is NEVER re-derived as `cart2polar(node − center)`
  from a live world during a cascade. That reconstruction against a mid-moving center is the
  bug that made positions fly to infinity. A moved center rigidly translates its satellites
  (offset unchanged ⇒ locks stay satisfied ⇒ the wave terminates). Guard:
  `TestPolarLockNoBlowup`.
- **Panel-authored locks must be structurally incapable of a position blow-up.** If one
  happens, the implementation is wrong (an offset was reconstructed from a moving reference),
  not the locks.

## Allowed vocabulary

- bead, in-flight, held (node-local) state
- channel, input port, output port, fan-in
- arc length, pulse speed (world-units per tick), ticks-to-cross,
  tick-count processing window
- tick, human-speed clock (the one system monotonic clock scaled to ticks
  at human speed), scale, `WaitTick`
- node receives, node holds, node fires, wire advances, wire delivers,
  wire emits position
- halt, resume, global gate
