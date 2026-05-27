# Feature Audit: Wirefold 3D Visual Editor (Planned vs. Implemented)

## 1. Summary

The plan was to replace the React Flow 2D editor with a Three.js/R3F 3D canvas (`ThreeView`) backed by a Go substrate (`paced_wire`) that enforces backpressure and slot-phase discipline. The cutover spec (`rf-to-r3f-cutover.md`, `3d-editor.md`) named a full editor: arcball navigation, select/pick, two-click edge creation, inline label edit, multi-select, delete, palette add, undo/redo, persistent view-saves, and a Fold node in both Go and 3D mesh form.

**Scorecard:** 26 features implemented and working; 15 cutover-debt items (10 restore-parity, 4 half-wired, 1 not-started); 1 never-specced decision point; 3 accepted-for-build items; 0 dead-code orphans (§3d — all removed).

> **Re-verified 2026-05-26** against post-architecture-audit code. Undo/redo moved from half-wired to not-started (no history.ts, no pushSnapshot exists). Folds filename corrected. Sublabel edit and edge midpoint drag annotations updated.
> **Updated 2026-05-27** (commit `45cee602`, branch `task/remove-saved-views`): saved-views dead-code orphan REMOVED in full. Scorecard adjusted from 4 to 3 dead-code orphans.
> **Updated 2026-05-27** (commits `9cc63677`, `93b6412a`, branch `main`): `valueLabel` wire prop and fold mutators (`setFolds`, `toggleFoldCollapse`, `updateFoldPosition`) REMOVED. Orphan count → 0.

---

## 2. Implemented and Working

| Feature | Evidence (file:line) |
|---|---|
| Three.js/R3F 3D canvas replacing React Flow | `tools/topology-vscode/src/webview/three/ThreeView.tsx` (now ~264-line orchestrator; geometry in `geometry-helpers.ts`, render in `scene-content.tsx`) |
| Arcball rotation (pointer drag) | `tools/topology-vscode/src/webview/three/interaction-controls.ts` (pointer state machine extracted from ThreeView) |
| Scroll dolly (zoom) | `tools/topology-vscode/src/webview/three/interaction-controls.ts` (wheel handler) |
| Dwell-pan via PanPad | `tools/topology-vscode/src/webview/three/camera-ui.tsx` (PanPad extracted from ThreeView) |
| Roll slider | `tools/topology-vscode/src/webview/three/camera-ui.tsx` (RollSlider extracted from ThreeView) |
| Raycast node pick / single select | `tools/topology-vscode/src/webview/three/interaction-controls.ts` (raycast + setSelectedNodeId) |
| Two-click edge creation | `tools/topology-vscode/src/webview/three/interaction-controls.ts`; edge built in `tools/topology-vscode/src/webview/three/edge-creation.ts` (`buildEdge`) |
| Bezier tube edges (`SingleEdgeTube`) | `tools/topology-vscode/src/webview/three/SingleEdgeTube.tsx` |
| Pulse bead animation | `tools/topology-vscode/src/webview/three/PulseBead.tsx` |
| Pulse delivered handshake (Go↔TS pacing) | `tools/topology-vscode/src/webview/three/pump.ts`; `handoff.md` Phase 0 resolved |
| Validation flag colors (missing required input) | `tools/topology-vscode/src/webview/three/ThreeNodeMesh.tsx`; `parseSpec` diagnostics |
| Billboarded node labels | `tools/topology-vscode/src/webview/three/scene-content.tsx` (Billboard + Text from @react-three/drei) |
| Occlusion badge | `tools/topology-vscode/src/webview/three/OcclusionBadge.tsx` (or inline in scene-content.tsx) |
| Nearest-N LOD | `tools/topology-vscode/src/webview/three/scene-content.tsx` (distance-sorted visibility culling) |
| Camera fit-on-load / re-fit on loadEpoch | `tools/topology-vscode/src/webview/three/ThreeView.tsx` (fitCamera effect on loadEpoch; ThreeView orchestrator) |
| Run / pause / stop controls | `tools/topology-vscode/src/webview/three/ControlBar.tsx` |
| Run-status pause accounting | `tools/topology-vscode/src/webview/three/store.ts` (runStatus state) |
| Debounced spec save | `tools/topology-vscode/src/webview/save.ts` (scheduleSave) |
| Load spec / load view from VS Code | `store.ts:loadSpec`, `store.ts:loadView` |
| Fold state module (RF-free) | `tools/topology-vscode/src/webview/state/folds-state.ts` |
| Node dimming | Removed with saved-views teardown (commit `45cee602`, branch `task/remove-saved-views`). `dimmed.ts`, `data.dimmed` in specToFlow/NodeData, `.dim` CSS, and `__wirefold_test.applyDim` hook all deleted. |
| Spec↔flow adapters | `tools/topology-vscode/src/webview/three/adapters/specToFlow.ts`, `flowToSpec.ts` |
| Ring-topology deadlock break | Bootstrap `Input` node (`data.init=[seed]`, `data.repeat=false`) wired by a real edge into the receiving port; single-fires seed at tick-0. `edgeSeeds` TS pipeline removed entirely. |
| Paced wire substrate | `nodes/Wiring/paced_wire.go` |
| Trace/probe logging (4 JSONL files) | `.probe/`; `tools/topology-vscode/src/webview/probe.ts` |
| Error boundary | `tools/topology-vscode/src/webview/three/ErrorBoundary.tsx` (or ThreeView wrapper) |
| Node kinds: ChainInhibitor, InhibitRightGate, Input, ReadGate (TS + Go) | `tools/topology-vscode/src/schema/node-defs.ts` (NODE_DEFS keys; moved from `src/webview/schema/`); `nodes/chaininhibitor/`, `nodes/inhibitrightgate/`, `nodes/input/`, `nodes/readgate/` |

