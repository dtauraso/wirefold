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
