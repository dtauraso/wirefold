---
name: feedback-invariants-drive-design
description: Treat user-stated invariants as axioms that drive design and verify framing, not as task constraints to check at the end
metadata:
  type: feedback
---

When the user states an invariant ("X never affects Y", "Z is uniform", "this must always be true"), it is not a constraint on the task — it is an axiom the design must be derived from. Same for verify steps: anchor to the invariant, not to its consequences.

**Why:** In pulse-substrate-transport, the invariant was "edge length does not affect pulse speed; px/ms is uniform." I knew this through every phase. But when designing Phase 4 (latency-live via TS→Go→TS round trip), I optimized for "implement latency-live" without mentally simulating the round trip against the invariant frame-by-frame. If I had, I would have seen immediately: any frame where the curve updates but `simLatencyMs` hasn't = a frame where px/ms ≠ uniform = violation. The bug was foreseeable from the design, not just at runtime. For Phase 5 verify I also told the user to look for "remaining time stretches/shrinks" — a *consequence* of uniform speed, not the invariant itself. The user had to restate the invariant a third time before I named it cleanly: "edge length should not be able to affect the pulse speed."

**How to apply:**
- When the user states an invariant, write it down literally and re-read it before every design choice in the same task.
- For each proposed change, mentally simulate: "is the invariant true at every frame / every moment, or only at steady state?" Round-trip designs that leave gap frames violate per-frame invariants by construction.
- For verify steps, frame the user-facing test in the invariant's own terms ("confirm X doesn't change Y"), not in terms of consequences ("confirm Y stretches when X stretches"). The consequence framing lets glitches that violate the invariant pass the test by accident.

Related: [[feedback-no-manufactured-shortcuts]] (don't offer partial paths when the model prescribes the full one); [[feedback-derive-model-from-visual-spec]] (derive the implied model up front; don't accept patches that preserve a wrong model); [[feedback-substrate-vs-coordinator-bias]] (name the contract violated, not the symptom).
