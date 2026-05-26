---
branch: task/full-code-audit
---

# R3F 3D Editor — Read-Only Correctness Audit

Scope: `tools/topology-vscode/src/webview/three/ThreeView.tsx`,
`three/store.ts`, `rf/adapter/{spec-to-flow,flow-to-spec}.ts`,
`state/viewer/types.ts`, plus the save/load wiring in `main.tsx`,
`save.ts`, `SaveLifecycle.tsx`.

Findings are not fixes. Severity is the reviewer's estimate of
user-visible impact × likelihood.

---

## HIGH

### H1 — View-side persistence is permanently a no-op; `markViewSynced` never called

- `save.ts:78` `markViewSynced(text)` is exported but **never called**
  anywhere in the R3F entry path (`grep` confirms only the definition).
- `save.ts:52` `performViewSave()` early-returns while
  `lastViewSyncedText === undefined`.
- `save.ts:70` `scheduleViewSave()` *also* early-returns while
  `lastViewSyncedText === undefined`.
- `store.ts:66` `loadView()` calls `setViewerState(next)` but does **not**
  call `markViewSynced`, the only thing that would set `lastViewSyncedText`.

Consequence: `lastViewSyncedText` stays `undefined` for the entire session,
so **every view-save is silently dropped**. The node-drag commit path in
`ThreeView.tsx:904-910` writes `{x,y}` into viewerState and calls
`scheduleViewSave()` + `scheduleSave()` — the `scheduleViewSave()` is a
no-op, so dragged node positions are never written to the `.view.json`
sidecar. (The spec save *does* fire, but spec carries no position.)

Repro: open editor, drag a node, reload — node snaps back to its loaded
position (or 0,0 if the sidecar had none). Severity HIGH: core "move a node"
gesture does not persist.

### H2 — `setSpecMeta` never called; top-level spec metadata lost on every save

- `save.ts:13` `setSpecMeta(s)` exported, **never called** in the R3F path.
- `save.ts:12` `_specMeta` therefore stays `{ nodes: [], edges: [] }`.
- `save.ts:41` `performSave()` passes `_specMeta` as `currentSpec` to
  `flowToSpec`, which uses it for (a) `notes` fallback (`flow-to-spec.ts:98`)
  and (b) all passThrough metadata fields (`flow-to-spec.ts:104-108`).

Consequence: any top-level `topology.json` field marked `passThrough` in
`TOPOLOGY_META_FIELDS` (and the notes-fallback) is **stripped on the first
save** the user triggers (e.g. creating an edge → `store.ts:150`
`scheduleSave()` → `performSave()`). Loading restores the spec into the
store via `loadSpec`, but the store never feeds those top-level fields back
to `save.ts`. Severity HIGH: silent data loss in the persisted file.

Note: `loadSpec` (`store.ts:53-64`) parses the spec and keeps it in
`_lastSpec`, but nothing bridges `_lastSpec`/parsed meta to `setSpecMeta`.

### H3 — `loadView` before `loadSpec` silently drops the view

- `store.ts:69` `loadView` reads `_lastSpec`; if null it sets
  `viewerState` but never rebuilds `nodes/edges`, so positions/sublabels/z
  from the sidecar are not applied.
- `main.tsx:67-72` handles `load` then `view-load` as independent messages.
  Order is host-controlled. The comment at `main.tsx:77-79` claims the
  ready-signal guarantees ordering, but only guarantees *delivery*, not
  that `load` arrives before `view-load`.

If `view-load` arrives first (or `load` fails to parse — `store.ts:61`
swallows the error and leaves `_lastSpec` null), the view is applied to a
`ViewerState` that is then **never re-projected** onto nodes, because
`loadSpec` later runs `specToFlow` with the *current* `viewerState`
(`store.ts:57`) — which is actually fine *iff* `setViewerState` ran first.
The real bug is the asymmetric path: `loadView` with no spec mutates global
`viewerState` but produces no nodes, and there is no retry. Severity HIGH
(ordering-dependent): on the unlucky order, the sidecar positions are
applied only because `loadSpec` happens to re-read the mutated global — a
fragile implicit coupling, not a contract.

---

## MEDIUM

### M1 — Pick-by-position `scene.traverse` + 1-unit position match is fragile

- `ThreeView.tsx:509-526` `RaycasterHelper` traverses **all** meshes in the
  scene each pick, then maps the hit's parent group `position` back to a
  node by `Math.abs(... ) < 1` on x and y only (z ignored).

