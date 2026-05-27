# Feature Audit: Wirefold 3D Visual Editor (Planned vs. Implemented)

## 1. Summary

The plan was to replace the React Flow 2D editor with a Three.js/R3F 3D canvas (`ThreeView`) backed by a Go substrate (`paced_wire`) that enforces backpressure and slot-phase discipline. The cutover spec (`rf-to-r3f-cutover.md`, `3d-editor.md`) named a full editor: arcball navigation, select/pick, two-click edge creation, inline label edit, multi-select, delete, palette add, undo/redo, persistent view-saves, and a Fold node in both Go and 3D mesh form.

**Scorecard:** 26 features implemented and working; 15 cutover-debt items (10 restore-parity, 5 half-wired); 1 never-specced decision point; 3 accepted-for-build items.

---

## 2. Implemented and Working

| Feature | Evidence (file:line) |
|---|---|
| Three.js/R3F 3D canvas replacing React Flow | `tools/topology-vscode/src/webview/three/ThreeView.tsx` |
| Arcball rotation (pointer drag) | `ThreeView.tsx:~400` (pointerdown/move handlers) |
| Scroll dolly (zoom) | `ThreeView.tsx:~460` (wheel handler) |
| Dwell-pan via PanPad | `tools/topology-vscode/src/webview/three/PanPad.tsx` |
| Roll slider | `tools/topology-vscode/src/webview/three/RollSlider.tsx` |
| Raycast node pick / single select | `ThreeView.tsx:~600` (raycast + setSelectedNodeId) |
| Two-click edge creation | `ThreeView.tsx:~820–840` (pendingEdgeSource state + pushSnapshot call) |
| Bezier tube edges (`SingleEdgeTube`) | `tools/topology-vscode/src/webview/three/SingleEdgeTube.tsx` |
| Pulse bead animation | `tools/topology-vscode/src/webview/three/PulseBead.tsx` |
| Pulse delivered handshake (Go↔TS pacing) | `tools/topology-vscode/src/webview/rf/pump.ts`; `handoff.md` Phase 0 resolved |
| Validation flag colors (missing required input) | `tools/topology-vscode/src/webview/three/ThreeNodeMesh.tsx`; `parseSpec` diagnostics |
| Billboarded node labels | `ThreeView.tsx` (Billboard + Text from @react-three/drei) |
| Occlusion badge | `tools/topology-vscode/src/webview/three/OcclusionBadge.tsx` (or inline in ThreeView) |
| Nearest-N LOD | `ThreeView.tsx` (distance-sorted visibility culling) |
| Camera fit-on-load / re-fit on loadEpoch | `ThreeView.tsx:~1240` (fitCamera effect on loadEpoch) |
| Run / pause / stop controls | `tools/topology-vscode/src/webview/three/ControlBar.tsx` |
| Run-status pause accounting | `tools/topology-vscode/src/webview/three/store.ts` (runStatus state) |
| Debounced spec save | `tools/topology-vscode/src/webview/save.ts` (scheduleSave) |
| Load spec / load view from VS Code | `store.ts:loadSpec`, `store.ts:loadView` |
| Fold state module (RF-free) | `tools/topology-vscode/src/webview/state/folds.ts` |
| Node dimming | `tools/topology-vscode/src/webview/state/dimmed.ts` |
| Spec↔flow adapters | `tools/topology-vscode/src/webview/three/adapters/specToFlow.ts`, `flowToSpec.ts` |
| Edge seeds (ring-topology deadlock break) | Go loader `nodes/Wiring/loader.go`; `topology.json` `edgeSeeds` field |
| Paced wire substrate | `nodes/Wiring/paced_wire.go` |
| Trace/probe logging (4 JSONL files) | `.probe/`; `tools/topology-vscode/src/webview/probe.ts` |
| Error boundary | `tools/topology-vscode/src/webview/three/ErrorBoundary.tsx` (or ThreeView wrapper) |
| Node kinds: ChainInhibitor, InhibitRightGate, Input, ReadGate (TS + Go) | `tools/topology-vscode/src/webview/schema/node-defs.ts` (NODE_DEFS keys); `nodes/chaininhibitor/`, `nodes/inhibitrightgate/`, `nodes/input/`, `nodes/readgate/` |

---

## 3. Gaps — Three Causes

The gaps in this editor have three distinct causes: regression (worked in the RF editor, dropped in the R3F cutover), under-specification (planned only loosely, needs an explicit decision), and accepted-for-build (friction surfaced, promoted to committed work). The old single "planned-not-built" list conflated these, inflating the apparent backlog. The three-cause split makes each item actionable on its own terms.

