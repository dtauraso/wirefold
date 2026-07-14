---
branch: task/node5-decentralized-cascade
---

# Node-5 cascade → decentralized node-to-node messaging

## Why

Today the whole layout cascade is a **central coordinator**: `MoveDispatch.rootMove`
recurses on ONE call stack, with hardcoded per-node `case "5"/"2"/"1"/"9"/"10"`
branches, and terminates via an `origin` identity guard (stop when the chain would
return to the node that started it). The `origin` guard exists only to prevent the
5→2→5→2 **recursion** from blowing the Go stack — it is a proxy for "this hop won't
change anything."

David's model: **nodes run concurrently and send update messages to each other along
the double-linked edge channels.** Propagation stops when **there is nothing left to
adjust** (edge-length delta zero), not when an identity guard fires. Termination is
emergent convergence, not a bounded walk.

The transport already exists: `nodeMover.sendMove(id, msg)` routes a `moveMsg` to
another mover's inbox by id (dispatch map, read-only directory). Only the LOGIC lives
centrally today. This change moves the decision + routing into the node goroutines.

## Model (agreed with David)

- **Node = goroutine that owns its layout rule.** Single-writer invariant preserved: a
  node writes only its OWN position (`applyCenter` is the sole writer).
- **Edge = the double-linked channel** between its two node goroutines (transport for
  update messages).
- **Receiver-computes** (agreed): when node N is told "neighbor S moved, hold edge S-N at
  length L", N moves *itself* to `S.center + unit(N_old ← S) * L` and writes itself.
- **Message carries the target c** (agreed): the length to hold rides in the message, so
  every node runs the SAME generic rule — the per-node `case` branches collapse.

### Per-node rule

Each rule-node N has a designated **source** neighbor S_N (5→2, 2→5, 1→2). Its other
neighbors split:
- **Followers** — neighbors WITHOUT their own rule (6,7,8,3). N repositions them to the
  new length L (via Equalize messages; they move themselves, then STOP — no rule, no
  forward).
- **Rule-neighbors** — neighbors WITH their own source rule. N does NOT reposition them;
  instead, if edge N↔RuleNeighbor actually changed length, N notifies it "your source
  edge changed" so it runs its OWN rule. Delta-gated: unchanged length → no message.

N is TRIGGERED when its source edge N↔S_N changes length (S moved, or N itself moved).
On trigger: L = new dist(N, S_N); send each follower an Equalize{fromCenter: N.center,
targetC: L}; for each rule-neighbor whose shared edge changed, forward the trigger.

### Stop condition (edge-length delta)

A node forwards along an edge only if the c-distance it would set **differs** from the
one already stored (compare the length being equalized to, not the position). Zero delta
→ no send. This is the base case, now distributed — no `origin`, no recursion, no stack.

## The node-5 drag chain (must reproduce identical final positions)

Drag 5 → target:
1. Node 5 moves itself to target. L = dist(target, center(2)).
2. Node 5 is a rule-node, source 2. Followers {7,8}: Equalize to L → 7,8 move to dist L
   from 5, then STOP (no rule).
3. Edge 5↔2 changed (5 moved) → notify rule-neighbor 2. Node 2 (source 5) triggers:
   L2 = dist(center2, newCenter5) = L. Follower {6}: Equalize to L → 6 moves to dist L
   from 2, STOP. Rule-neighbor 1: edge 2↔1 did NOT change (2 didn't move) → delta zero →
   **no message → node 1 never runs.** Node 3 stays. ✅ (Current code runs node 1's rule
   as a no-op; new model simply skips the dead hop — SAME final positions.)

Net moved: 5 (drag), 7, 8, 6. Node 1, node 3 unchanged. Matches current behavior.

Node exclusion of 1 from node 2's peer set + separate cascade (current `if dragged=="2"
&& other=="1"`) unifies into: 1 is a rule-neighbor of 2, not a follower — notified only
when edge 1↔2 changes.

## Scope: node 5 only (DONE)

This branch converts **node 5's** cascade and nothing else. Delivered:
- Node-5 chain (5 → 2 → followers 7/8/6) over `sendMove` messages between movers; stops
  before node 1 because that edge did not change. Final positions identical to the old
  central path (`node5_equalize_test.go` + `TestNode5DragCascadesToNode2Follower6AndStopsBeforeNode1`).
- Anti-drift `TestNode5DragEmitsDecentralizedMessages`: asserts the exact message trace via
  a test-only `msgTap`; a reversion to central recursion emits zero `cascade` messages and
  fails hard.
- Sparse `cascade.*` breadcrumbs (`.probe/go-debug.jsonl`) at each cascade decision.

The `SenderID==""` termination is COMPLETE for node 5, not a placeholder: node 5 is the
only node that moves in its chain, so a forwarded trigger provably carries no change.

NOT part of this branch (other nodes' cascades — separate tasks if ever wanted): porting
gate nodes 9/10 or node 6, and retiring the `origin`/`excludeOrigin` params, which still
serve the OLD central path that nodes 2/1/6/9/10 use when directly dragged.

Reuse (do NOT reinvent): `fanCenters` (one-node map commits a single node's center +
persist + reachR), `centerOfNode`/`heldPolar`/`heldEdges`, `cart2polar`,
`requantizeLocalPolars`, quantized-offset persist. The whole-graph SOLVE machinery stays;
only the DECISION + ROUTING move into the movers.

Guards to keep green: `scripts/stop-checks.sh` (go build/test, staticcheck, the guard
suite). Single-writer-of-position must stay true (a node writes only its own center).
