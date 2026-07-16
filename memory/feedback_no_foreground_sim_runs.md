---
name: Never run the sim in the foreground; bound or background it
description: The sim and anything parked on a paced wire can fail to exit; a foreground run-and-grep blocks until the harness limit. Background it or wrap in tools/run-bounded.sh, and keep these checks in the main session.
type: feedback
---

A subagent once hung for 13 minutes on a verification step that ran the
sim binary in the foreground (`./wirefold … -duration 1s`). The sim — and
anything parked on paced-wire delivery — does not
reliably exit on its own, and macOS has no `timeout`, so the `Bash` call
blocked until the harness limit.

**Why:** verification commands that can block are a latent hang; the cost
shows up as a long, silent stall the user has to interrupt.

**How to apply:**
- Never run the sim in the foreground. Run it **backgrounded** (the runtime
  streams the trace live and re-invokes you on exit), or wrap any
  potentially-blocking command in `tools/run-bounded.sh <seconds> <cmd…>`.
- Capture geometry/startup events from the streamed trace file, not by
  waiting on process exit — startup geometry is emitted during
  `LoadTopology`, before any pacing.
- Do **not** delegate a single run-and-grep verification: keep it in the
  main session where you control backgrounding/kill, and delegate only the
  editing.
- The durable fix landed too: `runTopology` (main.go) now bails out of
  `wg.Wait()` on ctx cancellation (resume clock + short grace), so
  `-duration`/SIGINT runs exit instead of hanging.

Relates to [[feedback_paced_tryrecv_blocks]] (paced TryRecv blocks) and
[[feedback_cost_overruns]] (silent stalls as a cost pattern).
