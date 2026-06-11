---
name: paced-tryrecv-blocks
description: Paced-mode TryRecv BLOCKS; judge blocking-ness from paced_wire.go implementation, not the Try/ok call-site idiom
metadata:
  type: feedback
---

Paced-mode `TryRecv` BLOCKS; it is not a poll. The paced branch in `nodes/Wiring/ports.go` calls `paced_wire.go` `Recv`, which selects on `slotReadyCh` with NO default → parks until the clock delivers; `ok=false` means ctx-cancelled, not "nothing available." The `if v, ok := port.TryRecv(); ok {` call-site idiom looks non-blocking (Go Try/comma-ok convention) and misled a subagent into mis-reporting i0 as a per-round poll. Rule: judge blocking-ness from the paced_wire.go implementation, not the call-site idiom. Consequence: an unseeded feedback ring deadlocks at t=0 (read-before-send parks forever) — why in08 must send before it reads.
