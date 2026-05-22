# Visual editor — real-world session log

Append-only log of friction surfaced while driving the visual editor. Newest first.

## Entry format

```
## YYYY-MM-DD — <short task description>

**Observation:** what the user noticed driving the editor.

**Hypothesis / scope:** quick read on what's likely going on.

**Decision:** start a task branch, log-only, defer, etc.

**Outcome:** what changed (or "logged only").
```

---


<!-- source: session-log/2026-05-10-step-mid-pulse-replaces-instead-of-blocking.md -->

## 2026-05-10 — step mid-pulse replaces in-flight pulse instead of blocking

**Observation:** During step-7c proof-out (`topology.frameRendererEnabled` on,
`spec.runtime: "ticked"`, shape `input->readGate`). Pressing Step
advances input one item at a time, in order. Pressing Step again
*before* the pulse animation finishes does not block — the in-flight
pulse is replaced on the wire by the next value.

**Diagnosis:** Not a substrate cap-1 violation. Trace:
  - `step()` runs nodes in topological order. `inputRunner.run` calls
    `ctx.send(edge, v)` → `publishEdgeArrive(edge, v)` → push to slot.
    `readGateRunner.run` (same tick) drains the slot via `recv`.
    Between ticks the slot is empty, so the next Step's send is legal
    (no throw, no block).
    [ticked/shape-a.ts:21-38](../../../tools/topology-vscode/src/substrate/ticked/shape-a.ts#L21-L38),
    [ticked/runtime.ts:38-48](../../../tools/topology-vscode/src/substrate/ticked/runtime.ts#L38-L48).
  - `send` publishes `edgeArrive`; `recv` publishes nothing. The
    painter only hears arrive events and binds the wire's "carrying"
    visual to the latest value. Two arrives in quick succession ⇒
    visual replacement.

**Why this surfaced now:** Step 7c (`168675e` painter) just landed
and is the first time per-wire painting consumed FrameMsg from the
ticked substrate. The pacing contract — what the painter does when
arrive events outrun the pulse animation — was never specified.
handoff-substrate-iteration assigned pacing to the renderer; 7c
built the painter as a stateless mirror of the latest event, which
is one valid renderer but not a *pacing* one.

**Decision:** Log only; pacing is a renderer-side choice and needs
the user's call. Three options worth weighing:
  1. **Animation = one tick.** Pulse duration is tied to whatever the
     user perceives as a step. No queue needed; replacement becomes
     impossible because the previous pulse has visually finished by
     the time Step returns.
  2. **Disable Step while animating.** Simple, preserves the longer
     pulse animation, but couples UI input to renderer state.
  3. **Queue arrives in the painter.** Subsequent arrives play in
     order after the current pulse finishes. Preserves animation
     length and keeps Step responsive, at the cost of renderer state
     drifting behind substrate state — visually misleading once
     drift exceeds one or two pulses.

Option 1 is the most model-coherent (substrate is timing-free; the
"step" is the pacing unit). Option 3 reintroduces a queue the
substrate explicitly doesn't have.

**Outcome:** logged only; awaiting the pacing decision.


---

<!-- source: session-log/2026-05-10-readgate-ack-button-torn-out-fragile-gating.md -->

## 2026-05-10 — readgate-ack-button torn out: gating must be schema-enforced

**Observation:** With minimal topology `in08 (Input) → readGate1
(ReadGate) on chainIn`, Input pulses cycle forever. User expected the
"slot" (chainIn wire's `loaded` phase) to fill and Input to
backpressure until something clears it.

**Hypothesis / scope:** ReadGate's schema declares two inputs
(`chainIn` and `ack`). `runNode` parks on `awaitAll(awaitLoaded)`
over inputs **built from spec edges**, not from declared ports. With
only `chainIn` wired, runNode treats ReadGate as a one-input node:
takes chainIn immediately, loops, Input keeps emitting. The gating
the user expected only exists if `ack` is wired.

**Decision:** Branch `task/readgate-ack-button`. Added a new `Button`
node type (zero inputs, one output, awaits manual `fire()` instead of
a seed queue), wired `btn1 → readGate1.ack`, plumbed a `fire-button`
webview→host message through to a new `runFrames.fireButton(nodeId)`
handle, made the Button node clickable in the webview. All gates
green; gating worked while wired.

**Outcome:** Torn out and branch deleted (local + remote). The fix
ran in the right direction (no substrate change needed — the wire's
three-phase state machine and `awaitAll(awaitLoaded)` already do the
gating) but the design silently regressed the moment a user deleted
btn1 or its edge: parseSpec accepts a ReadGate with no ack wire,
runNode falls back to one-input behavior, pulses flood again. No
error, no warning. User rejected the posture as too fragile and asked
for a model-enforced design instead.

**Lesson saved:** `memory/feedback_enforce_required_inputs.md` — when
a node type's correctness depends on an input being wired, mark it
required at the schema and fail parseSpec if missing. Don't ship the
gating without the validation.

**Next task (handoff-next-task.md):** schema-level required-input
enforcement. Add `required?: boolean` to Port; mark `ReadGate.ack`
required; validate in parseSpec; update the spec to satisfy the
constraint. Button / manual-ack UX is parked, not killed — revisit
once enforcement is in.


---

<!-- source: session-log/2026-05-07-bundle-hot-reload.md -->

# 2026-05-07 — Bundle hot-reload in place (Reload Window obsolete)

## Friction

VS Code's "Developer: Reload Window" did not pick up freshly built
`out/webview.js`. The prior workaround (close+reopen the topology
tab) was manual and disrupted any in-flight editor state.

## Cause

Two bugs stacked:

1. The dev-mode `bundleWatcher` was created with an absolute path
   string. VS Code treats string globs that aren't relative to a
   workspace folder as never-matching, so the watcher never fired.
2. Even when HTML was re-rendered, the webview-resource URI for
   `webview.js` had no cache buster, so VS Code's webview cache
   served the previous bundle.

## Fix (commit d7983ab)

- Switched watcher to `RelativePattern` rooted at the extension's
  `out` dir.
- Stamped `?v=<mtime>` onto the script/style URIs in
  `buildWebviewHtml`.
- Watcher action stays as `panel.webview.html = build...` (debounced
  150ms), no dispose+reopen.

## Workflow now

Edit → `npm run build` → topology tab refreshes in place. No Reload
Window, no tab cycling. Logs at Output → Log (Extension Host):
`[topology] bundleWatcher fired: change` and
`hot-reload: re-rendering webview.html`.

## Side observation surfaced during this work

Cold-open and any in-editor doc edit (e.g., renaming a node) trip a
stuck-pulse: substrate emits the first pulse, AE re-subscribes
after `ae-received`, ack never returns, loop stalls. This is the
new blocker for port-plan step 2 — see
[../handoff-next-task.md](../handoff-next-task.md).


---

<!-- source: session-log/2026-05-05-pulse-leak-resolved-new-bugs-from-the-abstraction-split.md -->

## 2026-05-05 — pulse-leak resolved; new bugs from the abstraction split

**Branch:** task/pulse-animation-abstraction (4d4ae63)
**Mode:** done; handoff written.

The pulse-leak-investigation root cause was identified: not a
defer-mode counter regression but a fold-vs-defer ownership gap.
PulseInstance owned the slot-release bridge; folded edges suppressed
PulseInstance; bridge never fired; readGate's chainIn slot was held
forever; chainIn declined indefinitely waiting for an ack queued
behind the held slot.

Fix lifted lifecycle ownership to a runner-layer module
(`src/sim/runner/pulse-lifetimes.ts`), subscribed to `notify(emit)`
at webview boot. Contract C6 pins the new invariant; contract C4
inverted to pin that PulseInstance must NOT touch activeAnimations.
204/204 tests pass. Three time-spaced probes confirm cycle advances
5 → 7 → 13 across 30s where it previously froze at 1.

**Two new bugs introduced by the design.** The lifecycle clock (2s
default) is decoupled from visual duration (~10s on the longest
edge). On `i1.out->readGate.ack`, dump 3 captured 6 simultaneous
PulseInstance components (IDs 49, 54, 57, 64, 69, 72) plus
`msSinceLastFrame: 1615ms` (rAF normally ~16ms). Visual stacking
and frame stall.

User accepted these as trade-offs to ship the livelock fix and
asked to wrap with a handoff documenting follow-ups. Three
candidate directions (A renderer-authoritative completion, B
per-edge visual concurrency cap, C shorten long routes) recorded
in handoff.md. A is the principled answer; B is defense in depth;
C doesn't generalize.


---

<!-- source: session-log/2026-05-04-fold-halo-bug-runtime-error-probe-pattern.md -->

## 2026-05-04 — fold halo bug + runtime-error probe pattern

**Branch:** main (single-session work, landed in 73007f4 + follow-ups)
**Mode:** friction-driven fix during real-world fold/halo iteration

Symptom chain (user-visible, in order of report):
1. Fold node's halo "not happening" — turned out to be a misread of
   what was visible (perimeter halo too subtle vs. dashed border).
2. After moving to a port-dot halo: "halo always off" + "pulses
   decoupled from play/pause" + "fold mode halo strobe."

Root cause: `createFoldActivityTracker`'s default `setTimer` was
`{ set: setTimeout, clear: clearTimeout }`. When invoked as
`setTimer.set(fn, ms)`, JS binds `this = setTimer`, and browsers'
`setTimeout` requires `this = window` → throws
`TypeError: Illegal invocation` on every member fire.

The throw propagated up through the runner's `notify()` (no try/catch
at the time), aborted the listener loop mid-iteration, and from
`stepOnce()` up into `play()` — which set `playing = true` *before*
the throw and never created the interval. Result: button shows pause,
no ticks, no pulses, no halo. All four symptoms from one bug.

Fix (single line): wrap the defaults so the calls invoke
`setTimeout(fn, ms)` with the correct global binding.
Regression test added at
[fold-activity.test.ts](../../../tools/topology-vscode/test/fold-activity.test.ts)
("noteFire and decay run without 'Illegal invocation' on real timers").

### Diagnostic infrastructure that paid for itself

The bug was diagnosed by reading
`../../../.probe/runner-errors-last.json` directly — the user never had to
copy a console trace. The probe was set up earlier in the same
session as a quasi-automation play after the user asked whether I
could run the diagnostic steps myself (I can't drive the VS Code
webview UI, but I can route caught throws to disk).

Pattern: any caught exception in a webview listener → push to
`window.__runnerErrorsLog` → debounced postMessage to host →
`../../../.probe/runner-errors-last.json`. Eager init in
[runner.ts](../../../tools/topology-vscode/src/sim/runner.ts)
(`reportRunnerError` + `__runnerErrorsDump` globals) so the bridge
is alive before any error fires. Mirrors the pulse-probe and
fold-halo-probe lifecycles.

**Followups (open the next time something throws and the runner
state goes weird):**
- First check: `cat .probe/runner-errors-last.json`. If it has a
  stack, you're 80% done.
- Then: `cat .probe/fold-halo-last.json` for the halo timeline (mount
  / start / end transitions; verbose `fire` entries gated behind
  `window.__foldHaloProbeVerbose = true`).
- The runner now isolates listener throws so a buggy subscriber can't
  take the simulator down — but a thrown listener is still a
  *correctness* bug for that subscriber. Don't ignore the probe entry.

**Lesson logged for future sessions:**
- When a webview UI symptom is "X stuck" + "Y decoupled" + "Z
  missing" simultaneously, the cheapest first move is to check the
  runner-errors probe — these are usually one upstream throw, not
  three independent bugs.
- When adding a method to a config object, never put a globally-bound
  function (`setTimeout`, `clearTimeout`, `addEventListener`,
  `requestAnimationFrame`, etc.) directly as the value. Wrap in an
  arrow so the global `this` is preserved.


---

<!-- source: session-log/2026-05-03-industry-standard-pattern-review-visual-editor.md -->

## 2026-05-03 — industry-standard-pattern review (visual editor)

**Branch:** task/industry-pattern-review
**Mode:** AI-driven audit (CLAUDE.md "what did the rest of the world
converge on?" rule). No implementation this session — output is a
coverage matrix and triage of which gaps merit task branches.

Reference set: yEd, draw.io (mxGraph), ELK, the React Flow ecosystem
(incl. xyflow Pro examples), JointJS. Patterns surveyed are the
ones a typical graph-editor user expects on first contact.

### Coverage matrix

| Pattern | Have it? | Where / gap | Rough effort |
|---|---|---|---|
| Pan / zoom / fit-view on load | Yes | [app.tsx:892](../../../tools/topology-vscode/src/webview/rf/app.tsx#L892) (`fitView`), `minZoom 0.1`, `maxZoom 4` | — |
| Snap-to-grid | Yes | [app.tsx:879-880](../../../tools/topology-vscode/src/webview/rf/app.tsx#L879-L880) (`GRID=24`) | — |
| Alignment guides during drag | Partial | [app.tsx:681-707](../../../tools/topology-vscode/src/webview/rf/app.tsx#L681-L707) — single-node only; multi-node selection drag clears guides intentionally | S — extend to bbox of selection |
| Marquee / lasso selection | Yes | `selectionOnDrag`, `SelectionMode.Partial`, `panOnDrag={[1]}` ([app.tsx:895-897](../../../tools/topology-vscode/src/webview/rf/app.tsx#L895-L897)) | — |
| Multi-select drag | Yes (RF default) | — | — |
| Port-anchored handles | Yes | `sourceHandle`/`targetHandle`; 1-to-1 input invariant enforced ([app.tsx:474-482](../../../tools/topology-vscode/src/webview/rf/app.tsx#L474-L482)) | — |
| Edge reroute (drag endpoint) | Yes | `onEdgeUpdate*` handlers | — |
| Orthogonal routing | Partial | `EdgeRoute = "line"\|"snake"\|"below"` ([schema.ts:62](../../../tools/topology-vscode/src/schema.ts#L62)); snake is orthogonal but **sharp corners** ([AnimatedEdge.tsx:155-167](../../../tools/topology-vscode/src/webview/rf/AnimatedEdge.tsx#L155-L167)) | S — replace `L` with rounded-corner arcs / `Q` |
| Rounded corners on orthogonal edges | **No** | sharp 90° corners only | folded into above |
| Auto-routing (avoid node overlaps) | **No** | corridor offset is fixed `+40`; no obstacle awareness | L — adopt a router (ELK, libavoid-js) or accept `route` field as authoritative |
| Edge labels | Partial | edges have a `label` (Go identifier, not display label); not rendered on canvas | M — render display label on `BaseEdge`, anchor near midpoint |
| Edge-label collision avoidance | **No** | n/a until labels render | M (after labels) |
| MiniMap / overview | **No** | no `MiniMap` import; only `Controls` | XS — drop in `<MiniMap />` |
| Zoom-to-fit shortcut | Partial | bridge exposes `fitNodes(ids)` ([app.tsx:255-261](../../../tools/topology-vscode/src/webview/rf/app.tsx#L255-L261)) but no global `f` / `cmd-1` keybinding | XS — wire keybinding |
| Zoom-to-selection | Partial | `fitNodes` works on selection via bridge, no keyboard hook | XS |
| Undo / redo | Yes | scoped stacks (spec / viewer), gesture-aware via `data-undo-scope` ([app.tsx:140-236](../../../tools/topology-vscode/src/webview/rf/app.tsx#L140-L236)) | — |
| Undo grouping at gesture level | Partial | `mutateBoth` groups spec+viewer for delete; multi-node drag pushes one history entry per node-drag-stop (not coalesced) | S — coalesce within a single selection-drag gesture |
| Copy / paste | **No** | no clipboard handlers | M — serialize selection subgraph, regen ids on paste |
| Duplicate (cmd-D) | **No** | — | S (after copy/paste plumbing) |
| Keyboard nav (arrows nudge, tab through nodes) | **No** | only delete + cmd-Z + space (onion swap) | S for arrow-nudge by GRID; M for tab cycle |
| Lane / swimlane containment | Partial | `Fold` placeholder collapses N nodes into one, **not** an open container holding children visually like a draw.io swimlane | L — different abstraction; would need parent-node support |
| Group / ungroup | Partial via folds | — | — |
| Node search / quick-jump (cmd-K palette) | **No** | — | S |
| Context menus | Partial | edge-kind and fold menus exist; no general node menu (rename/duplicate/etc.) | S |
| Keybinding cheatsheet / discoverability | **No** | — | XS — static panel |
| Touch / trackpad pan | Yes | `panOnScroll={true}` | — |
| Connect-validation feedback | Partial | port-conflict logged to console, no UI cue ([app.tsx:478-481](../../../tools/topology-vscode/src/webview/rf/app.tsx#L478-L481)) | XS — toast or red handle flash |
| Diff / compare view | Yes (project-specific) | A-live / A-other / B-onion modes | — beyond category baseline |
| Auto-layout (one-shot) | **No** | manual placement only; ELK / dagre are canonical drop-ins | M |

### Triage — which gaps deserve a task branch

**High value, low effort (open branches when next friction surfaces):**
1. **MiniMap** — XS, drop-in `<MiniMap />`. Standard expectation; perceptual win for "where am I in this graph."
2. **Zoom-to-fit / zoom-to-selection keybindings** — XS, function already exists; bind `f` and `shift-f`.
3. **Rounded corners on `snake` route** — S, single edit in [AnimatedEdge.tsx:155-167](../../../tools/topology-vscode/src/webview/rf/AnimatedEdge.tsx#L155-L167). Visual polish every other tool has.
4. **Connect-validation UI cue** — XS, replace `console.warn` for port-already-wired with a transient red flash on the rejected target handle.

**High value, medium effort (branch when friction is logged):**
5. **Copy / paste / duplicate** — M, baseline expectation. Subgraph serialization with id regeneration; hooks into existing `mutateSpec` + `scheduleSave`.
6. **Edge display labels** — M; channel-name `label` is currently invisible, surprising once more than a handful of edges are wired.
7. **Multi-node alignment guides** — S, extend matcher to use selection-bbox center.
8. **Drag-stop undo coalescing** — S, one history entry per multi-node drag gesture.

**High value, high effort (hold until friction insists):**
9. **Auto-routing with obstacle avoidance** — L. Biggest gap vs. yEd/draw.io. Defer until "edges crossing through nodes" gets logged. Canonical answer is ELK or libavoid-js; prefer adopting over rolling.
10. **Auto-layout (dagre / ELK one-shot)** — M-L. Defer until a 50+ node spec generates complaints about hand-placement.

**Low priority / different shape:**
- Swimlanes — fold abstraction already covers "collapse a region"; container-style lanes are a different mental model.
- Keyboard tab-through-nodes — diminishing return for graphs of this size.

**Branch-opening recommendations (proposed, not started):**
- `task/fix-minimap-add` (item 1).
- `task/fix-zoom-keybindings` (item 2).
- `task/fix-snake-rounded-corners` (item 3).
- Bundle items 1–4 into a single `task/industry-quick-wins` branch if landed together (≥$5 cost-marker territory).
- Items 5–8 wait for explicit friction logged in this session-log
  before opening branches.
- Items 9–10 stay dormant per post-v0 friction-driven posture.

### Addendum — patterns missed in the first pass

Surveyed after the quick-wins shipped; not in the original matrix.

| Pattern | Have it? | Notes | Effort |
|---|---|---|---|
| Export to PNG / SVG | **No** | Universal in yEd/draw.io/RF examples; `react-flow` has `toPng`/`toSvg` helpers | XS |
| Tooltips on hover | **No** | Long ids / truncated sublabels have no hover reveal | XS |
| Bend points / waypoints on orthogonal edges | **No** | draw.io's signature gesture; our `route` is one of three presets, no per-edge waypoints | M-L |
| Node resizing handles | **No** (intentional) | Sizes encode node role; deviation from yEd/draw.io is probably correct here | — |
| Snap to other nodes' edges (not just centers) | **No** | Guides match centers within `ALIGN_TOL`; edge-flush snapping is common | S |
| Outline / structure panel | **No** | yEd-style tree of nodes; probably overkill at our scale | — |
| Z-order controls (send to front/back) | **No** | Not needed until nodes overlap meaningfully | — |
| Properties inspector sidebar | **No** | Editing arbitrary `props` is piecemeal (rename, sublabel only) | M |

**Triage:** export and tooltips are the only clean "everyone has
this, it's cheap" gaps. Holding both per friction-driven posture;
neither has caused observed pain yet.



### 2026-05-03 — Implementation-pattern audit (different axis)

Reframed the industry-pattern review from *missing user features* to
*hand-rolled code that duplicates library primitives*. Scanned
`tools/topology-vscode/src/` and produced an industry-pattern audit: 19
"reimplemented" items (R1-R19) with canonical replacements + 7
"missing" react-flow/ecosystem features (M1-M7). Out-of-scope items
(Yjs, Storybook, telemetry, mobile, react-query) explicitly listed
and excluded.

Key cross-references with the deferrals memo:
- R14 (elkjs/libavoid-js routing) subsumes the deferred *auto-routing
  with obstacle avoidance* item.
- M1 (`isValidConnection`) subsumes the reject-flash quick win — no
  flash needed if the drag never starts.
- M3 (react-flow `EdgeLabelRenderer`) is a prerequisite for the
  deferred *edge display labels* item.
- R19 coordinates with deferred *snap to other nodes' edges* and
  *multi-node alignment guides*.

Audit doc is the spec for a future session; nothing landed here.
Suggested cluster order: state→zustand (R1-R3) → panels→React
(R4-R7) → geometry/routing (R14-R18, blocks on lib choice).


### 2026-05-04 — Node-timing audit (event-driven vs SMIL global clock)

Companion to the SMIL-parity audit (commits ed2987d..ba5f74b). That
audit closed visual mismatches but explicitly tagged cross-edge timing
as "correct — different model": SMIL encodes every edge phase in one
16.902s keyTimes envelope; the JS port has no compiled cross-edge
schedule, just a stepped event queue. This audit asks where that
difference introduces friction.

| # | Aspect | Runner/editor behavior | SMIL/spec invariant | Verdict | Notes (file:line) |
|---|--------|------------------------|----------------------|---------|-------------------|
| 1 | Cross-edge cadence / backpressure slack | Gates buffer first input, fire immediately on second arrival. No idle-wait visible. | readGate ack waits t=0.331→0.826s in cascade SVG; slack is observable. | friction | [runner.ts:324-344](../../../tools/topology-vscode/src/sim/runner.ts#L324-L344), [handlers.ts:56-74](../../../tools/topology-vscode/src/sim/handlers.ts#L56-L74) |
| 2 | Tick interval vs animation duration | `pulseSpeedPxPerMs = (400/tickMs)*0.08`. Long edge + fast tick: downstream emit at tick+1 lands while upstream pulse still animating. | SMIL: edge traversals locked into 16.902s envelope; downstream scheduled after upstream completes. | friction | [AnimatedEdge.tsx:257-259, 399-415](../../../tools/topology-vscode/src/webview/rf/AnimatedEdge.tsx#L257-L259) |
| 3 | Concurrent self-pacer offset | Re-fires unconditionally at `world.tick + 1`, no edge-traversal or queue-load compensation. | SMIL schedules downstream relative to *arrival*, not wall-clock tick. | bug-risk | [runner.ts:333](../../../tools/topology-vscode/src/sim/runner.ts#L333) — re-fire can outrun previous pulse on long edges or under backlog |
| 4 | Latch + AND gate ack visibility | Handler buffers ack, fires when input arrives — same tick. No render signal for "ack buffered, waiting." | SMIL shows ack pause on-screen (495ms slack) before input arrives. | friction | [handlers.ts:20-33](../../../tools/topology-vscode/src/sim/handlers.ts#L20-L33), [simulator.ts:252-261](../../../tools/topology-vscode/src/sim/simulator.ts#L252-L261) |
| 5 | Self-sustaining mode event ordering | Same-`readyAt` events tie-broken by insertion-order `id`. Deterministic but non-commutative across cycles. | Spec: repeated cycles must not depend on schedule call order. | bug-risk | [simulator.ts:181-184](../../../tools/topology-vscode/src/sim/simulator.ts#L181-L184) |
| 6 | stepToNode / per-node step | Pauses, runs `stepOnce` up to 5000 until target fires, leaves paused. Stops on *first* arrival. | No SMIL analog. Stepping must preserve free-running semantics. | correct — different model | [runner.ts:148-160](../../../tools/topology-vscode/src/sim/runner.ts#L148-L160), [AnimatedNode.tsx:123-155](../../../tools/topology-vscode/src/webview/rf/AnimatedNode.tsx#L123-L155) |

**Key takeaways:**
- Runner is logically correct. Friction is animation/visibility, not handler logic — except #3 (hardcoded `tick+1`) and #5 (insertion-order ties), which are latent bug-risks under long edges, backlog, or repeated cycles.
- Backpressure slack is invisible. The whole point of the latch+AND pattern is the wait — the runner collapses it visually. Most user-impactful finding.
- Pulse animation and event firing are async. No invariant guarantees a pulse finishes animating before downstream emits.

**Branches opened from this audit:**
- `task/edge-data-delay-support` — addresses row #3 (per-edge delay override so self-pacer can compensate for edge length).
- `task/validate-self-pacer-under-backlog` — pins the FIFO ordering invariant flagged in row #3 with a regression test.
- `task/deterministic-cycle-ordering` (row #5) — dropped: current `(readyAt, id)` is already deterministic; the non-commutativity concern needs an explicit ord-per-event design discussion before any change.


---


## 2026-05-14 — Integration test suite (task/integrated-substrate-tests)

Implemented the integration test plan from `diagrams/test-plan/`. Created harness
utilities (`_fixtures.ts`, `_harness.ts`) and 5 new test files covering:
- IRG modes A5–A8 (left-alone, right-only, both-filled)
- CI fan-out B1–B2 (lockstep fan-out, seed-blocked CI)
- Lateral cascade C1–C2 (inhibit drain verified; see blocker below)
- Backpressure D1–D3 (queue holding, consume release, partial join)
- Misc E1, F1, D3-ext (sequential drain, wire seed, 3-input partial gate)

**Blocker found:** C1 single-winner mutual exclusion is not achievable with
CI.inhibitOut → IRG.right. The right-only path drains the inhibit signal, but
a subsequent left delivery fires anyway. Mutual exclusion requires inhibit
upstream of CI's own firing decision. Documented in test comment; needs design
decision.

**Substrate finding:** relay fires only on input fill (fill→onRun). Sequential
drain via relay requires timer advancement; E1 test uses direct input→readgate
to observe canAccept-triggered sequential delivery.

125 tests passing, all green.

---
