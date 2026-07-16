---
name: the snapshot emitter packs from a fan-in channel
description: Describes the actual Go→TS seam — many goroutines send events through Trace's channel to one drain goroutine that packs the buffer. Single-writer is a real contract with a known race signature. Descriptive, not a rule.
metadata:
  type: project
---

**What this is:** a description of how the buffer emitter is actually wired, as
of 2026-07-15. It records the shape, not a rule — see the last section. Verify
against code before leaning on any symbol here.

**The chain, end to end:**

1. A node/wire goroutine calls a `tr.*` trace method (`tr.NodeGeometry`,
   `tr.Geometry`, …) — this is the goroutine *sending*.
2. That queues onto `Trace`'s channel.
3. A **single drain goroutine** consumes the channel in FIFO order.
4. The drain goroutine calls `snapState.Update(ev)` — wired as Trace's `onEvent`
   hook at `main.go:55`, `T.NewWithSinkHook(0, nil, snapState.Update)`.
5. `Update` switches on `ev.Kind`, mutates its tables, and calls `emitSnapshot()`
   at each state-change point (`Buffer/snapshot.go:363`).

So the emitter is a **fan-in channel consumer**: many senders, one channel, one
consumer that serializes. `main.go:51` describes its job in one word — the hook
"packs."

**Single-writer is a real contract, not a convention.** `SnapshotState`'s doc
comment requires every method call to come from that one drain goroutine.
`main.go:95-101` records what happened when it was bypassed: a first attempt at
deterministic row order added a `SnapshotState.SeedNodes`/`SeedEdges` method and
called it directly from the main goroutine. It hit a genuine data race — `go
test` panicked inside `writeEdgeBlock`/`SetEdgeRow` on a corrupted slice. The fix
routed the seeds through the same `tr.NodeGeometry`/`tr.Geometry` pipeline every
node's own startup emit uses, so they queue like any other event. **If you need
to get state into the buffer, send an event; do not reach into `SnapshotState`.**
That is a load-bearing lesson with a known failure signature, not style.

**What the emitter does not contain.** No geometry math. Every loop in
`snapshot.go` is a table rebuild or a row lookup (`rebuildNodeTable`,
`rebuildPortTable`, `nodeRowIndex`, …). Positions are computed by the goroutine
that owns them — MODEL.md is explicit that Go computes a bead's absolute world
position from its own live node/port endpoints, and the editor only decodes:
`readBeadX/Y/Z` are defined in `tools/topology-vscode/src/schema/buffer-layout.ts`
and consumed by `tools/topology-vscode/src/webview/three/BeadInstances.tsx`.
(MODEL.md:90 places both inside `buffer-scene.tsx`; that is stale — see the
caveat below.)

**Caveat — the docs' file pointers are one hop stale.** `buffer-scene.tsx` is no
longer the monolith MODEL.md and CLAUDE.md describe. It is now a ~4KB
composition root that imports and assembles `BeadInstances`, `NodeInstances`,
`PortInstances`, `EdgeTubes`, `BufferCamera`, … from 27 sibling files in
`three/`. The docs' claims are directionally right (that file composes the
scene) but every file:symbol pointer resolves one hop away from where the code
actually is. Grep for the symbol; do not trust the filename in a doc. This is
the split-drift that [[feedback_guards_hardcoding_single_file_break_on_split]]
predicts, landing in the doctrine docs rather than in a guard.

**One historical exception, now gone.** A central iterate-to-fixpoint solver over
all nodes/edges did once live inside the emitter — `Buffer/fade.go`, now deleted
(it was ported from the retired TS render-mask fixpoint). It went with the fade
feature (58557356). `grep "for changed := true" Buffer/` is now empty.

**Why this matters for [[feedback_per_goroutine_bridge]].** That memory worried
that a "central emitter" deviated from David's invariant — "each goroutine sends
things to TS and TS sends things the goroutine picks up." The wiring above shows
the invariant is intact: goroutines do send, via `tr.*`. A fan-in channel with a
single consumer is not a coordinator; it is the ordinary Go shape. The worry
misread a serializer as a decision-maker.

**Descriptive, not prescriptive.** The tempting next step is to promote this into
a rule — *the emitter may pack but must never decide*. That line is not written
anywhere in MODEL.md, CLAUDE.md, or `memory/`, and David has not stated it. This
file deliberately stops at describing the code. If the rule is wanted, it belongs
in MODEL.md in his words, not inferred here. See [[feedback_dont_invent_doctrine]].
