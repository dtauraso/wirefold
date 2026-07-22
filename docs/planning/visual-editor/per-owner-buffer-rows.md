---
branch: task/per-owner-buffer-rows
---

# Per-owner buffer rows — retire the single accumulator

## Premise

Every disagreement this design introduces is **about one tick** — and a tick is
`MsPerTick = 16`, a defined constant, 62.5 ticks/sec. A display frame is 1/60 s. They are
close but not the same unit, and neither should be written as a rounded decimal: the
natural unit here is TICKS and FRAMES, not milliseconds.

So the honest statement is: every disagreement is on the order of one tick, and the only
reader is an eye sampling at one frame. Nothing that reads the frame can resolve that. The
single accumulator is not being kept for correctness; it is being kept for an exactness
nobody can observe.

The one place a tick of skew converts into something visible is **relative displacement
during fast motion** — a node tracking the pointer covers real pixels in a tick, so a gap
between an edge endpoint and its node would be seen. That is a PIXEL budget, not a timing
one, and it is the single constraint this plan has to respect. State it in world units,
not in time.

## The observation this rests on

`SnapshotState` reconstructs state from events **that the owner already had**. Node 3's
mover knows node 3's position; it emits an event; the accumulator receives it and writes
it into node row 3. The accumulator is a middleman reassembling something never actually
in pieces.