Risks:
1. Two nodes whose world centers are within 1 unit in x *and* y (different z,
   or near-overlapping in the flattened z=0 layout) resolve to whichever
   appears first in `nodes` (`ThreeView.tsx:518`) — wrong-node pick.
2. The pulse bead (`ThreeView.tsx:284`), edge tubes, and any future mesh are
   all collected into `meshes` and raycast every pick; the parent-group walk
   `hits[0].object.parent` (`:515`) assumes the first hit's parent is a node
   group. A pulse-bead hit (no matching node position) returns null → a
   pick that lands on a bead in front of a node mis-reports empty space.

Severity MEDIUM: mis-picks under overlap; the 1-unit tolerance is a magic
constant unrelated to node radius (`nodeRadius` at `:50` can be << or >> 1).

### M2 — Roll slider zero-drift: roll state decoupled from camera quaternion

- `ThreeView.tsx:967-983` `RollSlider` tracks `rollDeg` / `prevRoll` as the
  *single source of roll truth* and applies only the **delta** to the camera
  (`:975` `delta = newDeg - prevRoll.current`).
- Arcball drag (`applyArcball`, `:712-740`) and dolly/pan mutate
  `cam.quaternion` directly without touching `prevRoll`/`rollDeg`.

Consequence: after any arcball rotation, the slider's notion of "0 roll" no
longer corresponds to the camera's actual screen-plane roll. The slider
value is stale; dragging it applies a delta about the *current* forward axis
(`:980`), so the displayed angle drifts away from the visual roll. Resetting
the slider to 0 does **not** restore zero roll. Severity MEDIUM: widget lies
about state; recoverable by re-dragging but confusing.

### M3 — `CameraSettleDetector` per-frame string snapshot

- `ThreeView.tsx:412-424` builds a 16-float `toFixed(2)` join **every frame**
  (`frameloop="always"`, `:1345`) and string-compares to detect camera
  motion.

Risk: allocation + 16 `toFixed` + `join` per frame at 60fps purely to drive
the occlusion-badge recompute debounce. Two-decimal rounding also means
sub-0.01 drift (slow auto-motion) never settles, or conversely a camera that
stops at a non-rounding boundary can flap. Severity MEDIUM (perf/correctness
minor): not a logic bug for users, but it's a hot-path string alloc and the
rounding makes "settled" detection imprecise. Cheaper: compare a few matrix
elements numerically with an epsilon.

### M4 — Node-drag reads stale `nodesRef` for plane-anchor, store for commit

- Drag start (`ThreeView.tsx:786-792`) captures `nodeCenterAtStart` from
  `nodesRef.current` (state mirror, one render behind, synced via effect at
  `:1213-1215`). Move (`:849`) and commit (`:902`) read different sources
  (`nodesRef` vs `useThreeStore.getState()`). The commit comment
  acknowledges the mirror can be a frame stale.

If a drag begins in the same frame nodes change (e.g. an edge-create
re-render), `nodeCenterAtStart` could be taken from the pre-update mirror
while subsequent moves compute deltas against it — small position jump.
Severity MEDIUM: narrow timing window, self-corrects on next drag.

---

## LOW

### L1 — `z` coordinate dropped on node-drag persistence

- `state/viewer/types.ts:42` `NodeView.z` is parsed (`parse.ts:69`) and fed
  into `data.z` (`spec-to-flow.ts:66`), but the drag commit
  (`ThreeView.tsx:907`) writes only `{ x, y }`, spreading over `existing`.
  Existing `z` is preserved (good), but there is no path to *edit* z, and
  any code expecting drag to set z is wrong. Severity LOW: z is currently
  always 0 in this flattened layout; asymmetry is latent.

### L2 — `connectPendingId` selection-suppression edge case

- On a connect-mode second click that lands on empty space,
  `onConnectClick` (`ThreeView.tsx:1251`) cancels connect mode, and
  `onPointerUp` (`:933`) suppresses `onSelect` because `inConnectMode` was
  true. Correct. But on the *first* connect click, `onConnectClick` sets
  pending and select is also suppressed — so entering connect mode clears
  any prior selection silently. Minor UX, not data loss. Severity LOW.

### L3 — Pointer capture released implicitly only; no explicit release on early-out

