# Model

Read this before changing anything in the **Go network** (`nodes/`,
`nodes/Wiring/paced_wire.go`, `nodes/Wiring/loader.go`,
`nodes/Wiring/builders.go`) or anything that schedules/orders work. If
your reasoning slips into retired vocabulary, you are in the wrong
frame. Stop, re-read this file, and re-derive from the model.

## The network

The network is **nodes and wires**. Each node runs on its own Go
goroutine. A wire (`PacedWire`) is NOT a goroutine or a channel — it is
a passive, mutex-guarded struct (`inflight`/`delivered` bead slices)
that the owning node's goroutine STEPS via `StepOnce`/`StepOnceAt`. The
network is self-scheduling: there is no central runner, no walker, no
underlying layer that "runs" the nodes. The network IS the running
program.

Behavior emerges from wiring — the topology is the logic.

The visual editor is the medium for authoring and observing the network;
the network itself is the nodes-and-wires Go runtime.

## What things are

- **Bead.** A value in transit from a source node to a destination node.
- **Wire (`PacedWire`).** Transport plus visual depiction. A passive
  mutex-guarded struct, not a goroutine or channel: the source node calls
  `placeBeadNoWalkerAt` directly to place a bead, the owning node's
  `StepOnce`/`StepOnceAt` times the traversal on Go's clock and moves the
  bead from `inflight` to `delivered` via `tryDeliverHeadLocked`, and the
  destination node consumes it with `In.PollRecv`. The wire owns no
  parked state and applies no send policy.
- **Node goroutine.** Consumes beads via `In.PollRecv` on its input
  port(s), holds them in node-local state until its firing rule is
  satisfied, then fires. There is no slot — node-local held state
  replaces it.
- **Input port.** One input port is one `PacedWire`, read via
  `In.PollRecv`. There is no channel between wire and node.
- **Clock (the human-speed clock).** There is exactly one clock: the system monotonic clock, read through a **scale** so it advances in integer **ticks** at human-watchable speed (`tick = ⌊(now − start) / tickPeriod⌋`; the scale is the human-speed / playback-speed knob, `MsPerTick = 16` ⇒ ≈62.5 ticks/sec). All timing is **tick counts**, not wall-clock durations. The model is **sleep-only**: a pacing loop calls `SleepCycle` to wait exactly ONE cycle and re-reads `Tick()`, rather than blocking on a target tick — there is no wait-until-tick-k primitive. The clock is **free-running**: it advances monotonically with wall time and never pauses (there is no play/pause gate). **Everything that animates runs in these ticks:** bead traveling, all in-node animations, and all node/gate processing windows. Per-update tick counts come from formulas, not literals — a bead crossing an edge takes `ticksToCross = arcLength / pulseSpeed` (pulseSpeed in world-units-per-tick, uniform across wires); node processing windows are tick counts. There is no separate render cadence — the tick IS the animation clock.

## Wire lifecycle

A bead crosses a wire in one direction:

1. The source node calls `placeBeadNoWalkerAt`, appending the bead to the
   wire's `inflight` slice with its traversal timed in ticks:
   `ticksToCross = arcLength / pulseSpeed`.
2. While in flight, the owning node's `StepOnce`/`StepOnceAt` advances
   the bead one position per tick and emits its position on the trace
   stream for the renderer.
3. On traversal-complete, `tryDeliverHeadLocked` moves the bead from
   `inflight` to `delivered`.
4. The destination node consumes it by calling `In.PollRecv`, which pops
   the delivered bead.

Go times its own delivery. There is no TS-driven delivery signal — the
renderer is told where the bead is, not asked when it has arrived.

## Node lifecycle and fan-in

- A destination node consumes beads via `In.PollRecv` on its input
  port(s) and holds them in node-local state.
- When the node's firing rule is satisfied, it fires.
- **Fan-in:** several beads can be in flight in one `PacedWire`'s
  `inflight` slice at once, each carrying its own placement geometry —
  geometry travels WITH the bead, never stored on the shared wire, so
  fan-in is safe. The node reads that one wire via `In.PollRecv` and
  accumulates received beads in node-local state. Distinct inputs are
  distinct wires.

## Sending

A node places a bead on its outgoing wire whenever its own rule says to. It does not check the wire's state and does not wait on the destination — there is no clear/busy state, no acknowledgment, no back-pressure. A wire may carry more than one bead at once, each its own value; it transports whatever the source emits, and the destination reads whatever arrives. Coordination between nodes is the topology and each node's local rule, not a delivery guarantee between the two.

