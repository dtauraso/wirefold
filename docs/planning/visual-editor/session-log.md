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

## 2026-06-27 — Shipped summary (folded from the retired handoff.md "What shipped")

Durable record of work that reached `main`, relocated when `handoff.md` was retired in
favor of git-derived state (`tools/next.sh` + branch descriptions):

- **Viewpoint nav math moved TS → Go; camera is Go-owned and POLAR.** Go holds
  `(pivot, r, pos, up)` (`nodes/Wiring/viewpoint.go`) and does angle-only spherical trig
  (`spherical.go`: `dir{θ,φ}`, `rot{axis,angle}`, `rotateDir`/`arcBetween`/`angleAboutAxis`).
  Trig is **epsilon-free** (great-circle bearing form, `atan2` of two unnormalized terms —
  no `/sinθ`, no pole special-case). Four gestures route through Go: `edit` op `viewpoint`
  (`kind` = set/orbit/orbit-locked/zoom/pan) in, `camera` trace event out. TS does only the
  two edge conversions (pointer px→angles; polar→three.js, quaternion at draw) —
  `viewpoint-bridge.ts`, `CameraFromStore.tsx`. Persists as `cameraPolar` in scene.json.
  Only Cartesian in Go is `pivot` (translated, never rotated). Zoom-to-cursor is a dolly = pan.
- **Pick resolution** by `userData.nodeId`/`body` across all pick paths; z-blind proximity
  fallback gone; handholds excluded from node picks.
- **Port-move** projects pointer onto the node's own ring plane (`z = nodeCenter.z`), not `z=0`.
- **Dynamic port auto-aim** (`AimedPortRegistry`, `aimed_ports.go` + loader.go): edges 1→2,
  1→6, 1→8, 2→3, 2→7 aim source port at child and child input back — radial spokes from LOAD.
  Node 8 `FeedbackOut` (8→1) stays ring-anchored and manually movable.
- **θ-lock** (`thetaLock`, lock.go): nodes 2 & 6 on 1, and 3 & 7 on 2, share θ, each keeps its
  own φ. Registered by id in loader.go (stopgap, like the chord lock).
- **Node kind `Excitatory` → `Pulse`** (pure rename; package `nodes/pulse`; nodes 6,7 `Pulse`).
- **HoldFlip (node 4)** mirrors Pulse: main loop drains input to LATEST + updates interior bead
  immediately; drive goroutine continuously pulses the flip (`1-held`). Continuous-drive output;
  before any input it drives the `-1` placeholder.
- **WindowAndGate (node 5)** discards `-1` placeholders, re-samples each side to most-recent real bead.
- **Trace** serializes stdout writes (`drain()` holds the mutex across the sink write).
- **Polar frame markers + scene-tori "rings" toggle** (theta-lock-diag keepers): camera-
  independent +y/+x/+z axis markers + labels in NavGuides.tsx; Go-owned scene-tori show/hide.

---

## 2026-06-12 — post-redesign follow-ups: prebuilt-binary runner + zombie-bead reset (2 branches)

**Observation:** After the persistence redesign merged, two friction points surfaced
driving the editor: (a) every launch re-linked via `go run .`; (b) a bead in-flight when
STOP killed Go reappeared as a zombie in the next run (seen on `2To3`).

**Decision:** two task branches, both merged + deleted.

**Outcome:**
- **Prebuilt-binary runner + `.go` watcher + orphan reap** (merge `6c8a1f31`,
  `task/prebuilt-binary-runner`): editor spawns a prebuilt
  `<repoRoot>/.wirefold-cache/wirefold` (gitignored) instead of `go run .`. Lazy staleness
  check (`ensureBinaryBuilt`, `runCommand.ts`) + eager `**/*.go` `FileSystemWatcher`
  (`extension.ts`, 250ms debounce) both rebuild via shared `buildBinary()` (`goBuild.ts`,
  module-level `building` guard = wait-free coalesce). `killOrphanedSims()` SIGKILLs
  leftover sims from crashed sessions on launch. First launch after fresh checkout does a
  one-time `go build`; reused until a `.go` changes.
- **Zombie-bead-on-restart fix** (merge `f40260b8`, `task/clear-pulses-on-restart`): added
  `clearAllPulses()` (swaps in a fresh empty `Map` in `webview/three/pulse-state.ts`),
  called at the TOP of `store.load()`. Go emits its startup spec → `load` on every restart,
  so each run wipes prior transient beads; pause doesn't route through `load()` so beads
  correctly persist across pause. Pure render-state reset at the run boundary — no change to
  wire timing or the bead model. Clear-on-bare-stop declined (beads linger until next run by
  choice).

---

## 2026-06-12 — topology-tree / Go-owns-persistence redesign: live bringup (task/persist-geometry-from-go-stream)

**Observation:** The 4-phase redesign (tree reader, tree writer, command-launched panel,
flowToSpec retirement) was built and statically verified last session but had not been
exercised in the live editor.

**Scope:** Bring the redesign up in the live editor; confirm the original branch goal
(port anchor survives reload) end to end.

**Five real fixes needed after the build:**
1. Startup load-race — Go's one-shot spec line beat the webview listener; fixed by caching
   `lastSpec` in the extension host and replaying `"load"` on webview `"ready"`.
2. Dead document-gate — `handle-message.ts` gated webview-log on the removed custom-editor
   `document`; fixed by threading a `logUri` into `MessageCtx` and removing the gates.
