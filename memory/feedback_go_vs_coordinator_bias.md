---
name: go-vs-coordinator bias
description: Before proposing a fix in Go code, name the contract being violated, not the symptom. Default training-data bias is to tune knobs and add coordinators; resist it.
type: feedback
---

**Rule:** Before proposing a fix in Go code (sim/runtime, channel/wire model, ack/back-pressure, scheduler), name the **contract** being violated — not the symptom. If you're about to suggest tuning a constant (interval, timeout, cap, retry count, debounce), stop: that is almost always the wrong shape. Find the missing local signal instead.

**Why:** Training-data default for "X overlaps with Y" is "add a coordinator / slow one of them down / raise the cap." For this project, the answer is almost always "the receiver should signal completion locally, and the sender should wait on that signal." Channels back-pressure locally; gates back-pressure locally. Coordinator-shaped fixes are training-data drift away from the Go network the user is actually building.

**Concrete failure mode (2026-05-07, ack-driven emit) — reasoning error is durable, vocabulary is retired:** User reported tokens looked like `001` not `010`. I proposed (1) changing the init array, (2) bumping an emit-interval constant, (3) bumping an edge capacity. All three were knob-tuning. The actual bug: a timer shoving tokens regardless of whether the wire was clear, with the overflow silently dropped. The fix deleted the timer and released on a real local signal — the contract the capacity already implied. The runtime's own comment named the contract and I still walked past it twice before the user pushed back. (The specific mechanism here is from the retired pre-Go-clock runtime; cite it for the *shape* of the error — three knobs proposed, contract unread — not as a live description of the code.)

**How to apply:**
- When the user surfaces a Go-side symptom, the first response should name the contract (e.g. "cap=0 means sender blocks until receiver consumes; current code violates that by emitting on a timer"), not propose a knob.
- If a fix involves a magic number being tuned, treat that as a smell. The number exists because a contract is missing.
- "What does the Go side do?" is the load-bearing question. Channels and gates back-pressure with one sender, one receiver, and a local signal between them — no coordinator.
- Re-read this memory at the start of any Go-shaped task, not only when the user reminds you. The bias is the default; corrections are the exception.
