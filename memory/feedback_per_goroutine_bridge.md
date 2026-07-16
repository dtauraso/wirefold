---
name: per-goroutine-bridge
description: David's Go↔TS bridge invariant — each goroutine sends things to TS and TS sends things the goroutine picks up. The old "geometry is the deviation" complaint is resolved and must not be acted on.
metadata:
  type: feedback
---

**David's invariant (his words):** "each goroutine sends things to TS and TS
sends things the goroutine picks up."

**How to apply:** when routing TS input inward, route it to the *owning*
goroutine rather than adding a central handler. Relates to
[[feedback_node_model_not_networking_handshake]] — local work + drive outputs.

**Resolved — do not act on the old complaint.** This memory used to say geometry
was the deviation, because edge curves were emitted centrally and "Go never
emits node/port world positions at all." That is now **false**. MODEL.md is
explicit that Go owns the bead's absolute world position, computes it from its
own live node/port endpoints, and packs it into the content buffer; the editor
decodes and draws it (`readBeadX/Y/Z`) and interpolates nothing. The
content-buffer work closed this. `NodeMoveRegistry`, cited as the central
node-move emitter, now survives only in a `loader.go` comment.

**The buffer does not deviate from the invariant.** Go packing the whole scene
into one streamed buffer looks central, but the emitter is a fan-in channel
consumer: goroutines send via `tr.*`, Trace's channel carries it, one drain
goroutine packs. Goroutines still send. See
[[project_emitter_packs_from_a_fan_in_channel]] for the wiring, the single-writer
contract, and the data race that enforces it.
