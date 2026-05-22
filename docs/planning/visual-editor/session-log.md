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

<!-- source: session-log/2026-05-07-reload-window-misses-webview-bundle.md -->

# 2026-05-07 — Reload Window does not pick up new webview bundle

## Friction

While iterating on the substrate runtime fixes, `npm run build`
produced a fresh `out/webview.js` but VS Code's "Developer: Reload
Window" command did not pick it up — the editor kept running the
previous bundle, making post-fix bug reports look like the fix had
silently failed. Closing the topology document tab and reopening it
DID load the new bundle.

## Probable cause

`tools/topology-vscode/src/extension.ts` installs a `bundleWatcher`
on `out/webview.js` (gated on `extensionMode === Development`) that
is supposed to hot-reload the webview HTML when the bundle changes.
This either (a) fires correctly on file change but VS Code's
webview cache wins, (b) doesn't run because the host extension also
needs to reload, or (c) only fires on a `vscode.workspace`-rooted
glob, not the absolute extension path used here. Not investigated
in this session.

## Workaround

Close the topology tab and reopen it. Skip "Developer: Reload
Window" — it's misleading.

## Why this matters for diagnostics

A stale bundle hides whether a fix landed. The substrate-log.jsonl
timestamps before/after a build are the cheap check: if no new
entries appear after the build's mtime, the new code isn't running.
Worth folding into the standard "verify a substrate fix" loop.

## Open question

Is bundleWatcher actually firing in dev? If not, that's a separate
small fix (one-shot, when this friction recurs).


---

<!-- source: session-log/2026-05-07-pulse-label-detaches-on-paused-drag.md -->

## 2026-05-07 — pulse label detaches from pulse on paused drag

**Observation:** With the runtime paused, dragging a node makes the
pulse label visibly separate from the pulse dot. Pressing play
re-attaches them on the next frame.

**Hypothesis / scope:** Follow-on from the paused-pulse-resumes fix.
That fix correctly skips rAF on a paused remount, but it also means
no frame is painted at all. The path's `d` updates via React, so the
dash sits on the new geom; but the label's `transform` was last set
by the previous mount's frame, which used the old geom's coords. So
the label stays at the old position while the dot jumps to the new
path.

**Decision:** Paint exactly one frame on mount when paused. With
`frozenElapsed = 0`, calling `frame()` once computes
`arcTraveled = startArc` against the new geom and updates both the
dash offset and the label transform.

**Outcome:** Patched `PulseInstance.tsx` to call `frame()` once in
the paused-on-mount branch. Build clean. User confirmed label now
tracks the pulse during paused drags.


---

<!-- source: session-log/2026-05-07-paused-pulse-resumes-on-node-touch.md -->

## 2026-05-07 — paused pulse resumes on node touch/drag

**Observation:** With the animation paused, touching or dragging
any node causes the in-flight pulse to finish its arc instead of
staying frozen.

**Hypothesis / scope:** `PulseInstance` effect deps are
`[geom, speedPxPerMs]`. A drag rebuilds geom → effect tears down
and remounts. The pause-freeze only listens to `subscribeWiresPause`
events; on a fresh mount it does not consult
`isWiresRuntimePaused()`, so a new rAF loop kicks off and the pulse
runs to completion regardless of pause state.

**Decision:** Fix on `task/node-ticks`. Initialise `frozenElapsed`
on mount when the runtime is currently paused, and skip the initial
rAF schedule.

**Outcome:** Patched `PulseInstance.tsx` to read
`isWiresRuntimePaused()` on mount; if paused, set
`frozenElapsed = 0` and don't request the first frame. Resume path
already handles the rebase.


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

## Supersedes

[2026-05-07-reload-window-misses-webview-bundle.md](2026-05-07-reload-window-misses-webview-bundle.md).
The Reload Window path is no longer the dev loop; the question of
why Reload Window itself misses the bundle is moot.

## Side observation surfaced during this work

Cold-open and any in-editor doc edit (e.g., renaming a node) trip a
stuck-pulse: substrate emits the first pulse, AE re-subscribes
after `ae-received`, ack never returns, loop stalls. This is the
new blocker for port-plan step 2 — see
[../handoff-next-task.md](../handoff-next-task.md).


---

<!-- source: session-log/2026-05-05-view-load-race-destroys-topology-files-on-open.md -->

## 2026-05-05 — view-load race destroys topology files on open

