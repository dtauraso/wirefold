---
branch: task/layout-rebuild-on-domain-network
---

# Layout on the domain network (rebuild)

## Why

The editor-time layout system (`MoveDispatch` + `nodeMover`/`edgeMover`) computes node
positions with a **central solve** from the stdin goroutine (`RootMove` → `fanCenters` +
`remeasureTriples`). The `sendMove` peer-to-peer scaffolding on `nodeMover` is dead code.
This is out of step with the domain network, which is genuinely peer-to-peer (nodes and
wires as goroutines coordinating over channels, no central runner). The layout system is
to be **redone from scratch** so position propagation is peer-to-peer too.

## Agreed model

- **Nodes are shared.** The same node goroutine participates in BOTH graphs — the domain
  graph (visible wires carrying beads) and the layout graph (hidden edges carrying drag
  messages). One node, two edge sets.
- **Hidden layout edges mirror the domain edges one-for-one.** Wherever there is a domain
  wire (source → target), there is a parallel layout edge with the same connectivity. The
  layout edges are **not rendered** and carry drag/layout messages instead of beads.
- **Drag messages hop node-to-node along the hidden layout edges** — no central
  `MoveDispatch` solve. Dragging a node injects a message; it propagates over the layout
  edges.
- **First message type: radius (iR) propagation.**
  - The message carries a new `iR` (radial step of a node about its reference).
  - On drag, the node sends the new iR along its outgoing layout edges.
  - A **time node** (`HoldNewSendOld`) that receives it re-places itself at the new iR and
    **forwards** on its own outgoing layout edges.
  - A **non-time node** that receives it re-places itself but **stops** (does not forward).
  - So only time-node descendants keep the cascade going; the wave terminates at the first
    non-time node on each branch. (Confirmed by David's example: editing 2→6 or 2→5 sets
    2→6, 2→5, 5→8, 5→7 when 2 and 5 are time nodes.)

## Where layout handling lives (decided)

**Inside each node's `Update()` firing loop.** One goroutine per node truly does both: its
`Update()` select loop gains a case for the hidden layout in-channel alongside its bead
channels. This is the most literal "same node" — the domain node goroutine IS the layout
node goroutine. It edits every `nodes/<Kind>/node.go` Update loop. The separate `nodeMover`
goroutine (and eventually `MoveDispatch`'s central solve) is retired as this lands.

The per-node layout plumbing (hidden inbound channel + outbound channels mirroring the
node's domain out-edges) is shared/injected generically by the loader — the same way
`EmitGeometry` is injected today — so only the tiny select-case and a shared handler are
added per kind, not duplicated logic.

## Deliberate override of the prior drift rule

MODEL.md's drift rule keeps geometry/position logic out of the domain firing goroutines.
David has explicitly chosen to UNIFY the two here: the same node goroutine handles both
bead firing (domain) and layout messages (hidden edges). This planning doc records that as
an intentional model decision for this branch, not drift.

## Node-kind vocabulary

"time nodes" = `HoldNewSendOld`. See `memory/project_node_color_vocab.md`.