---

## 3. Gaps — Three Causes

The gaps in this editor have three distinct causes: regression (worked in the RF editor, dropped in the R3F cutover), under-specification (planned only loosely, needs an explicit decision), and accepted-for-build (friction surfaced, promoted to committed work). The old single "planned-not-built" list conflated these, inflating the apparent backlog. The three-cause split makes each item actionable on its own terms.

### 3a. Cutover Debt — Worked in the RF editor, lost in the R3F move (restore-parity, bounded)

Each item existed and worked in the old React Flow editor. The R3F cutover dropped or half-wired it. Proof-of-prior-existence cited per line. Note: all proof files listed below (`AppView.tsx`, `_handle-delete.ts`, `panels/*`, `app/EdgeContextMenu.tsx`, `edges/MidpointDragHandle.tsx`) have since been deleted from the tree in the R3F cutover; the proof is git-history-only, not checkable in-tree.

| Feature | Proof of prior existence |
|---|---|
| Multi-select (box/shift-click) | `AppView.tsx` `selectionMode={SelectionMode.Partial}`, `selectionOnDrag` |
| Node delete | `_handle-delete.ts` `onNodesDelete`; `deleteKeyCode={["Delete","Backspace"]}` |
| Edge delete | `_handle-delete.ts` `onEdgesDelete` |
| Edge reconnect (drag endpoint) | `_on-reconnect.ts` `onReconnectImpl` |
| Node palette / add-node UI | `panels/NodePalette.tsx` |
| Sublabel inline edit | `inline-edit.ts` `beginEditSublabel` — **PARTIAL**: `beginEditSublabel` still exists in `src/webview/inline-edit.ts`; what's missing is a 3D gesture to trigger it (RunButton only calls `flushActiveInlineEdit`) |
| PseudoPanel | `panels/PseudoPanel.tsx` (was 2D-only; never had a 3D form) |
| Port drag (wire from handle) | RF native handle drag-to-connect |
| Edge-kind context menu | `app/EdgeContextMenu.tsx` |
| Edge midpoint drag | `edges/MidpointDragHandle.tsx` — **FULLY MISSING**: `midpointOffset` is schema-declared only (`src/schema/wire-defs.ts`); no adapter reads it, no setter exists, no drag UI wired |

The following were started in the R3F move but left inert — the infrastructure landed without the completing wiring (marked **half-wired**). One item (undo/redo) has no infrastructure at all and is marked **not-started**:

| Feature | Status | Evidence |
|---|---|---|
| Undo/redo | **NOT-STARTED** — no `history.ts`, no `pushSnapshot` anywhere; node drags and edge-create both only call `scheduleSave`/`scheduleViewSave`; undo/redo does not exist at all | — |
| View-save on settle | **half-wired** — `markViewSynced` IS called inside `loadView`; not called after camera/drag, so positions lost on reload | `tools/topology-vscode/src/webview/three/store.ts`; `save.ts:48,78` |
| Fit-view hotkey | **half-wired** — fit-on-load only (ThreeView orchestrator); no f/Shift-F for manual re-fit | `tools/topology-vscode/src/webview/three/ThreeView.tsx` |
| Folds (collapsed subgraph) | **half-wired** — view-state module exists (`state/folds-state.ts`); `getFolds()` wired into `specToFlow` via `buildFoldNodes` (renders as React Flow "note" placeholder nodes); no 3D mesh render | `tools/topology-vscode/src/webview/state/folds-state.ts`; `tools/topology-vscode/src/webview/three/store.ts` |
| Z-coordinate (node depth) | **half-wired** — schema parses `z`; always 0 in practice; no UI to set depth | `tools/topology-vscode/src/schema/node-defs.ts`; `specToFlow` adapter |

---

### 3b. Never-Specced — Planned only loosely, needs a decision

