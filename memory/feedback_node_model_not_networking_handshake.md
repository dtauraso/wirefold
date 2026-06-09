---
name: node-model-not-networking-handshake
description: Model is nodes doing local work + driving their outputs; do not import TCP-handshake/ack-nack/send-gating delivery guarantees
metadata:
  type: feedback
---

The substrate is nodes-and-wires, not a computer-networking model. Each node focuses only on what it has to do and how hard it drives its output; it does NOT try to guarantee delivery. There is no ack/nack, no TCP-style handshake, no send-gating reliability between nodes — a node emits on its output based on its own local state, and a receiver reads whatever is there. Multiple signals being in flight on a wire is not a "deviation" to reconcile; it is just nodes emitting freely.

**Why:** Across many turns the assistant repeatedly reintroduced networking/handshake framing (ack wires, `consumeGated` send-gating, "direct channel vs PacedWire", multi-pulse-as-a-delivery-problem). David named the TCP handshake from computer networking as the thing getting in the way — nodes are not trying to solve a guaranteed-delivery problem. Delivery guarantees are a coordinator-shaped default from training data, the wrong shape for the substance. Spike trains (repeated exposure) were also floated and judged not needed here.

**How to apply:** When designing wire/signal behavior, frame it as "what does each node emit based on its local state" — never "how do we guarantee or acknowledge delivery between nodes." If you catch yourself adding ack/nack, handshake, send-gating, or single-vs-multi-pulse delivery framing to a wire, that is the drift — stop and reframe to local node behavior. The `consumeGated`/send-gating apparatus currently in MODEL.md is this same intrusion and is a candidate for removal. Relates to [[feedback_substrate_vs_coordinator_bias]] and the medium-vs-substance doctrine in CLAUDE.md.
