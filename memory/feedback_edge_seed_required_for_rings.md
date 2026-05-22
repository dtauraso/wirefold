---
name: feedback-edge-seed-required-for-rings
description: Ring topologies need a data.edgeSeeds entry on the feedback input port to break startup deadlock; the loader fills the channel once before goroutines start.
metadata:
  node_type: memory
  type: feedback
  originSessionId: 4d5176b2-1c69-4013-9932-e601ea592a86
---

Ring topologies need to prime the destination slot of the feedback wire once at mount, or both sides wait on each other and never start. In the current Go loader the mechanism is node-level: `data.edgeSeeds: { "<inputPortName>": <value> }` on the receiving node. The loader (`nodes/Wiring/loader.go`) pre-sends `<value>` to the channel feeding that input port before any goroutine starts.

**Why:** Concrete instance — the edge-detector ring (Input → ReadGate → i0 → i1 → ReadGate). ReadGate's `FromChainInhibitor` ack input is fed by i1, which is fed (transitively) by ReadGate. With the partial-0 emit rule removed (see [[feedback-readgate-partial-0-is-spec]]), ReadGate refuses to fire until both slots are filled, so without a prime on the `FromChainInhibitor` port the ring never starts. The user's original ask: "i1 needs to send an initial seed pulse to readgate only 1 time so readgate starts." `edgeSeeds` implements exactly that contract.

**How to apply:** When wiring a ring in `topologies/*.json`, identify the feedback wire (the one closing the loop). On the *receiving* node, add `data.edgeSeeds: { "<destPort>": <value> }`. The prime is one-shot — it does not re-fire. Historical note: pre-RF substrate-r used edge-level `wire.data.seed`; that API no longer exists in the Go loader. Don't propose `data.seed` on a wire — it will be silently ignored.
