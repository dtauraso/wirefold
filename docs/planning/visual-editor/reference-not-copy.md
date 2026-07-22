---
branch: task/per-owner-buffer-rows
---

# Reference, don't copy — the endpoint tear is a duplication artifact, not a timing one

This is a refinement of [per-owner-buffer-rows.md](per-owner-buffer-rows.md). That plan
retires the accumulator so each owner publishes its own row. This doc removes the one
constraint that plan still carried — the "pixel budget" during a fast drag — by showing it
was never a timing constraint at all.

**Buffer topology is decided: N per-block content buffers, no global packer.** Each block
(Node, Edge, Bead, …) is its own binary content buffer, streamed change-driven by its
owner(s) — the Bead buffer streams every tick because beads churn; the Node buffer streams
only on a move. This restores "Go emits only when something changes" per buffer and removes
the single-frame packer entirely. Everything below assumes it. (This settles what earlier
drafts called "Axis A." It is not an open question — do not reintroduce a packer.)

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

With N per-block buffers, the Edge and Bead buffers ship *references*, and TS resolves them
at render by gathering from the latest Node/Port buffer it holds. Since a reference is
"wherever the node is now," the tear is unrepresentable even though the Edge buffer and Node
buffer arrive on different ticks — the edge end is always the current node position, never a
stored instant.

- **Buffer schema (`Buffer/layout.go`).** The Edge block's `SX..EZ` columns are the copy to
  remove; replace with two row-index references (start node/port row, end node/port row).
  Regenerate `buffer_layout_gen.go` + `buffer-layout.ts` in the same commit (schema is
  hand-authored `buf:"…"` tags; both generated files derive from it — see the primitive
  landing rule).
- **`EdgeTube` (`three/EdgeTube.tsx`).** Today reads `SX..EZ` and builds the tube. After:
  gather the two endpoint positions from the referenced Node/Port rows, build the tube
  between them. This is a *gather*, not a computation — TS reads a position it is told to
  read by index; it authors no geometry and no timing, so it stays inside the render+forward
  rule. (Confirm against `check-no-webview-state.sh` — reflecting a referenced row is not
  holding domain state.)
- **Beads.** Same move: the bead row is a fraction-along-wire + a wire reference, never an
  owned world xyz. The renderer interpolates the referenced wire's endpoints. Verify where
  bead world position is computed today before moving it.

## Open questions — settle before writing code

1. **Port anchors.** An edge endpoint is often a *port* on a node, not the node centre.
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
outright: the reference formulation sets that magnitude to zero, and with N per-block
buffers there is no single frame for it to be a property of anyway. No topology decision
remains open — the only work left is the two caveats above (port-anchor target, and
whether any endpoint is ever moved independent of its node).
