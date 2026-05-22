# Visual Topology Tool: design surface for the Go code

The active plan. The tool is the **design surface where decisions about
the Go code get made**. You sketch the topology visually because sketching
is how you decide what the Go code should be; `topogen` mechanically
translates that decision into Go; the Go code is the deliverable.

```
visual editor  →  topology.json  →  generated Go  (topogen)  →  running system
                                                                       ↓
                                                              (future) trace
                                                                       ↓
                                                            replays into editor
```

Editing the diagram is editing the spec; editing the spec regenerates code.
Trace replay is the rightmost arrow; deferred until the visual → spec → Go loop runs smoothly.

**Opus 4.7 effort level: Low** for the entire visual-editor plan. All
per-phase / per-chunk $ estimates and actuals throughout this plan and
its phase docs assume that setting.

## How this doc is organized

This index covers cross-cutting decisions and a phase table. Each phase has
its own file under [visual-editor/](visual-editor/). Load only the phase(s)
you're working on.

| Phase | Status | File | Cap est. | \$ extra-usage est. |
|---|---|---|---|---|
| 1 — pipeline foundations | ✅ done | phase-1 | ~2 | ~\$120 |
| 2 — recall affordances | ✅ done | phase-2 | ≤2 | ≤\$120 |
| 3 — structural editing | 🟡 ~½ remaining (3 Tier-3 follow-ups) | phase-3 | — | ~\$3 left |
| 4 — fold/unfold | ✅ done; nested ⏳ | phase-4 | — | ~\$3 nested |
| 4.5 — plugin hardening (audit) | ✅ 4.5.1–4.5.5 done; 4.5.6 opportunistic | phase-4.5 | — | ~\$13.47 actual |
| 5 — comparison | ✅ done (incl. follow-ups) | phase-5 | — | ~\$8.05 actual |
| 5.5 — animation model rewrite | ✅ done | phase-5.5 | — | \$21.26 actual |
| 6 — node motion (state-derived) | ✅ done | phase-6 | — | \$5.04 actual |
| 7 — trace replay | ✅ chunks 1–5 done; Phase 8 (per-node parity) chunks 1–11 also done | phase-7 | — | \$11.57 + \$8.69 actual |
| 8 — polish (undo, snap, e2e) | ✅ chunks 1–3 done; Tier 4 nightly latency deferred | phase-8 | — | \$4.69 actual |
| 9 — SVG diagram parity | ✅ done | phase-9 | — | \$4.93 actual |

**Cap-hit column dropped.** Phase 5 came in at ~\$5.65 against a \$110 estimate (~5% of midpoint, ~18× overestimate). The cap-hit unit was calibrated against an older model and a less mature codebase; with Opus 4.7 + the existing harness/adapter/save infrastructure, mechanical phases run roughly an order of magnitude under the original budget. \$ figures above are the post-Phase-5 recalibration; cap-hit estimates are no longer load-bearing.

**\$ totals (remaining):** ~\$135 midpoint to ship Phases 3 → 9 (Phase 4.5 now done at \$13.47 actual), range ~\$90–\$265 depending on whether the refactor/exploratory phases (5.5, 6, 7) hit their risk-case multipliers. Mechanical phases (3-leftover, 4-nested, 8, 9) scaled at ~10% of original estimate; hardening (4.5) landed at ~12% (audit work has more codebase exploration than pure-function authoring); refactor (5.5, 6) and exploratory (7) at ~15–20% with wider risk bands since the Phase-5 efficiency factor may not generalize fully to less-scoped work.

## ▶ v0 closeout

The `visual-editor` branch is being merged to `main` as **v0**. What
that means honestly:

- **Bar passed: hello-world low.** Changing a label in the editor
  produces an updated value in the regenerated Go. Pulses animate.
  Some labels show distinct in-flight values in at least one place.
  Those very basic things work end-to-end.
- **Real-world testing has not started.** No real topology change
  has been driven through this tool in actual use yet. The
  hello-world bar exercises the spec → Go round-trip surface and
  the comprehension surfaces (animation, undo, fold) but does not
  stress them at the volume or shape a real design session would.