**Branch:** task/view-load-race-guard
**Mode:** debug + fix.

Opened the editor on `topology.json` and saw nothing — no diagram, no pulses.
Investigation found both `topology.json` and `topology.view.json` had been
clobbered by the editor itself: nodes/edges keys missing, just camera +
folds + bookmarks remained. HEAD versions were intact and had to be
restored twice (first restore got re-clobbered when the editor was
reopened).

**Root cause.** Race in the ready→load→view-load sequence. After audit-15
moved positions off the spec onto `viewerState.{nodes,edges}`, the
sidecar (`topology.view.json`) became the sole source of truth for
positions. On startup the host posts `load` synchronously, then
`await sendView()` posts `view-load`. Between them, React Flow renders
the spec with default positions and its initial onMoveEnd fires
`scheduleViewSave()` ([app.tsx:65-68](../../tools/topology-vscode/src/webview/rf/app.tsx#L65-L68))
with `viewerState.nodes` still empty. The debounced save can land
before `view-load` arrives → file written with no nodes block → HEAD
positions destroyed. Subsequent saves drift further.

**Fix.** `performViewSave` and `scheduleViewSave` short-circuit while
`lastViewSyncedText === undefined`. `markViewSynced` is called from
`handleViewLoad` after parsing the sidecar, so the gate flips exactly
when viewerState is fully populated. Three regression tests in
`test/contracts/view-save-load-gate.test.ts`.

**Why this didn't hit pre-audit-15.** Positions used to live on the
spec, so `handleLoad` populated everything in one shot; the view
sidecar carried only camera/folds/selection. An early view-save lost
nothing important. Splitting positions into the sidecar made the
racewindow load-bearing.


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

**What worked methodologically.** Instrumented before guessing.
Three time-spaced probe captures — first dump (state at stuck-anim),
1.5s follow-up (clock-vs-arc check), 30s third (genuine stall vs
slow recovery) — distinguished four hypotheses (clock frozen,
geom rerun, completion path, gating bug) cleanly. The third dump
plus cross-reference with `.probe/fold-halo-last.json` was what
identified the fold-vs-defer ownership gap. Without the
instrumentation we'd have shipped option A (clamp duration) which
masks symptoms without fixing the architectural gap.


---

<!-- source: session-log/2026-05-05-pulse-animation-leak-5-of-6-edges-stuck-per-cycle.md -->

## 2026-05-05 — pulse animation leak: 5 of 6 edges stuck per cycle

**Branch:** task/pulse-leak-investigation
**Mode:** debug, in-flight.

User reported: pulses run for a while, then stop and don't restart.
Bisected via the new RunnerProbe (merged d843036) to bug B
(stuck-anim, Contract C4 regression). Per-edge breakdown captured:

```
⚠ stuck-anim: 5 [
  i0.out->i1.in,
  i0.inhibitOut->inhibitRight0.left,
  i1.out->readGate.ack,
  i1.inhibitOut->inhibitRight0.right,
  in0.out->readGate.chainIn
]
```

Each edge leaks exactly 1 pulse. Only `readGate.out->i0.in` is
never in the leaked set. Topology has 6 edges total.

**Suggestive pattern.** The exception is the edge whose source is the
ReadGate. That edge fires N times per cycle (one per input value);
the others fire either at gate-mediated points or as feedback. So
the rule may be: "every pulse that fires from a non-ReadGate source
gets stuck once per cycle." Or equivalently: every pulse stuck mid-
flight EXCEPT pulses traveling along readGate.out->i0.in.

**Hypothesis to test first.** PulseInstance's makeFrame computes
`localT = elapsed / remainingMs` and only calls onComplete when
localT >= 1. If `getSimTime()` advances such that elapsed never
reaches remainingMs for those 5 edges (some kind of clock-vs-arc
mismatch tied to the tick that kicks them off), the rAF loop runs
forever without completing. The readGate.out edge's pulses might
be unaffected if their `simStart` is captured at a different point
in the tick boundary.

**Investigation steps not yet taken:**
1. Instrument makeFrame to log first 30s of each pulse's localT
   progression — see whether stuck pulses freeze at a particular
   localT value.
2. Check whether stuck pulses' `swapStart` is captured before vs
   after the runner's `state.simSegmentStartWall` was reset.
3. Check whether geom changes mid-flight on those edges (e.g. fold
   collapse causing a re-route).
4. Verify Contract C4 test still pins the once-per-mount invariant
   it claims to — possibly the regression is in a path the test
   doesn't cover (e.g. the geom-rerun branch in PulseInstance).

**Why this needs its own branch.** Iterating on rebuild-and-check
in the live editor was burning cycles without converging. Need
proper instrumentation, not blind hypotheses.


---

<!-- source: session-log/2026-05-05-editor-rename-did-not-reach-go-run-output.md -->

## 2026-05-05 — editor rename did not reach `go run` output

Dogfooding the InputNode-stdout change ([1](screenshots/2026-05-05-editor-go-output-mismatch-1.png)
shows the editor with `in0555` while `go run` output references the
same name; [2](screenshots/2026-05-05-editor-go-output-mismatch-2.png)
shows the editor displaying `in0grtyh` while `go run` output references
`in045ps` — three different names across the editor view, the on-disk
topology.json/Wiring.go, and the spawned binary).

Two distinct bugs surfaced:

1. **Save / codegen race vs. Run.** The Run button posted `{type:"run"}`
   without waiting for the 250ms save debounce, and an in-flight
   contenteditable rename was never committed before Run fired.
   topogen ran against an older topology.json than the editor view.
   Fix on `task/run-flush-pending-edits`: RunButton now blurs any
   active inline edit (commits via the existing blur listener) and
   bundles the latest spec text into the run message; the host applies
   + saves that text before `topogen.write()`.

2. **Stale `lastSpec.current` after rename.** After the rename worked
   and Run produced correct output, dblclicking the same node opened
   the edit field with the OLD name and effectively undid the rename.
   `mutateBoth` replaces `store.spec` via immer but
   `ctx.lastSpec.current` was only updated on load/connect/undo.
   inline-edit's `rerenderFromSpec` rebuilt RF from the stale spec,
   leaving RF nodes with OLD ids; the displayed label only looked
   correct because the contenteditable's typed-in text persisted
   through React's no-op reconciliation. Fix in the same branch:
   `rerenderFromSpec` reads `getSpec()` from the live store and writes
   it back into `lastSpec.current`.

Adjacent fix shipped same day: `task/input-node-stdout` added
`fmt.Printf` to InputNode.Update so the Input rename is actually
visible in `go run` stdout in the first place — without that, the
"editor change → Go output" round-trip had no signal to verify.


---

<!-- source: session-log/2026-05-05-deleting-a-collapsed-fold-node-nukes-the-editor-view.md -->

## 2026-05-05 — deleting a collapsed fold node nukes the editor view

**Branch:** task/fold-delete-crash
**Mode:** edit
**Duration:** ~5m (initial report)

- User selected a fold node in folded mode and pressed delete. Almost
  the entire editor vanished. Recovering required removing the
  topology diff overlay AND reloading the topology.json tab.
- No probe dump was produced (.probe/ entries are stale from the
  pulse-rules investigation). No console error was captured.

**Hypothesis (from code read, not yet verified):**

`onNodesDelete` at [tools/topology-vscode/src/webview/rf/app/_handle-delete.ts:27-42](../../tools/topology-vscode/src/webview/rf/app/_handle-delete.ts#L27-L42)
treats a fold-type RF node as viewer-only: it removes the fold from
`viewerState.folds` and rebuilds the flow. It does **not** delete the
members from the spec, by design.

But on rebuild via
[spec-to-flow.ts:70-80](../../tools/topology-vscode/src/webview/rf/adapter/spec-to-flow.ts#L70-L80),
each member's RF position falls back to `vs.nodes?.[n.id]?.x ?? 0`
(and `?? 0` for y). Members that lived inside a collapsed fold and
were never individually positioned in `viewerState.nodes` therefore
render at (0,0). All members of the just-deleted fold stack at the
origin, looking "vanished" from a viewport panned elsewhere.

The diff-overlay aggravation is likely
[decorateForCompare / decorateForOnion](../../tools/topology-vscode/src/webview/rf/app/_decorate.ts)
holding stale references to the fold id, which is why removing the
diff (plus reloading the tab) is what cleared it.

**Resolution:** the actual root cause (confirmed by an in-process
diagnostic that tracked every keydown / RF node-change event for the
fold) was that **React Flow v11 silently dropped the Backspace
keypress** when a fold-placeholder div was the active element. RF's
internal `useKeyPress` treats focus-on-the-node-DOM as "user is
interacting with the node, don't fire global delete". Selection fired,
keydown fired (defaultPrevented=false), but no `remove` change ever
materialised — and therefore `onNodesDelete` never ran, so the fold
was never deleted. The "vanishing" symptom was the absence of any
visible feedback; the topology-diff overlay reload masked it because
reload re-created the RF state from scratch.

Fix: a webview-level keydown handler at
[tools/topology-vscode/src/webview/rf/app.tsx](../../tools/topology-vscode/src/webview/rf/app.tsx)
that, on Backspace/Delete, dispatches `delH.onNodesDelete` directly
for any selected fold node, bypassing RF's quirk.

A secondary bug surfaced during investigation:
`decorateForCompare` / `decorateForOnion` called `specToFlow` with an
empty viewer-state, dropping member positions to (0,0) under the diff
overlay. Fixed in the same branch by threading `viewerState`
through.


---

<!-- source: session-log/2026-05-04-smoothness-audit-re-run-with-always-on-probe.md -->

## 2026-05-04 — smoothness audit re-run with always-on probe

**Branch:** task/probe-rerun-smoothness
**Mode:** smoothness audit (audits.md #5)
**Start cost:** $320.85

Scope: pan, zoom, node drag (no topology change), animation
playback, scrub, fold/unfold of existing folds, bookmark jump,
replay, view recall. The pulse visual probe is always on as of
`d771871` (main); periodically run `window.__pulseProbeReport()`
in the webview devtools console — empty array is a clean result
worth recording, non-empty entries are fresh friction.

- The probe output is now persisted to `../../../.probe/pulse-last.json`
  via a new webview→host message (`pulse-probe-dump`). The webview
  installs `__pulseProbeDump()` eagerly on module load; entries
  also auto-dump 500ms after each push, and a 5s heartbeat
  refreshes the file whenever any pulse rendered since the last
  dump (so clean runs produce confirmed `entries: []` evidence
  without a console call).
- Tooling friction: getting the bridge wired ate the session.
  Eager init was guarded by `!__pulseProbeLog` (broke on hot
  reload — fixed). `acquireVsCodeApi()` re-throws on the second
  call when the bundle re-executes against the same context
  (`retainContextWhenHidden: true`) — fixed by caching the API
  on `window.__vscodeApi`. Two spec attempts (cache-bust query,
  auto-reload watcher) caused VS Code-internal `toUrl` errors
  and were reverted. The final wrong-frame mistake (devtools
  console attached to outer wrapper, not the iframe running the
  bundle) was the longest dead-end — heartbeat dump removes the
  need for a console diagnostic, so the trap can't recur.

**Probe output:**
- First clean run after the bridge landed: `{"ts": 1777880387755,
  "entries": []}`. Scope exercised was minimal (node drag) — see
  followups for broader coverage.

**Followups (candidates, not commitments):**
- Drive the rest of the smoothness scope (pan, zoom, scrub, fold,
  bookmark jump, replay, view recall) and capture each as a
  separate dump (or accept heartbeat-overwritten last-state).
- If recurring drift entries appear, dig into the bezier-end
  label-drift hypothesis from the prior session (eps-precision
  in finite-difference tangent on flattening curves).


---

<!-- source: session-log/2026-05-04-pulse-label-slides-into-target-node-before-fading.md -->

## 2026-05-04 — pulse label slides into target node before fading

**Branch:** task/fix-pulse-label-early-fade
**Mode:** smoothness audit (probe threshold 0.01)
**Cost:** untracked (user waived)

After the slowdown fix shipped, user noted the label kept full
opacity past the arrow tip and slid into the target node's box
before fading. Cause: opacity was keyed to `arcTraveled` (the dot /
back of dash) but the label rides `PULSE_DASH_PX/2` ahead in arc
space. Fade started at `overall = 0.95` (i.e. dot at 0.95 of
svgArc), which corresponds to the label already being at
`0.95 + PULSE_DASH_PX / (2 * svgArc)` — past the arrow tip and
overlapping the node. Fix at
[AnimatedEdge.tsx:468-473](../../../tools/topology-vscode/src/webview/rf/AnimatedEdge.tsx#L468-L473):
key the label's opacity to `labelArcSvg / svgArc` instead of
`overall`, so the fade trips spatially at 0.95 of the path —
before the node. Dot keeps its own envelope. Build + 157 tests
green.


---

<!-- source: session-log/2026-05-04-pulse-label-end-of-edge-slowdown.md -->

## 2026-05-04 — pulse label end-of-edge slowdown

**Branch:** task/fix-pulse-label-end-slowdown
**Mode:** smoothness audit (probe threshold 0.01)
**Start cost:** $345.38

User observation: "when the label is right above the arrow it slows
down by 2x." Diagnosis: in
[AnimatedEdge.tsx:429-430](../../../tools/topology-vscode/src/webview/rf/AnimatedEdge.tsx#L429-L430)
the label rides the *visible* midpoint of the dash:
`labelArcSvg = (arcTraveled + headArc) / 2` with
`headArc = min(svgArc, arcTraveled + PULSE_DASH_PX)`. Once `headArc`
clamps to `svgArc`, the right endpoint stops moving while
`arcTraveled` keeps advancing — so the label's arc-position advances
at half the rate. Geometric, not a timing bug. The dot has the same
clipping but its brightness fade hides it.

Fix (option 2 of 3 considered — XS): widened the opacity envelope
from `0.95..1` (0.05 ramp) to `0.90..1` (0.10 ramp) at
[AnimatedEdge.tsx:418](../../../tools/topology-vscode/src/webview/rf/AnimatedEdge.tsx#L418)
so the label is mostly invisible during the final `PULSE_DASH_PX`
where the slowdown lives. Doesn't fix the geometry; hides it behind
the existing fade. Build + 157 tests green. User to judge feel.

Options not taken: (1) drop visible-midpoint for fixed-offset midpoint
— replaces slowdown with a brief stop at the endpoint, probably worse.
(3) extend path with invisible tail of length `PULSE_DASH_PX` — clean
geometric fix but touches edge geometry / arrowhead placement; revisit
if this resurfaces.

**Followups (candidates, not commitments):**
- If the wider fade looks too eager (label disappears too far from
  arrow), revert to 0.95/0.05 and pursue option 3 (path tail).
- Symmetric question: does the dot itself slow visibly at end now
  that the label fades earlier? If yes, option 3 covers both.

**Update — fade alone insufficient.** User: "I still see the 1/2
speed change. the only difference is there is a fraction of a
second fade out before disapparing." Slowdown begins at
`overall = 1 - PULSE_DASH_PX/svgArc` (often <0.90), so widening the
fade only catches the tail. Reverted opacity to 0.95/0.05 and
swapped to option 3-lite: replaced the visible-midpoint formula
with `labelArcSvg = arcTraveled + PULSE_DASH_PX/2` (constant offset
from the back of the dash, no clamp). Past `svgArc` the label
position extrapolates from the path-end point along the end tangent
so the label rides off the edge at constant speed and the existing
opacity envelope fades it as it exits. Same `queryTangent` call as
before; tangent is queried at `min(labelArcSvg, svgArc)` and reused
for both extrapolation and the normal-direction offset, so no new
sampling mismatch. Build + 157 tests green.


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

<!-- source: session-log/2026-05-04-ack-edge-junction-jerk.md -->

## 2026-05-04 — ack edge junction jerk

**Branch:** task/ack-edge-junction-jerk
**Mode:** smoothness audit follow-up (audits.md #5)
**Start cost:** $336.67

Scope: per-edge speed inconsistency / Ack-edge corner jerk from
the 2026-05-04 audit. Symptom: pulse moves at constant speed but
the riding label snaps abruptly at corners.

- **Root cause: queryTangent walked straight-only `geom.cum[]`
  while `svgArc` measured the real SVG path including rounded
  Q-corners.** At each cum boundary the tangent flipped 90° in
  one frame → label translated ~14px diagonally. Fixed by
  always finite-differencing on the SVG path (the previously-
  gated "mixed routes (none today)" fallback at the bottom of
  `queryTangent` was the right code; the early `kind === "straight"`
  return shadowed it).
- **Geometric corner-rotation wobble.** With the snap fixed, the
  label's perpendicular offset still rotated 90° through corners
  → ~18px sweep. User accepted the wobble on snake (looks like a
  bank), pushed back on below-route corners (label inside the U
  cut across the bend rather than banking).
- **Final placement spec (user-driven):** label always above
  horizontal segments, always right of vertical segments, smooth
  parallel path. Implemented as `nx = |ty|, ny = -|tx|` for
  axis-aligned routes (snake/below) — perpendicular for first-
  quadrant tangents, smooth quarter-circle blend through 90°
  corners. Cubic/line routes kept the classic perpendicular-
  with-upward-bias because their tangents leave the first
  quadrant and the absolute-value rule produces tangent slip
  there. Probe (threshold lowered to 0.01px) caught a 5.27px
  tangent slip when the new rule was first applied
  unconditionally — the split per route kind cleared it. Rule
  recorded at [memory/project_pulse_label_offset_rule.md](../../../memory/project_pulse_label_offset_rule.md).
- Corner radius `r` increased to 15 (from 8) for visible banking
  on inner curves; below-route corridor offset bumped 40 → 80
  so the inner radius isn't pinched by short vertical legs.

**Probe output (final):** `{"ts": 1777884184038, "entries": []}`
clean across the smoothness scope at 0.01px threshold.

**Followups (candidates, not commitments):**
- Sweep remaining smoothness scope (pan/zoom/scrub/fold/bookmark/
  replay/view-recall) with the lowered probe threshold.
- Decide whether to keep the probe threshold at 0.01 long-term
  (catches noise, generates churn) or restore to 1.5 (the
  original "real artifact" threshold).


---

<!-- source: session-log/2026-05-03-snake-corner-label-jerk-observation-salvaged.md -->

## 2026-05-03 — snake-corner label jerk observation (salvaged)

**Branch:** task/smoothness-audit-always-on-probe (superseded; log
entry salvaged on 2026-05-04 — code rewrite was overtaken by
[ack edge junction jerk](#2026-05-04--ack-edge-junction-jerk) work
that solved the same family of artifacts via a different route).

- **Snake-corner label jerk**
  ([1](screenshots/2026-05-03-snake-corner-label-jerk-1.png) /
  [2](screenshots/2026-05-03-snake-corner-label-jerk-2.png) /
  [3](screenshots/2026-05-03-snake-corner-label-jerk-3.png)).
  At the elbow of a snake route, the riding label
  jumps across the wire and appears to curve/teleport through the
  corner instead of pivoting cleanly. Two structural causes:
  1. Per-segment normal + the `if (ny > 0) flip` rule put the
     label on different sides of the wire on the two legs
     (above on the horizontal leg; left on the vertical-down
     leg), so at the junction the side flips.
  2. Within ~PULSE_DASH_PX of the elbow, dot-midpoint arc can
     straddle the corner; tangent is whichever leg owns that
     arc — a step function, not continuous — so the label snaps.

**Proposed fix at the time — parallel-track offset path (drafted,
not landed; the actual fix took a different shape — see ack-edge
entry above for the `nx = |ty|, ny = -|tx|` rule that shipped):**

Build, alongside `PathGeom`, an `OffsetPathGeom` representing a
*parallel track* offset by `PULSE_LABEL_NORMAL_PX` to one
consistent side. The label rides this path; its position is read
directly from `offsetPath.getPointAtLength(...)`. No per-frame
normal computation, no flip rule, no tangent query for label
placement.

Key properties (the "locked on a parallel track" model
the user asked for):

- **Mitered right-angle corners.** For snake/below routes the
  offset path is an `M L L L …` polyline whose vertices are the
  miter intersections of the offset legs. The label travels
  straight along each offset leg and pivots at the miter point —
  no curving through the corner.
- **Per-segment correspondence**, not global-fraction mapping.
  When the dot is at fraction `f` along leg *i* of the main
  path, the label is at fraction `f` along leg *i* of the
  offset path. At the elbow (dot exactly at the wire vertex)
  the label is exactly at the miter vertex. This avoids the
  outer/inner arc-length mismatch that a single global
  fraction would introduce.
- **Consistent side.** The offset side is chosen once per route
  from its topology so "up-and-right of the wire" is honored on
  both legs of a snake — never via a per-segment ny-sign flip.
- **Cubic routes.** Offset the analytic cubic by displacing
  both control points along their endpoint normals; query
  position at the same `t` the dot uses (we already recover `t`
  via Newton inversion). Same continuity guarantee.

**Followup that did open from this seed:**
- Industry-standard pattern review across the plugin — landed as
  the 2026-05-03 industry-pattern review entry below.

**Recommended branches (not yet opened, need design input):**
- `task/visualize-gate-buffer-state` (row #4) — render "waiting on second input" affordance.
- `task/backpressure-slack-envelope` (row #1) — animate the ack-wait interval.
- `task/stepping-semantics-doc` (row #6) — clarify "step one fire" vs "step one cycle" in self-sustaining mode.


---

<!-- source: session-log/2026-05-03-smoothness-audit-re-run-on-rebuilt-pulse-engine.md -->

## 2026-05-03 — smoothness audit re-run on rebuilt pulse engine

**Branch:** task/fix-pulse-overlap
**Mode:** smoothness audit (audits.md #5)
**Start cost:** $279.83

Scope: pan, zoom, node drag (no topology change), animation playback,
scrub, fold/unfold of existing folds, bookmark jump, replay, view
recall. Re-run against the rebuilt AnimatedEdge engine (single rAF,
arc-traveled SoT, per-edge queue, geometry-preserving swap, single
knob `PULSE_PX_PER_MS_AT_REF_TICK`). Logging only — no fixes during
pass.

- **Animation playback — riding label off-curve from dot.** The
  riding value label traces a slightly different curve than the dot
  along the same edge, independent of node dragging. Both are
  supposed to read from the same arc-traveled value, so this points
  at a position-derivation difference (e.g. label translate uses the
  point but not the path's local geometry/offset, or the dot's
  visible position differs from `getPointAtLength(arcTraveled)` due
  to the dashoffset window vs. tangent). Visible on stationary
  edges, so not a geometry-swap artifact.
  - Resolved in-session: two compounded causes. (1) Label read
    `arcTraveled` (back of the 20px dash window) instead of the
    dot's visual midpoint. (2) Label used a fixed screen-y offset,
    which on diagonals/curves has a component along the direction
    of motion, so the label visibly led/lagged the dot. Fix: named
    `PULSE_DASH_PX`, label reads `arcTraveled + PULSE_DASH_PX/2`,
    and offsets along the local path normal by
    `PULSE_LABEL_NORMAL_PX` (always toward screen-up). Dot and
    label now ride one curve, parallel-separated.
  - Sub-finding: at the end of a bezier edge (input-node edge),
    label rises slightly off the parallel curve just before the
    pulse finishes. Distinct from the parallel curve itself
    rising as the bezier flattens toward a horizontal target
    handle — user reports drift off that parallel track. Not
    explained by the current model (label arc tracks dot visible
    midpoint; normal taken at same arc point). Candidate causes
    not verified: visual centroid of a high-curvature dash drifting
    off arc midpoint, sub-pixel text baseline vs. dash centerline,
    eps-clamp on the last 0.5px of normal sampling. Deferred:
    user to capture screenshots; revisit with a real repro rather
    than another speculative patch. Four iterations already spent
    on this observation; pausing further changes per audit posture.
  - Evidence captured: three screenshots under
    [screenshots/](screenshots/) — `2026-05-03-pulse-label-end-bezier-1.png`
    through `-3.png`, bezier edge into `readGate1`. Shot 3 shows
    the label ~25–30px above-left of the dot at the target handle,
    visibly farther than the configured 10px parallel offset —
    real position discrepancy, not a parallel-curve perception.
    Leading hypothesis for next session: finite-difference tangent
    uses `eps ≤ 0.5px` (1px sampling window); near a fast-flattening
    bezier, `getPointAtLength` precision makes the tangent direction
    noisy, and noise in `n` is amplified 10× by the offset distance.
    Try a larger eps (4–6px) for tangent sampling.
  - User-suggested direction for next session: prefer a fix that
    does not rely on finite-difference tangent sampling at all.
    Possibilities to explore: derive the tangent analytically from
    the path's segment definition (e.g. parse the `d` string and
    evaluate the bezier derivative for the line route, or use the
    known H/V segment direction for snake/below routes); or skip
    the perpendicular-normal model entirely in favor of a fix that
    doesn't need a tangent at all. Sampling-based normals are the
    current weak point — replace the mechanism rather than tune
    its eps.
  - Resolved next session (same day, $283.34 → $285.65). Three
    layered changes in `AnimatedEdge.tsx`:
    1. Lifted reactflow's bezier control-point math into a local
       `buildPathGeom` helper. Both the `d` string and the analytic
       control points/segments now come from one source — no string
       parsing of our own output, no dependence on reactflow internals.
    2. Replaced finite-difference tangent sampling with analytic
       tangent. For straights, segment unit vector. For the cubic,
       Newton-invert `B(t) = path.getPointAtLength(labelArcSvg)` to
       recover `t`, then evaluate `B'(t)`. Point and tangent share
       `t` by construction, eliminating the eps-clamp mismatch that
       made the tail tangent disagree with the tail point. (An
       intermediate attempt that built a chord-arc LUT and scaled
       to SVG total was structurally wrong — chord arc isn't
       proportional to SVG arc on a curl, so the recovered `t` was
       still off. Newton inversion sidesteps the conversion.)
    3. The visible "label rising at end" was actually compounded by
       SVG `<text>` defaulting to `dominant-baseline: alphabetic` —
       the y coordinate was the baseline, so glyphs rendered ~9–11px
       *above* the translate point, stacking with the 10px normal
       offset. Setting `dominantBaseline="central"` made the
       configured offset visually match.
  - Lesson: when a "geometry" bug visually exceeds the configured
    parameter by ~one font height, suspect text baseline before
    suspecting more geometry. Cheap to check, easy to overlook.


---

<!-- source: session-log/2026-05-03-smoothness-audit-non-edit-interactions.md -->

## 2026-05-03 — smoothness audit (non-edit interactions)

**Branch:** task/smoothness-audit
**Mode:** smoothness audit (audits.md #5)
**Start cost:** $271.69

Scope: pan, zoom, node drag (no topology change), animation playback,
scrub, fold/unfold of existing folds, bookmark jump, replay, view
recall. Logging only — no fixes during pass.

- **Node drag — jerky during drag.** Dragging a node does not track
  the cursor smoothly; motion feels stepped/laggy rather than
  continuous. User would like smooth tracking during drag. Considered
  snap-to-grid on drag end, but no chosen grid size — too large a
  snap defeats the point of placing freely; no standard picked yet.
  (Open question, not a fix decision.)
- **Animation playback — pulses too fast overall.** Pulses and their
  attached data labels travel along edges very fast — hard to read
  the value as it moves.
- **Animation playback — per-edge speed inconsistency.** Speed is
  not consistent across edges. The Ack edge's pulse moves ~3× (or
  more) faster than the pulse leaving the input node. Suggests
  per-edge duration is decoupled from edge length (or vice versa).


---

<!-- source: session-log/2026-05-03-smooth-node-drag-un-snap.md -->

## 2026-05-03 — smooth node drag (un-snap)

**Branch:** task/smooth-node-drag
**Mode:** smoothness audit fix (audits.md #5, item 1 from prior log)
**Start cost:** $317.49

Drag-jerkiness root cause was not render cost (5 nodes / 6 edges).
ReactFlow had `snapToGrid={true}` with `snapGrid=[24, 24]` set on the
canvas — node position quantized to 24px steps during drag, which
*is* the "stepped/laggy rather than continuous" feel reported. Fix:
drop both props (and the unused `GRID` constant). No snap on drop
either, matching the prior log's note that no grid size was chosen.
Build + 157 tests green. User to verify in webview.


---

<!-- source: session-log/2026-05-03-slow-pulse-speed-1-2.md -->

## 2026-05-03 — slow pulse speed (1/2×)

**Branch:** task/slow-pulse-speed
**Mode:** smoothness audit fix (audits.md #5, "pulses too fast overall")
**Start cost:** $319.14

Per-edge speed inconsistency and probe re-run still open. Global
speed only this session. Single knob `PULSE_PX_PER_MS_AT_REF_TICK`
in [AnimatedEdge.tsx:251](../../../tools/topology-vscode/src/webview/rf/AnimatedEdge.tsx#L251)
halved 0.06 → 0.03. Everything visual (dot, dash window, riding
label) scales from this constant, so no per-edge follow-up needed
for the global tune. Build + 157 tests green. User to verify feel
in webview.


---

<!-- source: session-log/2026-05-03-match-cascade-svg-pulse-speed-0-08-px-ms.md -->

## 2026-05-03 — match cascade SVG pulse speed (0.08 px/ms)

**Branch:** task/pulse-speed-svg-match
**Mode:** smoothness audit follow-up — try the reference diagram's speed
**Start cost:** $319.86

After halving to 0.03 (prior entry), pulled the speed from
[diagrams/topology-chain-cascade.svg](../../../diagrams/topology-chain-cascade.svg)
("Edge pulses at 80 px/s"). Set
`PULSE_PX_PER_MS_AT_REF_TICK` → 0.08 in
[AnimatedEdge.tsx:251](../../../tools/topology-vscode/src/webview/rf/AnimatedEdge.tsx#L251).
Build + 157 tests green. User to judge feel; expected to be
noticeably faster than the prior 0.06 baseline that was called
"too fast", so revert is on the table.


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
