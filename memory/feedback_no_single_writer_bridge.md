---
name: feedback_no_single_writer_bridge
description: The Go→TS bridge must not funnel through one pipe or one serialized writer; each owner writes its own stream
metadata:
  type: feedback
---

For the per-owner buffer-rows / accumulator-retirement work, David directed: "don't use 1
pipe and don't use a single writer." Do NOT funnel per-block frames through a single fd3
with a serialized writer goroutine.

**Why:** it violates the per-goroutine bridge invariant ([[feedback_per_goroutine_bridge]]:
each goroutine sends to TS itself). A single writer re-introduces the fan-in the
accumulator-retirement is meant to delete.

**How to apply — the rule is literal: 1 goroutine = 1 binary channel to TS.** Every EMITTING
goroutine gets its own stream, not one-per-block-type. That means each `nodeMover`, each node
`Update` loop (interior beads), each `edgeMover`/wire (edge + transit beads), and the
view/gesture goroutine (camera/overlay/scene) each owns its own channel.

**Transport = REUSE the existing inherited-stdio-pipe mechanism, generalized from 1 fd to N.
NOT a socket, NOT a server, NOT a new system.** fd3 today is just the 4th entry in the ext
host's `stdio` spawn array; extend that array to fd 4, 5, 6, … — one inherited pipe per
goroutine. Each goroutine writes its own fd with `os.NewFile(n)`, framed binary exactly like
fd3 (`[len:u32][payload]`). David was explicit: "there should not need to be a new system for
this; there should not need to be an extension host [as a server]." Do not build a socket
listener/dialer.

Identity is by POSITION, not a handshake: both sides enumerate the spec in the SAME canonical
order (the load-time seed order that already assigns stable buffer rows), so fd_k ↔ (kind,row)
is agreed by convention. The ext host knows N from the spec it holds; "updated when new things
are added" is handled by the existing RESPAWN (a topology change re-reads the graph on respawn
→ recompute N → new stdio array). No dynamic-connection machinery.

Rejected alternatives (do not revisit without David): (1) one pipe per block TYPE — the
node/edge/bead pipes would each have many goroutine writers → per-pipe serialization → "one
writer per block", still violates no-single-writer. (2) Unix-domain-socket transport with a
listener — rejected as "a new system"; reuse inherited stdio pipes instead. See
[[project_per_owner_buffer_split]] for the staged split this constrains.
