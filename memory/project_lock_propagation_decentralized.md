---
name: project_lock_propagation_decentralized
description: Lock/colinearity propagation on node move must be decentralized message-passing between node goroutines, never a central worklist/collection
metadata:
  type: project
---

Agreed 2026-07-04 replacement for editor-time lock (colinearity) propagation on node drag.

**Model (David's words):** a node can only update ITSELF and send update messages to the
nodes it's doubly-linked to. Any node receiving the message checks if it needs to
recalculate; if it does, it updates itself and broadcasts the same update to ITS
doubly-linked neighbors. No queues. No collections.

**Why:** the central worklist (`RootMove` fixpoint over `md.polarEqs`, commit cb3dd91c)
was a coordinator+collection — the exact shape MODEL.md forbids. On an over-constrained
lock set it did not converge: each central re-solve amplified positions past 1e28 and
persisted the garbage into scene.json/meta.json permanently. Reverted in
`revert(locks): remove transitive lock-propagation worklist`. See [[feedback_go_vs_coordinator_bias]].

**Invariant (David):** panel-authorable locks must NEVER be able to explode the sim.
The decentralized cascade can't amplify because each node only COPIES its own value from a
neighbor (idempotent once equal) and re-broadcasts only if it actually moved > epsilon —
a consistent set converges, an over-constrained one settles to last-consistent and goes silent.

**Go-layer facts (verified against code, not the file layout — a later round is expected
to move these tables onto `LayoutHolder`, so treat the MECHANISM below as durable and the
current file/symbol names as incidental):** each node is a `nodeMover` goroutine with an
`inbox chan moveMsg` + select loop (`nodes/Wiring/node_move.go`). Propagation is routed
node-to-node via `sendMove` (a lookup into `md.dispatch`, a map keyed by node/edge id →
inbox channel) — no central worklist. Which nodes a rule-node's cascade reaches is
described by three tables in `node_move.go`: `ruleSource` (rule-node → its designated
source neighbor), `ruleFollowers` (rule-node → follower neighbors it repositions), and
`gateNeighbors` (a two-neighbor gate node → its two fixed neighbors). Key MODEL.md rule
for this: state rides the MESSAGE (e.g. `moveMsg.FromCenter`/`TargetC` for an `equalize`
message), not shared-memory reads across goroutines. Keep the dragged node's own
edge/aimed-port fan; only the lock-FOLLOWER propagation becomes the node-to-node cascade.

There is no `links.go` or `locks.go` in the tree, and no `movementLink`/`applyPolarEqs`/
`applyPortTorusColinearity` symbols exist as of this writing — an earlier version of this
note named those; they were never real (or belonged to a design that was never merged).
If you are trying to find where lock/follower math lives, grep `node_move.go` fresh
rather than trusting any filename cited here.
