---
name: an affordance must be disabled when it cannot mean anything
description: A control whose backing state can't accept the action must be disabled, not silently bank the action for later; don't lean on Go-side deferral to fix a UI contract.
metadata:
  node_type: memory
  type: feedback
  originSessionId: 9b4bdc8f-d9fe-4e4b-81a4-a49697de4425
---

**Rule:** An editor affordance must be **disabled when the state it acts on
cannot accept the action**. It must not silently bank the action for later. If a
click can't mean anything now, it must not quietly mean something later.

**Why:** David hit this directly. A clear button was clickable while the state it
cleared was empty. He clicked it expecting it to advance things, assumed the
click area was broken, and kept clicking. The early clicks were no-ops — but any
click that landed mid-flight banked a *deferred* clear, so when the next value
arrived the state instantly emptied and the following one fired. It surfaced as
a "free" event he never triggered.

The bug isn't the deferral mechanism; it's that the control lied about its own
contract. An enabled control promises the action is available. If the action is
unavailable, the honest move is to disable the control — not to accept the click
and reinterpret it against future state.

**How to apply:**
- Any affordance that clears, resets, or steps state must be gated on that
  state's actual readiness — enabled only while there is something to act on.
- Do not lean on Go-side deferral to "do the right thing." That mechanism exists
  for Go's own ordering, not to paper over a UI contract.
- Watch for the tell: a user clicking repeatedly and concluding the hit area is
  broken usually means an enabled control is a no-op, and the clicks are
  accumulating somewhere.

**Vocabulary note:** the original instance named a specific node kind, a per-slot
phase enum, and a `dest.slotPhase(slotId)` read. All of that is retired — MODEL.md
is explicit that **there is no slot**; node-local held state replaces it. The
contract above is what survives; re-derive the readiness check against current
code rather than trusting any accessor named here.
