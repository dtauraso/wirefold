---
branch: task/code-smell-cleanup-2
---

# Firing-error demo topology

A minimal 3-node network staged to **deterministically trigger the firing-error
torus** (the `node-status` `torusRed=true` state + missed-bead marker) so you can
watch it in the live editor. In the default `topology/` this error never fires
naturally — nothing arrives mis-timed — so this demo forces the condition.

## The network

```
src (Input, alternating 0,1, repeat)  ──short wire──▶  h (HoldNewSendOld)  ──LONG wire──▶  snk (HoldNewSendOld)
```

- **src** emits `0,1,0,1,…` forever (`data.init=[0,1]`, `repeat=true`). It
  self-paces: each emit blocks until its `src→h` bead finishes transit, so beads
  enter `h` roughly one *short*-wire latency apart.
- **h** is a `HoldNewSendOld`. When it fires, it drives its output bead down the
  **long** `h→snk` wire. Its *processing window* stays open until that output bead
  finishes transit.
- Because `h→snk` is far longer than `src→h` (centers 1000 apart vs. 60), the
  window is wide: src's **next, different-color** bead lands on h's input port
  while h is still processing. Per the model that is a firing error — h flips its
  torus **red**, shows the missed bead just outside itself, discards it, and
  reverts to normal when the long output transit finally completes.

## Timing rationale (why it always fires)

Let `T_in` = `src→h` transit and `T_out` = `h→snk` transit. The chord lengths give
`T_out ≈ 25 × T_in`. Trace of the deterministic start (h starts `held=-1`):

1. src emits `1` → h receives it; `prevHeld=-1` is the empty sentinel, so h places
   **no** output bead → no window. h's held becomes `1`.
2. src emits `0` → h receives it; now `prevHeld=1` is real, so h drives an output
   bead on the long wire and **opens a window** of length `T_out`. h's held→`0`,
   so the window's last value is `0`.
3. src emits `1` (one `T_in` later) → it arrives at h **inside** the still-open
   window (`T_in ≪ T_out`). `1 ≠ 0` → **firing error**, `torusRed=true`,
   `missedValue=1`. The bead is discarded (never processed).
4. When the long output transit completes, the window closes → `torusRed=false`
   revert.

The one permitted duration is wire transit time; nothing here uses a timer. The
error is a pure consequence of geometry (`T_out ≫ T_in`) plus the alternating feed.

A headless proof of this exact scenario (same values, same wire-length ratio,
driven by the real node loops under a fake clock) lives in
`firing_error_demo_test.go` → `TestFiringErrorEmittedEndToEnd`.

## How to watch it in the editor

The editor's "Topology: Open Editor" command opens whatever `topology*` folder you
launch it on (the explorer context menu matches any folder whose name starts with
`topology`). To watch the demo **without touching the real `topology/`**:

1. In the VS Code Explorer, **right-click the `topology-firing-demo/` folder**.
2. Choose **"Topology: Open Editor"**.
3. Press **Play** (the global play/pause gate resumes the clock).
4. Watch node **h**: after the first couple of beads it flips to a **pulsing bright
   red ring** with a large glowing **missed-bead marker** just outside it (the
   discarded `1`). Because the `h→snk` wire is long, the red state is held for many
   seconds before it reverts — plenty of time to see it — then it re-fires on the
   next window.

> If you changed `package.json`'s menu `when` clause, VS Code needs
> **"Developer: Reload Window"** once for the context menu to appear on the demo
> folder (a package.json/extension change reloads the extension host, not just the
> webview).