3. parseSpec schema mismatch — tree `EmitSpecLine` dropped port `kind` and edge `id` that
   `parseSpec` required; fixed by emitting edge `id`, applying `json:"-"` to
   `specNode.Position`, defaulting `kind` in `parsePort`, and adding a load-error breadcrumb
   to surface silent `store.load` throws to `ts-errors.jsonl`.
4. Auto-fit not firing / wrong framing — `CameraFitter` bailed before Go geometry was
   present; fixed to fit once-per-epoch gated on full Go geometry; dropped y-negation to
   match manual Fit.
5. `tree_writer` now emits compact single-line JSON matching the fixtures.

**Outcome:** Live-verified persistence round-trip: dragging a node wrote
`topology/view/nodes/<id>.json`; dragging a port anchor wrote
`topology/nodes/<id>/inputs/<port>.json` with an `"anchor"` field; on reload the spec
stream carried both the moved position AND the port anchor. The original branch goal
confirmed. Redesign complete; merging to `main` this session.

---

## 2026-06-01 — InhibitRightGate window verified live (no task branch)

**Observation:** handoff open-item #2 asked whether InhibitRightGate's coincidence window had ever been validated against actual input alignment in the live ring, or whether the ring was merely "not starved."

**Finding 1 — existing coverage understated:** the open-item wording was stale. InhibitRightGate already has direct unit coverage in `nodes/inhibitrightgate/firing_rule_test.go` (TestWindowFire / TestWindowClear). ReadGate's window (commit 48749fd) explicitly mirrors it. "Not independently verified" understated what was already in the test suite.

**Finding 2 — ring cannot run headlessly:** `go run . -duration=20s` builds and starts but deadlocks after the first hop — only bootstrap_rg + in08 fire, then nothing. Cause: poll-and-hold delivery. A Send marks a bead inFlight; the value only enters the destination slot when NotifyDelivered fires, which is driven by the visual layer (webview pulse-completion → stdin reader). No editor = no delivered messages = ring stalls. By design, not a bug. To exercise the ring you need the live editor (.probe relay) or a headless delivery driver.

**Finding 3 — live measurement:** cleared .probe, ran once in the editor. 12.1 s, 18 fires across 6 nodes, 0 errors. Per gate: inhibitRight0 = 2 fires / 0 window_clear; readGate1 = 4 fires / 0 window_clear. The gate's inputs genuinely coincide within W in the live ring — it is not surviving via lucky non-starvation. Caveat: small sample (2 fires / 12 s); a 60 s run would strengthen the ratio, but the qualitative signal (zero clears, zero errors) is unambiguous.

**Decision:** log-only (no task branch). Open-item #2 resolved.

**Outcome:** open-item #2 closed. Handoff updated.

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



---

## 2026-05-27 — Load-transport collapse + 3D camera persistence (task/collapse-load-transport)

**Observation:** Two separate correctness flaws (H1 and H3) both verified fixed and working in the editor.

**H3 — Single-message load transport (order-fragility gone):** The old two-message protocol (`load` + `view-load`) let `view-load` arrive before `load`, silently dropping the view (the `_lastSpec` reorder cache only partially mitigated this). Collapsed into one `load` message and one `load(text)` store action that parses spec + `topology.json#view` together and builds flow once. Deleted `loadView`, `_lastSpec`, `view-load-noop` branch, `view-load` message variant, and host-side `sendView`. On-disk representations were already merged (single `topology.json` with a `view` key) by a prior effort; this collapsed only the in-memory transport.

**H1 — 3D camera persistence:** The old `viewerState.camera` was RF pan/zoom only — Three.js PerspectiveCamera state was never persisted. Added `Camera3D` (position + quaternion) to viewer-state schema and parser; committed on orbit/dolly/pan/roll gesture-end via `scheduleViewSave`; restored on mount, skipping auto-fit when a saved camera exists. A follow-up fixed a React effect-deps timing bug: `camera3d` arrives async after first render, so `initialCamera3d` was added to `CameraRefBridge` effect deps + `updateMatrixWorld` called to force the matrix before the skip-auto-fit guard ran.

**Outcome:** Both verified working in the editor — load preserves node positions/fade; rotate+reload restores orientation; topologies with no saved camera still auto-fit. Branch ready to merge pending sign-off.

---


## 2026-05-14 — Integration test suite (task/integrated-go-tests)

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

**Go finding:** relay fires only on input fill (fill→onRun). Sequential
drain via relay requires timer advancement; E1 test uses direct input→readgate
to observe canAccept-triggered sequential delivery.

125 tests passing, all green.

---

## 2026-06-17 — pan-guide triangle: right angle on the view-aligned torus

The pan-guide right angle wasn't landing on the visible tori intersection: after the tori
were made view-aligned (horizontal-torus normal = camera up), the disk∩torus intersection and
the triangle base still used world Y, so they sat on the world equator instead of the visible
torus. Briefly tried a Thales triangle (right angle guaranteed on the circle) but the
diameter-hypotenuse made it span the whole sphere and drift off-view
([drift 1](screenshots/2026-06-17-panguide-triangle-drift-1.png),
[drift 2](screenshots/2026-06-17-panguide-triangle-drift-2.png)). Fix: use the camera-up as
the pole so the green intersection line and the compact C–Q–P right triangle (right angle at
the foot Q) land on the visible horizontal torus.
