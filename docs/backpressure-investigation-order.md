# Backpressure / concurrency investigation — branch order

Recommended order for turning the seven investigation-stub branches (each currently a single
`docs/*.md` planning doc, no code yet) into implementation. Rooted in the A/B/C plan in
the backpressure-ceiling investigation doc, which lives branch-local on
`task/backpressure-ceiling-investigation` (not on main).

This is a coordination note, not a spec — re-check each branch's own doc before starting, and
confirm no concurrent session is on a branch before touching it (these were spun up together
and branches have flipped under an active worktree).

## Order

1. **`test-clock-injection-before-ci`** — prerequisite. A deterministic, injectable clock seam
   so timing/concurrency tests run reproducibly. Its name declares it comes *before* CI;
   everything timing-gated below depends on it (cf. the recent de-flake of
   `TestDecentralizedNodeMove`, which fought real-clock nondeterminism).
2. **`concurrency-guards-and-ci`** — option A in the root doc, "cheapest, highest signal": a CI
   guard that fails the moment a topology/clock change pushes a wire over the
   `wireChanBufferSize=4096` ceiling. Needs the test-clock seam from (1) to be deterministic.
3. **`goroutine-leak-test`** — leak test, runs under the CI harness from (1)+(2).
4. **`sends-per-cycle-benchmark`** — option C: derive/measure worst-case sends-per-cycle to
   prove the buffer bound. Runs under the same harness.

Independent hardening (no ordering dependency — do anytime):

- **`buffer-stream-version-handshake`** — stream version handshake between Go and the ext host.
- **`failfast-doctrine-consistency`** — align fail-fast doctrine across the tree.

## Tension to be aware of

The root doc calls the CI *guard* (item 2) the highest-signal item. If fastest payoff is
wanted over clean ordering, start there — but it will likely pull in the test-clock seam (1)
anyway, so (1)-first avoids doing that work twice.