- **Cumulative spend through v0:** ≈\$80 across Phases 1–9 + Tier 4
  latency. See per-phase docs for actuals.
- **Expectation:** real-world use will likely surface rewrites and
  redesigns. The remaining items in [NEXT UP](#-next-up) and the
  per-phase follow-ups are *candidates*, not commitments —
  re-evaluate them against actual workflow needs once real-world
  sessions begin, rather than implementing speculatively.
- **Branch hygiene:** `visual-editor` is being closed out at this
  point so cumulative risk doesn't keep stacking on a single
  long-lived branch. The next branch should be named after a
  specific real-world task, not after a phase number.

## ▶ Posture after v0: friction-driven, not phase-driven

Plan-driven phase work is **paused**. v0 passed a hello-world bar but
real-world testing has not started, so further speculative phase work
risks repeating exactly the trap that made v0 hard to evaluate (lots
shipped, value unproven). New posture:

- **Friction is the input.** The user drives the editor in real
  sessions; observations get logged as they happen to
  [visual-editor/session-log.md](visual-editor/session-log.md) — append-only,
  brief, concrete ("tried X, hit Y, took N min / felt awkward /
  produced wrong Go"). Sessions accumulate before any rewrite is
  proposed.
- **Candidate pool, not commitments.** The items below under
  *Candidate follow-ups* and the per-phase follow-ups in each phase
  doc are now **candidates** to be re-justified against logged
  friction, not work to be picked up speculatively.
- **Smoothness gates structural editing.** Before structural-edit
  sessions begin, non-edit interactions (pan/zoom/drag/playback/
  scrub/fold/replay/view recall) need to feel smooth. The first
  real-world session is a smoothness audit, not a topology change.
- **Audits replace phases as the periodic concern.** See
  [visual-editor/audits.md](visual-editor/audits.md) for the registry
  of audit categories (security, code smells, complexity,
  architectural tradeoffs, project-specific invariants like
  goroutine leaks and backpressure discipline, etc.).
- **Working mode.** User drives the editor and narrates; assistant
  logs to session-log.md and makes changes; debug sessions as needed.
- **Cost-marker rule.** Per CLAUDE.md, only record cost markers on
  work sized ≥$5 expected. Sub-$5 commits land without markers.
  Bundle small work into ≥$5 chunks for marker purposes. Pre-v0
  sub-$5 markers stay as historical record.
- **Branch hygiene.** Task-named branches (`task/<short-kebab>`)
  that merge to `main` quickly. No more long-lived feature branches.
- **Per-commit sign-off relaxed** (CLAUDE.md). Assistant commits and
  pushes freely on task branches; sign-off still required for
  merges to `main` and destructive/shared-state actions.

## ▶ Candidate follow-ups (re-justify against session log before picking up)

The most load-bearing items remaining from v0, in rough priority order.
**Do not pick these up speculatively.** Each is a candidate to be
re-justified against actual friction logged in session-log.md before
being scheduled as work.

1. **Phase 8 documented gap — rename + node-delete vs undo.** Surfaced
   at Phase 8 chunk 3: rename and node-delete mutate spec and
   viewerState atomically and aren't captured by the two-stack undo
   design. Trust property; closest-to-load-bearing of the open items.
2. **Phase 8 deferred — Tier 4 nightly latency test.** Headline
   edit-to-running-Go latency test was deferred at Phase 8 chunk 2
   sign-off. Natural Phase 8 closeout.
3. **Phase 4 nested folding follow-up [~\$3].** Single-level folds work;
   pick up only when a real topology hits the level-of-nesting wall.
4. **Phase 3 Tier 3 follow-ups [~\$3].** Three queued cases
   (port-drag → chan, palette-drag-position, port-drag mismatched-kinds
   fallback). Opportunistic.
5. **Phase 4.5.6 — audit lows & nits [opportunistic].** Pick up while
   touching adjacent code; not a planned spend. See [phase-4.5.md](visual-editor/phase-4.5.md).

**Recently shipped:** Phase 5.5 (animation model rewrite, \$21.26),
Phase 6 (state-derived node motion, \$5.04), Phase 7 chunks 1–5 (trace
replay, \$11.57), Phase 8 per-node Go↔TS parity chunks 1–11 (\$8.69,
every gate-shaped node now covered), Phase 8 polish chunks 1–3
(\$4.69; Tier 4 nightly deferred), and Phase 9 SVG diagram parity
(\$4.93 across chunks `6a2316e` / `bc1ff39` / `edd80b8` / `f0477c8` —
edge route dispatch, house-style vocabulary, notes round-trip, Tier 4
visual baselines).

## What the tool is for, in priority order

1. **Letting design decisions become code fast.** A change in the visual
   editor should produce updated Go within seconds. This is the load-bearing
   value of the tool.
2. **Holding the design stable across sessions** so you can come back to it
   without re-deriving. Spatial memory + saved views + bookmarks make the
   diagram a durable working surface.
3. **Replaying the dynamic story** as a refresher dose for "what happens
   when input arrives." Animation is comprehension, not decoration.
4. ~~**Producing diagram artifacts** (SVG export).~~ *Dropped — see Phase 8.*

When design conflicts arise, tie-break toward whichever serves design
throughput best.

## Spec vs viewer state

Two storage surfaces:

- **`topology.json` — the spec.** Round-trips through `topogen`. Every field
  in this file is something the generated Go code reads or depends on.
  Nothing else belongs here.
- **`topology.view.json` — viewer state, sidecar.** Saved views, bookmarks,
  fold/unfold state, last camera position, animation playback preferences.
  `topogen` ignores this file.

Whether keyframes are spec or viewer is a judgment call: if the moving /
rewiring is *part of the simulation's behavior* (the Go runtime causes
it), keyframes belong in the spec. If it's purely a presentation animation,
viewer state. Default: spec, on the assumption that animated change reflects
real Go-side change.

### Spec fields

The existing schema (nodes, edges, ports, kinds, roles, `timing.steps`)
plus, when needed: `positionKeyframes`, `endpointKeyframes`, `visibility`
keyframes.

### Viewer state fields

```jsonc
{
  "views": [
    { "name": "detector subsystem",
      "viewport": { "x": 200, "y": 50, "w": 600, "h": 400 },
      "nodeIds": ["sbd0", "sd0", "sbd1", "sd1", "a0"] }
  ],
  "folds": [
    { "id": "fold-detectors", "label": "detectors",
      "memberIds": ["sbd0", "sd0", "sbd1", "sd1"],
      "position": [600, 100], "collapsed": true }
  ],
  "bookmarks": [{ "name": "ack returns", "t": 0.913 }],
  "camera": { "x": 0, "y": 0, "zoom": 1.0 },
  "lastSelectionIds": ["i0"]
}
```

## Codegen integration

The pipeline is broken if editing the diagram doesn't produce running Go.
`topogen` is in the editing loop from day one (Phase 1).

**On every spec save:**
1. Plugin invokes `topogen` (debounce ~250ms, never overlap; queue latest).
2. Output Go files are written to their canonical location.
3. Status indicator: green (synced), amber (regenerating), red (error).
4. Errors from `topogen` should appear inline near the offending node/edge
   (deferred — currently bare strings).

**Build / run:** one-click "▶ run" invokes `go run ./cmd/...` and surfaces
the result.

## Editing decisions that affect code shape

Some authoring gestures are declarations `topogen` will translate into Go:

| Gesture | What `topogen` will generate |
|---|---|
| Drag-add a node | New struct + goroutine + run-loop |
| Drag-connect a port | New channel; type inferred from port spec |
| Set port direction | Channel direction in struct field |
| Pick node role | Struct embedded type or behavior preset |
| Set channel buffer size (advanced) | `make(chan T, N)` capacity |
| Mark edge as feedback-ack | Generated wiring that closes the loop |

The editor is a **declarative front-end for code generation**, not a paint
program. Every gesture has a code consequence.

## Design criteria

- **Codegen latency under one second.** Beyond ~one second the loop breaks
  down and the editor feels like a documentation tool.
- **Stable spatial layout** across sessions. Re-load relies on spatial
  memory; never auto-shift positions.
- **Spec-vs-viewer cleanliness.** No viewer state in `topology.json`.
- **Glanceability.** Color = role, shape = role-class, edge style = kind.
- **Saved views, bookmarks, fold/unfold** for re-load fluency.
- **Errors surface where they happen.**

## Rendering substrate: React Flow inside the vscode webview

The previous standalone `tools/topology-editor/` (browser, React Flow) was
deleted because wiring Claude Code chat into a browser would have been more
work than building a vscode plugin. React Flow itself was not the problem.
Adopting React Flow **inside** the existing vscode webview — with `topogen`
still authoritative — replaced large parts of Phases 3, 4, and 8 with
library-provided primitives:

- **Phase 3:** selection, port-drag edge creation, node palette — built in.
- **Phase 4:** fold geometry — RF subflow primitive covers most of it.
- **Phase 8:** undo/redo — `zundo` Zustand middleware.

What stays custom: `topogen` invocation, spec/viewer split, animation
timeline + bookmarks, keyframed motion + record-mode editor, trace replay.

## What success looks like

1. **Edit-to-running-Go test.** A small structural change reaches running
   Go in under 30 seconds end-to-end. Headline number.
2. **The five-second test.** Two weeks away, you open the diagram and the
   topology snaps back into your head in five seconds.
3. **The change-and-recompare test.** A small change a day ago is
   immediately visible when you re-open.
4. **The "show me just X" test.** One click frames a subsystem and dims context.
5. **The "what happens at moment Y" test.** One click jumps to a named
   transition in the animation.
6. **The decision-feels-like-sketching test.** Modifying the topology
   feels like sketching, not data entry.
7. **(Phase 7) The drift test.** Replay a trace next to the spec animation;
   any disagreement is visible.

## Status snapshot (this branch)

Phases 1, 2, 4, 4.5, and 5 complete. Phase 3 substrate migration done;
remaining is three Tier-3 e2e follow-ups. Phase 4.5 hardening shipped
at \$13.47 actual against ~\$15 post-recalibration estimate (~12% of the
original \$210 audit estimate; mechanical-phase factor is ~10%, hardening
ran slightly higher because audit work involves more codebase
exploration than pure-function authoring). 4.5.6 lows & nits remain
opportunistic. Phase 5 shipped at ~\$5.65 actual against \$110 estimate
(see commits `74e6abc`, `153c74d`); both follow-ups (collapsed-fold
diff badge `be4beef`, dim halo punch-through `bb0ac48`) shipped at
\$2.40 — Phase 5 total \$8.05. The visual → spec → Go pipeline
runs on every save with a status indicator; recall affordances (saved
views, bookmarks, playback) in place; comparison vs. HEAD or second file
in place.

Phase 1 alone changed the tool from "live preview" to "design surface."
Phases 2–3 made it a durable design surface. Phases 4–6 are recall +
dynamics power-tools. Phase 7 is observed-vs-intended. Phase 8 is comfort.

## What the AI does in this loop

The tool's value is *yours* — the topology you design and the system that
emerges from it. The AI's contribution is two-sided:

- **Inside the tool's construction:** picking implementation pieces,
  translating between your conceptual model and the rendering / persistence
  / codegen layers, maintaining boring invariants so they don't bleed time.
- **Inside the design loop the tool enables:** once the pipeline is smooth,
  the AI participates as a collaborator on topology design — you propose a
  structural change verbally or by sketching, the AI reasons about
  consequences, the editor makes the change concrete, `topogen` produces
  the code, you run it and see.

What the AI doesn't do: decide what the topology means, or which design
direction is interesting, or what the system is *for*.
