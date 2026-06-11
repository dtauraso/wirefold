---
name: clear-slot-button-is-armed-only-when-the-slot-is-filled
description: "ReadGate ⌫ button must be disabled unless the destination slot is `filled(v)`; otherwise users click on empty slots and clicks accumulate as deferred clears."
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 9b4bdc8f-d9fe-4e4b-81a4-a49697de4425
---

The ReadGate clear-slot (⌫) button must be **disabled when the
destination slot is not in phase `filled(v)`**. Enabled only while
the slot holds a parked value.

**Why:** David hit this directly — he clicked ⌫ on an empty slot
expecting it to "start" the next pulse, assumed the click area was
broken, kept clicking. Early clicks were no-ops, but any click that
landed mid-flight banked a deferred clear, so when the next pulse
arrived the slot instantly emptied and the following pulse fired —
appearing to be a "free pulse" the user didn't trigger. The contract
has to be honest: the button can only mean something when there's a
value parked in the slot.

**How to apply:** any editor affordance that clears a slot must be
gated on the destination slot's phase (`armed = slotPhase === "filled"`).
Under the slot-in-node model, the slot lives on the destination
node, so read it via `dest.slotPhase(slotId)`. Do not lean on
Go-side deferral to "do the right thing" — that mechanism
exists for Go, not the UI contract.
