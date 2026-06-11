---
name: local clocks vs global runner (one factor among several)
description: Per-PulseInstance clock locality contributed to the easy pause-freeze fix, but recency, small surface, simple problem shape, and written-down contracts also contributed. Don't use ease-of-fix as a single-factor signal.
type: project
---

**Fact (2026-05-07):** The pause/resume mid-arc fix landed in one commit (`34b8c20`). Each `PulseInstance` owns its own rAF clock and freezes/rebases on a `subscribeWiresPause` signal — no global runner state involved on the matched path.

**Why it was easy — multiple factors, not just locality:**
- **Locality.** Per-instance clock means the fix doesn't have to reason about `sim/runner` + `legacyRunnerState` + `pauseRunner` + `isPlaying` together.
- **Recency.** Wires runtime was rebuilt days earlier; it's fresh, small, and the contracts are already named in comments/tests.
- **Surface area.** Wires runtime is small; legacy runner is split across many files. Easier to hold in head independent of architecture.
- **Problem shape.** Pause-as-freeze (capture `t`, rebase on resume) is simple in any runtime. A harder fix wouldn't have benchmarked locality as cleanly.

**How to apply:**
- Don't use "this fix was easy" as standalone evidence that legacy-removal is paying off, or "this fix was hard" as standalone evidence a global is in the way. Confounds (recency, file size, problem shape, whether contracts are written down) are usually present.
- When the user reports ease/pain on a transport fix, separate the factors before drawing a Go-layer conclusion. Ask which one the user means.
- Carry the conceptual frame forward regardless: **concurrent clocks frozen on command**, not a global clock. That framing is right on its own merits, separate from "fixes got easier."
