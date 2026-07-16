---
name: specify-go-layer-first
description: State the Go-layer answer before/alongside the visible-layer spec. Implicit Go-layer slots get filled with coordinator-shaped defaults because this project's decentralized model is rare in training data.
metadata:
  type: feedback
---

For any new behavior in this repo, state the **Go-layer**
answer before (or alongside) the visible-layer spec. "There's a
pause button" is a visible-layer spec. "What does a wire do when
paused?" is the Go-layer spec. The visible spec alone is not
enough.

**Why:** This project's Go model — decentralized primitives,
no coordinator, visible-state-IS-logic — is rare in training data.
The common patterns the model has internalized are coordinator-shaped
(cursor, driver, central state). When the Go-layer question is
left implicit, the default fill is whatever central mechanism already
exists nearby, and that shape silently leaks into the new feature. The visible spec can still be satisfied with a wrong
Go layer underneath, so the regression isn't visible until a
*second* feature exposes the centralization (e.g. pause exposed the
cohort cursor's monotonic released-set; clear-slot exposed a gate's
deferred-side hack).

**How to apply:** When the user names a new behavior, before
proposing implementation, write one line answering "what does the
local Go primitive (wire / node / slot) do under this
behavior?" If that line reaches for an existing global mechanism
(cursor, driver, set, controller), stop — that's the coordinator
sneaking in. The right shape is almost always a per-primitive
observable signal that the primitive itself reads, with no central
authority. This is the workflow-level generalization of
[[feedback_go_vs_coordinator_bias]]: that memory says coordinator-shaped
fixes are drift; this one says *when* the drift happens — at the
moment a behavior is specified only at the UI layer.

Concrete tell: if you're about to plumb a new feature *through* an
existing global object (gate, driver, cursor), the Go-layer
spec was probably skipped. Re-derive locally first.