Nodes time their processing in **ticks**: a firing rule may span a
**tick-count window** (e.g. a gate's inhibit/processing window is K ticks),
paced against the human-speed clock by sleeping one cycle at a time
(`SleepCycle`) and comparing `Tick()`. Firing is still
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
- Go owns the bead's PROGRESS (the fraction `t`, timed in ticks on the human-speed clock)
  AND the bead's absolute world position — it computes the position from its own
  live node/port endpoints (moved by the same drag) and packs the result into the
  content buffer. The editor decodes and draws it (`readBeadX/Y/Z` from
  `tools/topology-vscode/src/schema/buffer-layout.ts`, consumed by `tools/topology-vscode/src/webview/three/BeadInstances.tsx`); it does not
  interpolate or own positions.
- Durations are tick counts: bead traversal (`ticksToCross`) and node processing windows.
## Driver

**Self-scheduling node goroutines.** Each node is a goroutine; a wire is
a passive struct the owning node's goroutine steps. There is no central
walker, and no play/pause gate — the clock is free-running and the
animation never halts.

Each wire times its OWN delivery on the human-speed clock: when its
`ticksToCross` have elapsed (observed by the owning node's loop driving
`StepOnce` one cycle at a time) `tryDeliverHeadLocked` moves the bead
from `inflight` to `delivered`, and the destination node picks it up via
`In.PollRecv`. Delivery is not triggered by the renderer — there is no
cross-boundary delivery signal. The editor is told where each bead is;
it is never asked when a bead has arrived.

There is one tick clock (the human-speed clock) but no lockstep round or
simultaneity layer: goroutines schedule independently against the shared
tick, each sleeping a cycle (`SleepCycle`) and re-reading `Tick()` on its
own — they are not aligned into global rounds, and the
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
  and no `pump.ts`**; Go emits no trace-event JSON on stdout at all — the
  `.probe` trace log (`go.jsonl`) is now the ext host's DECODE of the fd3
  binary content buffer's EVENT block (`buffer-log.ts`), not a stdout
  parse. Stdout carries only the DEBUG BREADCRUMB channel's sparse
  `{"kind":"breadcrumb",...}` control-event lines.
- **`BufferScene`** (`tools/topology-vscode/src/webview/three/buffer-scene.tsx`)
  is the composition root of the render tree — it decodes the buffer and
  assembles the per-concern components that draw ALL geometry from it. It is a
  small file; the drawing lives in its siblings under `three/`. Grep the symbol,
  not this filename. The tree covers: node bodies (`tools/topology-vscode/src/webview/three/NodeInstances.tsx` — sphere
  mesh + ring, keyed off `node.data.fill`/`node.data.stroke` from `NODE_DEFS`),
  ports (`tools/topology-vscode/src/webview/three/PortInstances.tsx`), edge tubes (`tools/topology-vscode/src/webview/three/EdgeTube.tsx`), transit and interior
  beads (`tools/topology-vscode/src/webview/three/BeadInstances.tsx`, `tools/topology-vscode/src/webview/three/InteriorBeadInstances.tsx`), selection highlight
  (`tools/topology-vscode/src/webview/three/SelectionHighlight.tsx`), and the camera (`tools/topology-vscode/src/webview/three/BufferCamera.tsx` maps the buffer
  Camera row onto the three.js camera). Nothing in this tree owns traversal
  timing, positions, or geometry.
- **Bridge surface — binary BOTH ways.** **Go → TS:** the binary content
  buffer ALONE (`buffer-snapshot` on fd 3) — stated in full under "Go → TS is
  the binary content buffer" above; not restated here, so the two copies cannot
  drift apart. **TS → Go:** framed binary records on stdin (`[len:u32-LE][record]`,
  symmetric with fd 3) — `raw-input` (raw pointer/wheel + the stateless raycast
  hit as numeric rows; Go's gesture FSM decides what each gesture MEANS), the
  geometry-CRUD `edit` (`op` = update — the sole remaining op; a `create` /
  `delete` op pair was removed end-to-end, no live TS sender ever emitted them.
  `update` sets a numeric attribute on a typed entity, e.g. overlays toggle/set
  as a flag-id / bitfield), a bare `save` command (Go persists its OWN current
  state — camera + overlays — the editor sends no scene payload). There is NO JSON on
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

Editor-time node geometry and lock propagation are **pure polar**. The scene sphere's center
is the only cartesian value that is **persisted and authoritative** — the world anchor. It is
not the only cartesian value that exists: the camera pose, port anchors, bead segments and
per-node world centers are cartesian too. The invariant is narrower and stronger than
"cartesian appears once": every other cartesian is **derived** from this anchor
(`sceneCenter + polar2cart(…)`) or **quarantined at the renderer edge** — none is persisted,
and none is a source of truth.

- **Scene sphere** — a first-class, persisted reference (NOT the derived content-sphere
  centroid, which moves with the nodes and is circular). It has a **cartesian center** (the
  one world anchor, in `scene.json`) and a **radius** that fits the diagram.

  **It is established once and never moves.** `LoadSceneSphere` reads it from `scene.json`,
  or content-fits it from the node centers and persists that immediately — persisting matters,
  because every node position is a polar measured ABOUT this center, so a center re-fitted
  over moved nodes on the next load would silently reinterpret the whole diagram.

  It is a SEPARATE entity from the camera pivot, and **both camera gestures leave it alone**:
  orbit must not move it, and `PanViewpoint` is a pure CAMERA move that deliberately does not
  touch it.

  > **Rejected: pan moves the sphere.** The model once said camera pan should translate the
  > center by the same delta, holding node world positions fixed while their scene polars
  > recomputed about the new center. Coupling pan/dolly to the sphere left `md.sceneSphere`
  > diverged from the movers' held center until a later broadcast reconciled it with a jump —
  > the "zoom got canceled" symptom. It was decoupled, a separate scene-pan gesture was named
  > as the proper home, and that gesture was never built. The claim is now DROPPED rather than
  > pending: the sphere is a load-time-fitted constant.
  >
  > The cost, stated so it is a choice and not an accident: the polar frame is best
  > conditioned near the center it was fitted to. Pan the camera far away and drag there, and
  > you work at large `r`, where a small angular step moves a node a long way. If that becomes
  > real friction in the editor, THAT is the reason to revisit — and the trap to avoid is the
  > one above: a scene pan is its own gesture, never a side effect of a camera move.
- **Two polars per node.** (1) **Scene polar** `(r,θ,φ)` about the scene-sphere center — the
  node's POSITION, persisted (`meta.json` `scenePolar*`; cartesian `x/y/z` kept only for
  back-compat, and only used at load when no sphere is persisted yet). World = `sceneCenter +
  polar2cart(scenePolar)`. (2) **Local polar** — the node's stored OFFSET about the local
  node it is doubly-linked to (its lock center). This is the constraint frame.
- **Locks are offsets.** A node-node lock nudges ONE component of a node's **stored local
  polar** offset (a bounded copy of a neighbor's owned component), carried node-to-node in the
  decentralized cascade message (`sendMove`, node-to-node over per-node inboxes — there is no
  central worklist). The offset lives on the node's `LayoutHolder` (`SetLocalPolar` /
  `LocalPolarsSnapshot`), seeded from the existing movement-link value by
  `computeLocalPolars` at load and re-quantized on move by `requantizeLocalPolars` — there is
  no separate seed step. Each mover enqueues onto its OWN unbounded `outbox`
  (`md.enqueueFor(nm.outbox)`), drained by a dedicated sender goroutine (`outbox.run`); the
  cascade never drops a message. (An earlier direct-to-inbox lossy sender dropped ~98% of
  sends under load and was removed.)
- **No blow-up, by construction.** The offset is STORED and only carried through the
  composition or nudged one component — it is NEVER re-derived as `cart2polar(node − center)`
  from a live world during a cascade. That reconstruction against a mid-moving center is the
  bug that made positions fly to infinity. A moved center rigidly translates its satellites
  (offset unchanged ⇒ locks stay satisfied ⇒ the wave terminates). This is STRUCTURAL, not a
  test: the reconstruction that caused the blow-up has no call site to write. Nav is held
  polar-only by `tools/check-polar-only-nav.sh`.
- **Panel-authored locks must be structurally incapable of a position blow-up.** If one
  happens, the implementation is wrong (an offset was reconstructed from a moving reference),
  not the locks.

## Allowed vocabulary

- bead, in-flight, held (node-local) state
- channel, input port, output port, fan-in
- arc length, pulse speed (world-units per tick), ticks-to-cross,
  tick-count processing window
- tick, human-speed clock (the one system monotonic clock scaled to ticks
  at human speed), scale, `SleepCycle`, `Tick`
- node receives, node holds, node fires, wire advances, wire delivers,
  wire emits position
