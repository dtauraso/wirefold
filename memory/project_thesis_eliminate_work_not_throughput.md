---
name: project-thesis-eliminate-work-not-throughput
description: Project thesis — the substrate optimizes for work that never has to happen (eliminated by structure), not throughput of jobs crammed onto a CPU
metadata:
  type: project
---

The substrate's objective function is the INVERSE of industry concurrency/dataflow systems. Industry (actor frameworks, reactive streams, FBP engines like Node-RED/NiFi, stream processors) optimizes the THROUGHPUT of a machine: given a fixed pile of jobs, maximize jobs-per-CPU-per-second via central schedulers, bounded queues, and thread pools. They optimize the numerator (jobs done).

This project optimizes the DENOMINATOR: how much work never has to happen because the wired structure already accounts for it. Because the topology IS the logic, the jobs a conventional system burns cycles on simply don't exist:
- the scheduling job (a central scheduler is itself a job bossing other jobs — self-pacing local nodes don't need it),
- the orchestration job ("wait for X then Y, retry, coordinate" — a consumeGated wire makes the handshake structural),
- the iteration job (winner-take-all by looping/comparing N — lateral inhibition just SETTLES, the answer falls out of the circuit),
- the bookkeeping job (global rounds/ticks/queue-depths — none exist because there is no global clock).

A queue + scheduler are not free; they are ADDED jobs taken on to manage existing jobs. It is closer to analog/physical computation: a circuit solves by settling into a state, doing zero jobs in the von Neumann sense (the retina does winner-take-all with no scheduler or queue).

**Why:** This is the layer beneath the CLAUDE.md "Medium vs. substance" rule and MODEL.md's banned global-clock/scheduler vocabulary. Industry primitives (queues decouple timing; central schedulers maximize CPU utilization) are the CORRECT answer for throughput data-processing but they exist precisely to abstract away the moment-to-moment local coordination that IS this project's subject.

**How to apply:** When framing substrate work, measure success by jobs AVOIDED, not jobs done. Reject throughput-oriented framings ("how do we make this faster / handle more / scale") as missing the point at the substance layer. A proposal that ADDS coordination machinery (a queue, a scheduler, a global tick, a buffer) is suspect — ask whether structure could make that work not exist instead. See [[feedback_substrate_vs_coordinator_bias]], [[project_local_clocks_beat_global_runner]], [[feedback_derive_model_from_visual_spec]].
