---
branch: task/per-owner-buffer-rows
---

# Reference, don't copy — the endpoint tear is a duplication artifact, not a timing one

This is a refinement of [per-owner-buffer-rows.md](per-owner-buffer-rows.md). That plan
retires the accumulator so each owner publishes its own row. This doc removes the one
constraint that plan still carried — the "pixel budget" during a fast drag — by showing it
was never a timing constraint at all.

## The claim being dismantled

The per-owner plan states its single perceptual constraint as: during a fast drag, an edge
endpoint must not visibly separate from its node. It frames this as an accepted magnitude —
rows come from slightly different instants, and the gap must stay under a pixel threshold.

Walk the magnitudes and that constraint collapses:

- A "fast" drag is a human hand: ~2 px/ms at the aggressive end.
- The packer reads the node pointer and the edge pointer microseconds apart. In that gap
  the hand has moved **~0.1 px** — invisible by orders of magnitude. Read-skew is a non-event.
- The only way to spend a visible budget is a **whole tick** of lag between the two rows:
  `MsPerTick = 16`, so 2 px/ms × 16 ms = **~32 px**. That IS a visible tear.

So the machine's clock is glacial relative to the skew. The budget is unspendable *except*
through a full-tick lag between an owner and something that depends on it.

## Why the lag exists at all — the copy

An edge endpoint would only lag a node if it is stored as a **separate number** that must be
refreshed to match. And today it is: the Edge block carries `SX,SY,SZ..EZ` — the endpoint
coordinates, **copied** from the node/port positions — and `EdgeTube` reads those. "Same-tick
follow" is nothing but the requirement to keep two copies of one quantity in sync. It is
follow-latency between a value and a duplicate of that value.

But the endpoint *is* the node's position. It is not a quantity the edge owner computes and
owns; it is the node's own published coordinate. The edge genuinely owns only what lives
**between** the endpoints: the curve/tube shape and selection. Not the endpoints.

The `SX..EZ` copy is itself a small accumulator — the node's position, re-stored in edge
space. The per-owner plan removes the *big* accumulator (events → reassembled state) but
leaves this one in place.

## The change

**Owned quantities are independent by construction; everything else references them, never
copies them.**

- A **node** owns its position (published once, per per-owner-buffer-rows).
- An **edge** owns only its between-the-endpoints shape + selection. Its endpoints are
  **references** to the two node/port rows (by row index — the identity model already in
  use), resolved at pack/render time.
- A **bead** owns its fraction along its wire. Its world position references the wire's
  endpoints the same way, never a stored `x,y,z`.

## Why this is strictly better than a pixel budget

The 32 px tear becomes **unrepresentable**, not merely bounded:

- There is no second value, so there is nothing to keep in the same tick.
- There is no "dependent owner" — the edge does not follow the node; its endpoint literally
  *is* the node's row.
- You cannot open a gap between a value and itself.

This is the repo's own `make-bug-class-unrepresentable` rule (pick the formulation with no
free knob for the bug to live in): a tear needs two numbers; store one. No threshold to
write down, no drag-repro test to defend a magnitude, because the magnitude is structurally
zero.

## What this touches

Which of these fire depends on the Axis-A topology decision (see open questions). If a
packer is kept and resolves the gather, the wire format is unchanged and only the Go pack
step moves; the schema/EdgeTube bullets apply only if references are shipped to TS.

- **Buffer schema (`Buffer/layout.go`)** — *only if references are shipped to TS.* The Edge
  block's `SX..EZ` columns are the copy to remove; replace with two row-index references
  (start node/port row, end node/port row). Regenerate `buffer_layout_gen.go` +
  `buffer-layout.ts` in the same commit (schema is hand-authored `buf:"…"` tags; both
  generated files derive from it — see the primitive landing rule). If instead a packer
  resolves the gather, the Edge block keeps shipping `SX..EZ` and this bullet does not fire —
  the copy just stops being *owned state* and becomes a single-frame pack detail.
- **`EdgeTube` (`three/EdgeTube.tsx`)** — *only if references are shipped to TS.* Today reads
  `SX..EZ` and builds the tube. After: gather the two endpoint positions from the referenced
  Node/Port rows, build the tube between them. This is a *gather*, not a computation — TS
  reads a position it is told to read by index; it authors no geometry and no timing, so it
  stays inside the render+forward rule. (Confirm against `check-no-webview-state.sh` —
  reflecting a referenced row is not holding domain state.)
- **The Go pack site** — *if a packer is kept.* Wherever the edge's endpoint bytes are
  written today, write them by reading the referenced node/port rows from the same frame's
  published pointers instead of from an owned edge-space copy. No schema change; `EdgeTube`
  untouched.
- **Beads.** Same move, and same fork: bead position is a fraction-along-wire + a wire
  reference, never an owned world xyz. Resolve the interpolation at whichever site Axis A
  puts the gather. Verify where bead world position is computed today before moving it.

## Open questions — settle before writing code

1. **~~Where does the reference resolve — Go pack, or TS render?~~ Not an independent
   question — the gather rides the buffer-topology decision.** An earlier draft posed this
   as a fresh choice ("resolve in the packer vs resolve in TS"). It is not. Whether a packer
   even exists is the *parent plan's* still-open axis (one packed buffer vs N per-block
   buffers streaming change-driven — the N-buffers direction removes the global packer). So
   phrasing an open question in terms of "the packer" presumes an answer to that axis that
   is not settled.

   The reference formulation dissolves the dependence anyway. A reference is not a stored
   instant; it is *wherever the node is now*. So resolution is correct at **whatever site
   assembles bytes**, under either topology:
   - *Packer kept*: the packer gathers the edge endpoint from the same frame's node pointer.
   - *N buffers, no packer*: TS gathers the edge endpoint from the latest Node buffer it
     holds.
   Both yield the current node position, never a stale copy, so the tear is unrepresentable
   under either. The gather is a *gather*, not a computation, wherever it lands — it stays
   inside render+forward if it lands in TS.

   **Therefore the actually-open decision is the parent plan's Axis A — one packer, or N
   per-block buffers — and reference-resolution follows it automatically.** Decide Axis A on
   its own merits (emission cadence, wire traffic, TS-drift risk), not on anything in this
   doc. Axis B (copy vs reference) is settled here: reference, because it makes the tear
   structurally impossible independent of A.
2. **Port anchors.** An edge endpoint is often a *port* on a node, not the node centre.
   Confirm the reference target (node row vs port row) and that ports are already row-owned
   under the per-owner plan.
3. **Does anything mutate an endpoint independent of its node?** If some gesture moves an
   edge end *without* moving a node (a free-floating anchor), the endpoint is genuinely
   owned and this doc's premise fails for that case. Grep the anchor-move FSM path before
   assuming every endpoint is a pure reference. (See `project_rootmove_is_per_pointer_move`
   and the port-anchor move path.)

## Relationship to the parent plan

per-owner-buffer-rows.md removes the accumulator (state was never in pieces). This doc
removes the last duplication (the endpoint was never two things). Together: nothing is
reassembled and nothing is copied — each thing is published once by its owner, and
dependents reference it. The "pixel budget" line in the parent plan should be struck
outright — not "once a question here is decided," because Axis B (reference) already sets
that magnitude to zero regardless of how Axis A lands. The only decision that remains is
Axis A itself (one packer vs N per-block buffers), which this doc does not settle and does
not need to.
