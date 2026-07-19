---
name: project-rootmove-is-per-pointer-move
description: RootMove runs on EVERY pointer-move event, not once per drag — a naming trap that has caused two separate per-drag-semantics bugs
metadata:
  type: project
---

`MoveDispatch.RootMove` (nodes/Wiring/node_move.go) is called from
`applyNodeDragTarget` in gesture.go on **every pointer-move event** during a drag
(~8ms apart), plus once on pointer-up. It is NOT called once per drag, despite
reading like it is.

The real once-per-drag edge is the `gestPending -> gestDragging` transition in
gesture.go. That is where `tr.AbcDragReset()` fires and where drag-scoped state
must be anchored or reset.

**Why:** the name says "root move" — singular, the move — so anything with
per-drag semantics gets written there by reflex. Two bugs from this in one day
(2026-07-19):

- `338f05da` — `AbcDragReset` emitted from RootMove, so the drag-log recipient
  reset interleaved with the async neighborSetC fan and dropped recipients whose
  marks landed after the next move's reset.
- `154a05bd` — the drag-received cell delta computed as new-minus-old within one
  RootMove call, so it measured one mouse-move. With 15-degree theta/phi steps
  and stepR 20, `round(angle/step)` returns the same integer for dozens of
  frames, so the log read (0,0,0) almost always. Correct arithmetic over a
  useless interval.

**How to apply:** before putting anything in RootMove, ask whether it is
per-drag or per-move. Per-drag belongs at the FSM drag-start edge, reaching the
node's own mover by message (see `moveMsgKindDragStart` / `armDragAnchor`) so the
owning goroutine holds it. Symptom of getting it wrong: a value that is
arithmetically correct but almost always zero/empty, flickering for one frame.

Related: [[feedback_invariants_drive_design]] (simulate frame-by-frame, not just
steady-state — both bugs are invisible in a single-frame mental model),
[[feedback_check_the_signal_the_check_emits]].
