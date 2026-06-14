---
name: feedback-place-all-then-drive-concurrently
description: Place all outbound beads before driving them concurrently (DriveAll); serial per-edge drive causes fan-out timing regression
type: feedback
---

**RULE:** When folding async per-bead delivery into a SYNCHRONOUS node-driven model, a node with multiple outbound edges must PLACE all its beads first, then DRIVE them ALL together in ONE concurrent frame loop (DriveAll) — never drive edge-by-edge in series.

**WHY:** Driving per-edge in series blocks the node goroutine for the FULL traversal of edge 1 before edge 2's bead is even placed, so fan-out beads emit a whole edge-flight apart. The node-2 regression (2026-06-14) showed this as "node 2 sends no output" — really ToNext was delayed ~5s behind the feedback edge. Root-caused via SIGQUIT goroutine dump + headless trace step comparison (1326 regressed vs 1319 pre-regression), NOT by the initial hypotheses (FIFO cond.Wait, paced mis-detect, anyOccupied) which were all wrong.

**HOW TO APPLY:** place (no walker) → collect (wire,gen) handles → one DriveAll. Also: chan-mode unit tests don't exercise the paced drive loop, so verify drive changes by editor eyeball / headless trace, not the green suite.

See also: [[feedback_industry_bug_class_scan]], [[feedback_runner_errors_probe_first]]
