---
branch: task/feature-audit
---

# Feature Audit: Wirefold 3D Visual Editor (Planned vs. Implemented)

## 1. Summary

The plan was to replace the React Flow 2D editor with a Three.js/R3F 3D canvas (`ThreeView`) backed by a Go substrate (`paced_wire`) that enforces backpressure and slot-phase discipline. The cutover spec (`rf-to-r3f-cutover.md`, `3d-editor.md`) named a full editor: arcball navigation, select/pick, two-click edge creation, inline label edit, multi-select, delete, palette add, undo/redo, persistent view-saves, and a Fold node in both Go and 3D mesh form.

**Scorecard:** ~20 features implemented and working; 5 partial/wired-but-inert; 11 planned but not yet built; 10 explicitly deferred post-v0 (friction-driven).

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

## 3. Partial / Wired-but-Inert

| Feature | What Works | What's Missing / Broken | Evidence |
|---|---|---|---|
| Undo/redo | `pushSnapshot()` is called after two-click edge create (`ThreeView.tsx:832`); `undo()`/`redo()` are bound to Cmd/Ctrl+Z/Shift+Z (`ThreeView.tsx:1267–1271`); `history.ts` has a real snapshot stack backed by the zustand store | `registerHistory()` is a documented no-op (kept for call-site compat, comment at `history.ts:8`); more critically, `pushSnapshot` is only called in the edge-create path — node drags, label edits, and other mutations do not push snapshots, so undo is incomplete | `tools/topology-vscode/src/webview/state/history.ts:27–29`; `ThreeView.tsx:18,832,1267–1271` |
| View-save on settle (camera/drag persistence) | `markViewSynced` is exported from `save.ts:78` and imported in `store.ts:13`; it IS called inside `loadView` (`store.ts:76`) to sync the initial loaded view | Camera drags, pan, and roll do not call `markViewSynced` after settling — only the load path calls it. Post-drag view-saves are therefore not persisted across reloads. (Inventory claim "never called" is stale; it is called, but only on load, not on user-driven camera changes.) | `tools/topology-vscode/src/webview/three/store.ts:76`; `save.ts:48,78` |
| Fit-view hotkey | Camera fits on load (loadEpoch effect) | No keyboard shortcut for manual re-fit; only automatic on load | `ThreeView.tsx:~1240` |
| Folds (collapsed subgraph) | Fold state module exists; `getFolds()` is passed into `specToFlow`; fold toggle is present in the viewer-state schema | No 3D mesh for folded state; the fold concept is wired into the adapter but has no visual representation in ThreeView | `tools/topology-vscode/src/webview/state/folds.ts`; `store.ts:62` |
| Z-coordinate (node depth) | `z` field exists in the node position schema and is parsed | Always rendered at z=0; no UI to set z; depth ignored in layout | `tools/topology-vscode/src/webview/schema/node-defs.ts`; specToFlow adapter |

---

## 4. Planned, Not Implemented

| Feature | Planned In | Notes |
|---|---|---|
| Multi-select (box/shift-click) | `3d-editor.md`, `rf-to-r3f-cutover.md` | Single-select only; no box-select or shift-accumulate |
| Node delete | `rf-to-r3f-cutover.md` (cutover checklist) | No delete key handler or UI affordance |
| Edge delete | `rf-to-r3f-cutover.md` | No edge removal path in ThreeView or store |
| Edge reconnect (drag endpoint) | `3d-editor.md` | Edge endpoints are fixed after creation |
| Node palette / add-node UI | `3d-editor.md`, `rf-to-r3f-cutover.md` | No palette; graph can only be loaded from file |
| Sublabel inline edit | `3d-editor.md` (InhibitRightGate sublabel field) | Sublabel rendered read-only; no click-to-edit |
| PseudoPanel in 3D | `3d-editor.md` (hasPseudo nodes: ChainInhibitor, Input, ReadGate) | PseudoPanel rendered in RF path only; no 3D equivalent |
| Port drag (wire from port handle) | `3d-editor.md` | Two-click edge-create only; no draggable port handles |
| Edge-kind context menu | `3d-editor.md` | No right-click context menu on edges |
| Edge midpoint drag | `project_edge_midpoint_offset_plumbing.md` memory; `rf-to-r3f-cutover.md` | `midpointOffset` schema and EdgeActionsCtx are wired; no drag affordance in ThreeView |
| Fold node Go primitive | `3d-editor.md`; substrate plan | No `nodes/fold/` package; fold is view-state only |

---

## 5. Deferred by Design (Post-v0, Friction-Driven)

Per `CLAUDE.md` posture: new work is friction-driven, not phase-driven. The following were noted in the cutover checklist or planning docs and explicitly parked until real-world use creates friction:

- Minimap / overview panel
- Rounded edge corners
- Connect-validation visual cue (highlight valid drop targets)
- Copy / paste nodes or subgraphs
- Edge display labels (show channel name on wire)
- Auto-routing (avoid node overlap)
- Auto-layout (force-directed or hierarchical)
- PNG / SVG export
- Hover tooltips on nodes and edges
- Properties inspector panel

---

## 6. Known Correctness Flaws

Cross-referenced from `docs/planning/visual-editor/audit-correctness.md` (H1/H2/H3). STEP 1 verification updated H1 and H2 findings against current code:

| ID | Severity | Claim | Verification result |
|---|---|---|---|
| H1 | HIGH | `markViewSynced` never called → view-saves permanently dropped | **Partially stale.** `markViewSynced` is called in `store.ts:76` inside `loadView`. However it is NOT called after user-driven camera moves/drags, so post-interaction view state still does not persist. The audit-correctness.md claim holds in spirit (interactive view-saves are dropped) but the "never called" wording is no longer accurate. |
| H2 | HIGH | `setSpecMeta` never called → top-level spec metadata stripped on save | **Fixed.** `setSpecMeta(spec)` is called in `store.ts:66` inside `loadSpec`. The audit-correctness.md entry is stale; this path is now wired. |
| H3 | HIGH | `loadView` before `loadSpec` silently drops the view (order-dependent) | **Unverified change.** `store.ts:loadView` now calls `specToFlow` only when `_lastSpec` is non-null, logging `store:view-load-noop` otherwise (`store.ts:84`). The ordering dependency still exists; arriving view-load before spec-load produces no nodes. |

See `audit-correctness.md` for full reproduction steps and original severity ratings.

---

## 7. Code with No Plan Doc

These features were built but do not appear in `3d-editor.md`, `rf-to-r3f-cutover.md`, or the session-log cutover checklist:

| Feature | Location |
|---|---|
| PanPad dwell-pan (hold-to-pan overlay) | `tools/topology-vscode/src/webview/three/PanPad.tsx` |
| Roll slider (camera roll axis control) | `tools/topology-vscode/src/webview/three/RollSlider.tsx` |
| Occlusion badge (node hidden-behind indicator) | Referenced in ThreeView; no plan doc entry |
| Nearest-N LOD (distance-based mesh culling) | ThreeView; not in cutover checklist |