- `onPointerDown` (`:808`) calls `setPointerCapture`. There is no matching
  `releasePointerCapture`; capture auto-releases on pointerup, which is fine
  for the normal path. But if `onPointerUp` early-returns via the node-drag
  branch (`:914`) the capture still releases (browser auto-release on up), so
  no leak. Flagged only because no explicit release exists; if a future
  branch swallows pointerup the capture would stick. Severity LOW.

### L4 — `createEdge` kind fallback to `"any"` vs flow-to-spec default `"signal"`

- `store.ts:120-122` a new edge with mismatched/unknown port kinds gets
  `kind: "any"`. `flow-to-spec.ts:80` defaults missing kind to `"signal"`.
  These disagree, but `createEdge` always sets `data.kind`, so the
  flow-to-spec fallback isn't hit for store-created edges. Latent
  inconsistency only. Severity LOW.

---

## Adapter round-trip (spec↔flow) assessment

`specToFlow`/`flowToSpec` are field-symmetric for spec data (id, type, index,
props, spec, notes, inputs, outputs, node.data/state/edgeSeeds packed at
`data.*`, wire props via `WIRE_PROPS`). No data loss found in the pair
*itself*. The asymmetry that bites is **external**: `flowToSpec` depends on a
`currentSpec` arg that `save.ts` never populates (H2), so notes-fallback and
passThrough meta are lost at the call site, not in the adapter.
`specToFlow` emits fold/note nodes that `flowToSpec` correctly skips/rebuilds.

---

## Connect A→B trace (the "store fix in place, never visually confirmed")

Path: `onPointerUp` CLICK branch (`ThreeView.tsx:919-936`) → reads
`connectPendingIdRef.current` (live, avoids stale closure — correct) →
`onConnectClick(hitId)` (`:1243`) → second click calls
`storeCreateEdge(pending, null, hitId, null)` (`:1256`) → `store.createEdge`
(`store.ts:90`) → auto-picks first output/input port (`:108-109`) → builds
edge, `set({ edges })`, `scheduleSave()` (`:148-150`).

The store action is sound and **does** persist the edge to the spec (H1/H2
do not block edge creation — spec save fires). The likely reasons it "was
never visually confirmed":

1. **Port auto-pick can silently bail** (`store.ts:111`): if either node has
   no outputs/inputs resolvable from `NODE_TYPES[type]` *or* `data.outputs/
   inputs`, `sourceHandle`/`targetHandle` is null → `return null`, no edge,
   no feedback. A node kind with empty `outputs` produces a silent no-op.
2. **Duplicate-target guard** (`store.ts:113`): if the target's first input
   handle already has an edge, `createEdge` returns null silently — clicking
   B when B's sole input is wired no-ops with no UI hint.
3. The new edge renders via `GraphEdges`→`SingleEdgeTube`, which requires
   both endpoints in `nodeMap` (`ThreeView.tsx:339-341`); fine for normal
   nodes but a connect to/from a *collapsed-fold member* (not in `nodes`)
   would create a store edge that renders as nothing.

No bug that makes the *happy path* no-op was found; the no-ops are the three
silent `return null` guards. Severity of the silent-bail UX: MEDIUM
(grouped under M-class; user gets no feedback distinguishing "wired" from
"refused").

---

## Ranked summary

| ID | Sev | One-liner |
|----|-----|-----------|
| H1 | HIGH | `markViewSynced` never called → all view-saves dropped → node drags don't persist |
| H2 | HIGH | `setSpecMeta` never called → top-level/passThrough spec metadata stripped on every save |
| H3 | HIGH | `loadView`-before-`loadSpec` produces no nodes; correctness rests on implicit global-`viewerState` coupling |
| M1 | MED  | Pick-by-position scene.traverse + 1-unit x/y match → mis-pick under overlap / bead-in-front |
| M2 | MED  | Roll slider tracks roll independently of camera; drifts after any arcball rotation |
| M3 | MED  | CameraSettleDetector per-frame 16-float toFixed/join; rounding makes settle imprecise |
| M4 | MED  | Node-drag anchor from stale `nodesRef`, commit from store — narrow jump window |
| L1 | LOW  | Drag persists only x,y; z editing path absent (latent) |
| L2 | LOW  | Entering connect mode silently clears selection |
| L3 | LOW  | No explicit pointer-capture release (fine today, fragile to future early-outs) |
| L4 | LOW  | createEdge kind `"any"` vs flow-to-spec `"signal"` default disagree (latent) |