### 3a. Cutover Debt — Worked in the RF editor, lost in the R3F move (restore-parity, bounded)

Each item existed and worked in the old React Flow editor. The R3F cutover dropped or half-wired it. Proof-of-prior-existence cited per line.

| Feature | Proof of prior existence |
|---|---|
| Multi-select (box/shift-click) | `AppView.tsx` `selectionMode={SelectionMode.Partial}`, `selectionOnDrag` |
| Node delete | `_handle-delete.ts` `onNodesDelete`; `deleteKeyCode={["Delete","Backspace"]}` |
| Edge delete | `_handle-delete.ts` `onEdgesDelete` |
| Edge reconnect (drag endpoint) | `_on-reconnect.ts` `onReconnectImpl` |
| Node palette / add-node UI | `panels/NodePalette.tsx` |
| Sublabel inline edit | `inline-edit.ts` `beginEditSublabel` |
| PseudoPanel | `panels/PseudoPanel.tsx` (was 2D-only; never had a 3D form) |
| Port drag (wire from handle) | RF native handle drag-to-connect |
| Edge-kind context menu | `app/EdgeContextMenu.tsx` |
| Edge midpoint drag | `edges/MidpointDragHandle.tsx` |

The following were started in the R3F move but left inert — the infrastructure landed without the completing wiring (marked **half-wired**):

| Feature | Status | Evidence |
|---|---|---|
| Undo/redo | **half-wired** — snapshot stack exists (`history.ts`); `pushSnapshot` called only on edge-create; node drags don't push | `tools/topology-vscode/src/webview/state/history.ts:27–29`; `ThreeView.tsx:832,1267–1271` |
| View-save on settle | **half-wired** — `markViewSynced` IS called in `store.ts:76` inside `loadView`; not called after camera/drag, so positions lost on reload | `tools/topology-vscode/src/webview/three/store.ts:76`; `save.ts:48,78` |
| Fit-view hotkey | **half-wired** — fit-on-load only (`ThreeView.tsx:~1240`); no f/Shift-F for manual re-fit | `ThreeView.tsx:~1240` |
| Folds (collapsed subgraph) | **half-wired** — view-state module exists (`folds.ts`); `getFolds()` wired into `specToFlow`; no 3D mesh render | `tools/topology-vscode/src/webview/state/folds.ts`; `store.ts:62` |
| Z-coordinate (node depth) | **half-wired** — schema parses `z`; always 0 in practice; no UI to set depth | `tools/topology-vscode/src/webview/schema/node-defs.ts`; `specToFlow` adapter |

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
| 3 | Undo coalescing at gesture level | S | One undo entry per drag gesture (snapshot on pointer-up, not per pointer-move). Same work as finishing the §3a half-wired undo for node drags. |

---

## 4. Known Correctness Flaws

Cross-referenced from `docs/planning/visual-editor/audit-correctness.md` (H1/H2/H3). STEP 1 verification updated H1 and H2 findings against current code:

| ID | Severity | Claim | Verification result |
|---|---|---|---|
| H1 | HIGH | `markViewSynced` never called → view-saves permanently dropped | **Partially stale.** `markViewSynced` is called in `store.ts:76` inside `loadView`. However it is NOT called after user-driven camera moves/drags, so post-interaction view state still does not persist. The audit-correctness.md claim holds in spirit (interactive view-saves are dropped) but the "never called" wording is no longer accurate. |
| H2 | HIGH | `setSpecMeta` never called → top-level spec metadata stripped on save | **Fixed.** `setSpecMeta(spec)` is called in `store.ts:66` inside `loadSpec`. The audit-correctness.md entry is stale; this path is now wired. |
| H3 | HIGH | `loadView` before `loadSpec` silently drops the view (order-dependent) | **Unverified change.** `store.ts:loadView` now calls `specToFlow` only when `_lastSpec` is non-null, logging `store:view-load-noop` otherwise (`store.ts:84`). The ordering dependency still exists; arriving view-load before spec-load produces no nodes. |

See `audit-correctness.md` for full reproduction steps and original severity ratings.

---

## 5. Code with No Plan Doc

These features were built but do not appear in `3d-editor.md`, `rf-to-r3f-cutover.md`, or the session-log cutover checklist:

| Feature | Location |
|---|---|
| PanPad dwell-pan (hold-to-pan overlay) | `tools/topology-vscode/src/webview/three/PanPad.tsx` |
| Roll slider (camera roll axis control) | `tools/topology-vscode/src/webview/three/RollSlider.tsx` |
| Occlusion badge (node hidden-behind indicator) | Referenced in ThreeView; no plan doc entry |
| Nearest-N LOD (distance-based mesh culling) | ThreeView; not in cutover checklist |
