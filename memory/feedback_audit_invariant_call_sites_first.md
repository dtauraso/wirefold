---
name: feedback-audit-invariant-call-sites-first
description: "When a primitive-level invariant is violated (e.g. \"wire.load only when empty\"), grep every call site of that invariant before deep-diving any single one."
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 7b8746ce-fb8e-4959-8822-7f827b26900d
---

When a primitive-level invariant is violated at runtime (a throw, an assert,
an unexpected state transition), the first action is to grep **every call
site** of the operation that enforces the invariant and audit each for the
guard. Only after that audit comes up clean should narrower investigations
(log archaeology, single-body deep dives, React-closure theories) begin.

**Why:** in the May 2026 ChainInhibitor incident, `[Wire] dropped reload
while in-flight` was thrown on `i0.out->i1.in`. Three successive agent
investigations chased ReadGate's duty cycle, InputBody's queue, and a
hypothetical stale-handle bug in `useImperativeHandle` — across many turns
and several SVGs — before a 30-second grep of every `wire.load(` call site
under the old `rf/` component tree surfaced two unguarded loads in ChainInhibitorBody.
The bug was in a sibling primitive nobody had framed the investigation
around. Each prior agent stayed inside the narrow frame it was given.

**How to apply:** the moment a primitive-level throw appears, run the broad
grep first. For wirefold specifically: `wire.load`, `node.fill`,
`node.consume` are the load-bearing primitive operations — any violation
warrants an O(n) call-site audit before O(deep dive). Cost asymmetry
strongly favors the grep.
