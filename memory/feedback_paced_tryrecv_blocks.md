---
name: paced-tryrecv-blocks
description: In.TryRecv / PacedWire.Recv were deleted as dead code (2026-07); the live non-blocking receive is In.PollRecv / PacedWire.PollRecv
metadata:
  type: feedback
---

`In.TryRecv` and `PacedWire.Recv` (the cond.Wait-parked receive this memory used to warn about) were DELETED — a code-smell audit found `In.TryRecv` had zero callers anywhere, including tests. All live nodes read via `In.PollRecv`, which is genuinely non-blocking: in paced mode it calls `PacedWire.PollRecv` (returns immediately with `ok=false` when no delivered value is present, without parking); it does not select on anything with no default and does not `cond.Wait()`. If future code adds a blocking receive back, judge its blocking-ness from the `paced_wire.go` implementation, not the Try/ok call-site idiom, per the original lesson here. The historical deadlock this memory described (unseeded feedback ring parking forever at t=0 on `TryRecv`) is now moot — no receive path blocks — but ring bootstrap still needs a seed edge for correctness (`feedback_edge_seed_required_for_rings.md`).