And the buffer layout is **already row-per-owner**:

    Node / Interior / Port   -> that node's mover
    Edge                     -> that edge's mover
    Bead                     -> the wire (stepped by its source node's goroutine)
    Camera / Overlay / Scene -> the gesture/stdin goroutine
    Label / PortName / EdgeLabel -> static after load
    Event                    -> genuinely a log; .probe only

So the fan-in is not structural. It is an artifact of routing state through an event
stream that then has to be reassembled.

## The change

**Each owner publishes its own row. Nothing accumulates.**

Every owner holds an immutable row value and publishes it through `atomic.Pointer` — the
pattern already used by `nodeMover.snap` and the three row tables. A packer reads the
published pointers and writes bytes.

- No shared mutable state, so no lock.
- No merge step, because there is nothing to merge — each row is already whole.
- No tearing: a reader loads a pointer to an immutable value; a writer publishes a new one.

Rows will be from slightly different instants. That is the accepted premise.

## What this deletes

- `Trace.mu` outright — state AND events. No shared accumulator remains.
- `SnapshotState`'s accumulate-then-pack role. `Buffer/pack.go` survives; the ingest half
  in `snapshot.go` largely does not.
- The `on*` event handlers that exist solely to write state the owner already had.

## The Event block too — NO EXCEPTIONS

An earlier draft kept `Event` as a genuine fan-in on the grounds that a log is inherently
an accumulation. It is not, and the exception is not needed.

Each owner publishes its own recent events as an immutable slice alongside its row, and
the packer concatenates them in row order — the same shape as beads. No accumulator
anywhere, and `Trace.mu` is deleted outright rather than partially.

What is given up is a global total order across different owners' events. **That ordering
is already a fiction.** Today's log is in ARRIVAL ORDER AT THE DRAIN — channel delivery
order, decided by the scheduler — not causal order. Two goroutines' events appear in
whatever sequence they happened to be received. So per-owner streams lose no truth; they
stop asserting a global sequence that was never real.

Each owner's own events stay in that owner's order, which is the only ordering that was
ever meaningful, and the only one a reader can act on.

## Open questions — settle before writing code

1. **Who decides when to pack a frame? — DECIDED: N per-block buffers, no global packer.**
   Each block (Node, Edge, Bead, …) is its own binary content buffer, streamed change-driven
   by its owner(s): the Bead buffer streams every tick because beads churn; the Node buffer
   streams only on a move. This restores "Go emits only when something changes" per buffer
   without a shared frame trigger or a dirty-flag over one, and removes the single-frame
   packer. (Earlier drafts weighed a periodic packer vs a dirty-flag; both are moot — there
   is no single frame to trigger.)

   Consequence still OPEN — the tear: with per-block buffers, the Edge buffer can arrive a
   tick behind the Node buffer, and if the Edge block keeps shipping copied `SX..EZ`
   endpoints, that stale-copy gap is a visible ~32px separation during a fast drag. Two ways
   to close it: (a) edge stores no endpoint and references the node/port row
   (tear unrepresentable, but hinges on no endpoint ever moving independent of a node); or
   (b) edge keeps its copy but re-quantizes in the node's tick via the existing one-hop
   `neighborSetC` mechanism, so its buffer is never a stale tick behind.

   **DECIDED: (a) — edge references the PORT row; no stored endpoint.** A code trace
   (`node_mover.go`, `Buffer/pack.go`, `Buffer/snapshot.go`) established the facts:
   - Today the edge endpoint (`SX..EZ`) is a *stored copy* set in `onEdgeGeometry`
     (`snapshot.go:636-637`) from an edge-geometry event the **edgeMover's own goroutine**
     emits (`node_mover.go:580-582`), one async channel hop AFTER the node move
     (`quantized_move.go:71-83` enqueues to the edge non-blocking). So a lag already exists;
     what hides it is that one drain goroutine packs Node+Edge into one frame and ships them
     together (`snapshot.go:362-389`). The split removes that "shipped-together" coupling and
     turns the transient lag into a persistent cross-buffer skew — the tear is real.
   - Option (b) is mis-worded against the code: `neighborSetC` is the *other node's*
     distance/angle re-quantize, NOT what refreshes the dragged edge's endpoint. The endpoint
     is computed on the edge's own goroutine by design; to land it in the node's tick you'd
     move endpoint computation into the node goroutine, breaking the per-owner model this
     refactor exists to reach. (b) fights the model.
   - **Hinge for (a) — resolved.** Edge endpoints are PORT ANCHORS (`srcH`/`dstH` =
     `SourceHandle`, `node_mover.go:431-432,478`). Ports ARE independently draggable
     (`setPortAnchorId`, `node_mover.go:218-220`), so an endpoint does move independent of the
     node CENTER. But ports are packed as rows in the **Port block, owned by the node's mover,
     shipped with the node** (`pack.go:310-322`, `px/py/pz`). Referencing the PORT row (not the
     node center, not a copied coordinate) makes the endpoint carry no independent value: the
     tear is unrepresentable because the edge has nothing of its own to go stale. The packer
     already resolves edge→port this exact way for LayoutLink (`edgeRowForPair`,
     `pack.go:243-249`). So (a) holds by referencing the port row.
2. **Row identity across frames.** Node row order is stable today because the accumulator
   assigns it. With owners publishing independently, the packer needs the same stable
   ordering, from a table built once at load. `portTable` / `edgeTable` / `nodeTable`
   already do exactly this — reuse, do not reinvent.
3. **The string sections.** Labels, port names, edge labels are offset/length into a
   shared byte section, in row order. They are static after load, so they can be built
   once — but confirm nothing mutates a label at runtime.
4. **Bead rows are variable-count.** Node and edge rows are fixed per topology; beads come
   and go. Publishing "the beads on this wire" as one immutable slice per wire is the
   natural shape, with the packer concatenating. Verify the count fits the header the
   same way.

## What must be proven

1. **The pixel budget holds.** During a fast drag, an edge endpoint must not visibly
   separate from its node. This is the ONLY perceptual constraint in the plan, so it is
   the test that matters — drive a drag headlessly, pack frames, and assert the edge
   endpoint and the node centre in the SAME frame differ by less than a threshold stated
   in world units. Write the threshold down; it is the accepted magnitude.
2. **No torn rows.** A row read by the packer is always a value some owner published, never
   a mix of two. Falls out of the atomic-pointer discipline, but assert it under `-race`
   with owners publishing continuously while the packer runs.
3. **Frames stay complete.** Every node, edge and port has a row in every frame, even if
   its owner has published nothing since the last one. A missing row is not a small error;
   it is a node vanishing.


## What it costs

- A large refactor of the ingest half of `Buffer/`. This is not a small change.
- Emission becomes periodic or dirty-flag driven rather than event driven (open question 1).
- Debuggability changes shape: today one accumulator sees everything, which is convenient
  when something is wrong. Afterwards state is distributed across owners.

## What it buys

- Deletes the last shared mutable state in the render path. No exceptions, no residual
  accumulator.
- The buffer becomes what the model already claims: each thing reports itself, nothing
  coordinates.
- Removes a reassembly step for something that was never in pieces.