| Feature | Open question |
|---|---|
| Fold node Go primitive | No `nodes/fold/` package has ever existed; fold was always view-state only. Needs an explicit yes/no: does fold ever become a Go substrate node, or stay view-state forever? |

---

### 3c. Accepted for Build — friction surfaced, promoted to committed work

Items promoted because real use justified building them. These are committed work, not parked patterns.

| # | Feature | Size | Notes |
|---|---|---|---|
| 1 | Bend points / waypoints on orthogonal edges | M-L | Generalizes the cutover-debt "Edge midpoint drag" (§3a) from a single midpoint to arbitrary waypoints; per-edge persisted state threaded through schema + adapters. |
| 2 | Multi-node alignment guides | S | Generalizes existing single-node drag guides/snap to a multi-selection's collective bounding box. Gated behind multi-select restore (§3a cutover debt). |
| 3 | Undo coalescing at gesture level | S | One undo entry per drag gesture (snapshot on pointer-up, not per pointer-move). This is a from-scratch undo/redo build (§3a undo is not-started, not half-wired). |

---

### 3d. Dead-Code Orphans — built/specced, never surfaced (sweep 2026-05-27)

Each is infrastructure that exists in code but reaches no user today — some never wired, some deliberately unwired. Decide per item: wire it up, or delete it as fossil.

| Feature | Evidence (file:line) | State | Disposition question |
|---|---|---|---|
| Named / saved views | **REMOVED** — commit `45cee602` on branch `task/remove-saved-views`. Deleted: `SavedView` type + parse/serialize + rename-remap; `state/dimmed.ts`; `data.dimmed` in specToFlow + NodeData; `.dim` CSS; `__wirefold_test.applyDim` hook; saved-view assertions within `parseViewerState.test.ts` (file retained); saved-view / `.dim` assertions in `compare-fold-and-view.spec.ts` (folds + diff assertions kept). Build/tsc/17 unit tests clean after removal. | COMPLETE — no orphan remains | N/A |
| Spec diff | **REMOVED** — `state/ops/diff.ts` + `test/diff-core.test.ts` deleted; `diffSpecs` had no production caller. | COMPLETE — no orphan remains | N/A |
| Wire value label | **REMOVED** — `valueLabel` deleted from `nodes/Wiring/loader.go` wire tag and `wire-defs.ts` regenerated via gen-node-defs (commit `9cc63677`, branch `main`). TS + Go builds clean. | COMPLETE — no orphan remains | N/A |
| Fold mutators | **REMOVED** — `setFolds`, `toggleFoldCollapse`, `updateFoldPosition` deleted from `state/folds-state.ts`; `getFolds` retained (live consumer in `store.ts`). (commit `93b6412a`, branch `main`). Build clean. | COMPLETE — no orphan remains | N/A |

> `midpointOffset` (§3a item 10) and sublabel inline edit (§3a item 6) were also surfaced by the same sweep but are already tracked there and are not duplicated here.

---

## 4. Known Correctness Flaws

Cross-referenced from `docs/planning/visual-editor/audit-correctness.md` (H1/H2/H3). STEP 1 verification updated H1 and H2 findings against current code:

| ID | Severity | Claim | Verification result |
|---|---|---|---|
| H1 | HIGH | `markViewSynced` never called → view-saves permanently dropped | **Partially stale.** `markViewSynced` is called inside `loadView` in `store.ts`. However it is NOT called after user-driven camera moves/drags, so post-interaction view state still does not persist. The audit-correctness.md claim holds in spirit (interactive view-saves are dropped) but the "never called" wording is no longer accurate. |
| H2 | HIGH | `setSpecMeta` never called → top-level spec metadata stripped on save | **Fixed.** `setSpecMeta(spec)` is called inside `loadSpec` in `store.ts`. The audit-correctness.md entry is stale; this path is now wired. |
| H3 | HIGH | `loadView` before `loadSpec` silently drops the view (order-dependent) | **Unverified change.** `store.ts:loadView` now calls `specToFlow` only when `_lastSpec` is non-null, logging `store:view-load-noop` otherwise. The ordering dependency still exists; arriving view-load before spec-load produces no nodes. |

See `audit-correctness.md` for full reproduction steps and original severity ratings.

---

## 5. Code with No Plan Doc

These features were built but do not appear in `3d-editor.md`, `rf-to-r3f-cutover.md`, or the session-log cutover checklist:

| Feature | Location |
|---|---|
| PanPad dwell-pan (hold-to-pan overlay) | `tools/topology-vscode/src/webview/three/PanPad.tsx` |
| Roll slider (camera roll axis control) | `tools/topology-vscode/src/webview/three/RollSlider.tsx` |
| Occlusion badge (node hidden-behind indicator) | Referenced in scene-content.tsx; no plan doc entry |
| Nearest-N LOD (distance-based mesh culling) | `tools/topology-vscode/src/webview/three/scene-content.tsx`; not in cutover checklist |
