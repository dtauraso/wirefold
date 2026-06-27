---
name: feedback-reflect-dont-create-store
description: "Don't use a store" (David) means don't create a NEW store/conduit for a streamed bit; reusing an existing store to reflect a Go-owned value is fine
metadata:
  type: feedback
---

When David says "don't use a store," he means: do **not** create a NEW
store/conduit/state-authority on the TS side for a streamed bit. Reusing an
EXISTING store to merely REFLECT a Go-streamed value is fine.

**Why:** the model keeps all authority in Go (see [[project-go-visual-vocabulary]] and
MODEL.md "Editor surface"). A new TS store quietly makes TS an authority and is drift.

**How to apply:** Go owns the variable; a `tori-vis`-style `edit` op asks Go to change
it; Go streams the new value back on a trace event; TS renders Go's last-streamed value
out of an existing store. The scene-tori "rings" toggle
(`MoveDispatch.sceneToriVisible` → `scene-tori` trace event, reflected in the existing
camera store) is the reference pattern. Never make TS the source of truth.
