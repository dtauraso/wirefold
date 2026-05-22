---
name: feedback-code-self-defends
description: Solid code structure prevents AI from drifting toward industry defaults; preferable to corrective memory entries
metadata:
  type: feedback
---

# Code Self-Defends

**Lead:** When local code is overwhelming and clear, AI follows it. When local
code is sparse, AI defaults to industry training (e.g., "wire ack", "tick loop",
central scheduler).

**Why:** This session showed agents repeatedly proposing banned-vocab patterns
(ack channels, tick loops, schedulers) when designing in sparse areas. Code that
makes the wrong shape structurally impossible (no `Ack` type exists, no central
scheduler module exists, linter rejects banned tokens in substrate files) beats
memory entries that warn against drift.

**How to apply:** Each time a memory entry would otherwise correct AI drift, ask
"could this be a code-structure change instead?" Land the structural change;
retire the memory. Code that prevents the wrong thing > memory that warns
against it.

**Levers:**
- Volume + consistency: more substrate code using the correct vocabulary leaves
  less room for the AI to interpolate from training data.
- Anti-industry naming: type and function names that don't match industry
  defaults make wrong patterns harder to reach by autocomplete.
- Types that can't express the wrong shape: if `Ack` doesn't exist as a type,
  you can't write `chan Ack`.
- CI lints for banned tokens: a failing lint is harder to ignore than a memory
  entry.
- Decision-point comments: inline `// NOT a scheduler — see MODEL.md` at key
  sites catches drift during code review.
