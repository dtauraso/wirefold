---
branch: task/editor-3d-r3f-canvas
---

# RF → R3F Cutover: Parity-Gap Audit

Grep-first enumeration of every graph-authoring operation the 2D React Flow editor
supports vs. 3D ThreeView coverage. Defines what must be built before React Flow can be deleted.

**2D editor:** `src/webview/rf/` · **3D view:** `src/webview/three/ThreeView.tsx` · **state bridge:** `src/webview/rf/rf-imperative.ts`

## A. Operation coverage

| # | Operation | 2D impl | 3D status | Note |
|---|-----------|---------|-----------|------|
| 1 | Add node (palette drag-drop) | `_use-drag-drop.ts:19` | MISSING | no palette/drop target/creation in 3D |
| 2 | Delete node(s) (Del key) | `app.tsx:91` + `_handle-delete.ts:31` | MISSING | no delete gesture |
| 3 | Delete edge(s) (Del key) | `_handle-delete.ts:50` | MISSING | no edge select+delete |
| 4 | Duplicate/copy-paste | not implemented | N/A | not in 2D either |
| 5 | Move node (drag) | `_on-node-drag.ts:47` | MISSING | arcball drag moves camera, not nodes |
| 6 | Multi-select + move | `app.tsx:83-84` + RF box select | MISSING | 3D is single-select only |
| 7 | Alignment guides | `app/AlignGuides.tsx:8` | MISSING | no node drag |
| 8 | Node label display | `GenericNode.tsx:218` | PARTIAL | 3D hover/selected/nearest-N LOD |
| 9 | Sublabel inline edit (dbl-click) | `_on-node-context.ts:31` | MISSING | labels non-interactive in 3D |
| 10 | PseudoPanel edit (dbl-click) | `PseudoPanel.tsx:111` | MISSING | no PseudoPanel in 3D |
| 11 | Validation error display | `GenericNode.tsx:212` + 3D flag colors | COVERED | flag fill/ring + label bg |
| 12 | Single node select (click) | RF built-in / `_reconcile-selection.ts` | COVERED | raycast pick |
| 13 | Multi-select (box/shift) | RF SelectionMode | MISSING | — |
| 14 | Selection-driven fold | `_on-node-context.ts:65` | MISSING | no fold in 3D |
| 15 | Node context menu | `_on-node-context.ts:73` | MISSING | — |
| 16 | Fold collapse/expand | `_on-node-context.ts:17`/`:77` | MISSING | folds absent in 3D |
| 17 | Fold creation from selection | `_on-node-context.ts:35` | MISSING | — |
| 18 | Create edge | `_on-connect.ts:19` | PARTIAL | 3D two-click `rfCreateEdge`, first port only; no grow-handle; disambiguation deferred |
| 19 | Reconnect edge | `_on-reconnect.ts:8` | MISSING | — |
| 20 | Delete edge | `_use-edge-handlers.ts:50` | MISSING | — |
| 21 | Edge context menu (set kind) | `EdgeContextMenu.tsx:5` | MISSING | — |
| 22 | Edge kind change | `_use-edge-handlers.ts:52` | MISSING | — |
| 23 | Edge midpoint offset drag | `MidpointDragHandle.tsx:27` | MISSING | 3D edges are bezier tubes, no handle |
| 24 | Port position drag | `GenericNode.tsx:168` + `PortRim.tsx:66` | MISSING | spheres have no handles |
| 25 | Grow-handle (new input port on drop) | `PortRim.tsx` + `_on-connect.ts:62` | MISSING | — |
| 26 | Undo (Cmd-Z) | `app.tsx:50` → `rfUndo()` | MISSING | no hotkey in 3D |
| 27 | Redo | `app.tsx:51` | MISSING | — |
| 28 | Save (debounced) | `save.ts:36` | COVERED | 3D mutations → `scheduleSave` |
| 29 | View save (camera/positions) | `save.ts:48` | MISSING | 3D never writes viewerState |
| 30 | Load graph | `_handle-load.ts` | COVERED | 3D reacts via `subscribeRFState` |
| 31 | Fit-view all (f) | `_use-fit-view.ts:9` | MISSING | 3D fits once on mount, no hotkey |
| 32 | Fit-view selection (Shift-F) | `_use-fit-view.ts:14` | MISSING | — |
| 33 | Run/pause/stop | `RunButton.tsx:22-35` | COVERED | hoisted to Root, both views |
| 34 | Manual take | `ManualTakeButton.tsx` | COVERED | portal pattern |
| 35 | Pan canvas | RF built-in | PARTIAL | 3D dwell→PanPad |
| 36 | Zoom/dolly | RF built-in | PARTIAL | 3D scroll dolly + buttons |
| 37 | Pulse animation | `SubstrateEdge.tsx:66` + `pump.ts` | COVERED | 3D PulseBead reads `getPulseMap()` |

## B. RF-removal checklist (build before cutover, dependency-ordered)

1. **Node-space coordinate model** — a "world-edit" plane in 3D where nodes are draggable. Unblocks everything spatial.
2. **Node drag (move)** — drag selected node on the plane → `rfSetNodes` + `scheduleViewSave`. **The single biggest primitive; nearly everything builds on it.**
3. **Multi-select** — box-select on empty space; must differentiate from dwell-pan.
4. **Node delete** — Del key → `rfSetNodes` filter + `scheduleSave`.
5. **Edge delete** — raycast-select edge + Del → `rfSetEdges` filter + `scheduleSave`.
6. **Edge reconnect** — drag existing endpoint (replicate `onReconnectImpl`).
7. **Node palette + add** — 3D-native palette/radial, place node → `rfSetNodes` + `pushSnapshot` + saves.
8. **Sublabel inline edit** — dbl-click overlay → input; `inline-edit.ts` is already RF-free.
9. **PseudoPanel** — float overlay on dbl-click of Input/Transformer; host round-trip unchanged.
10. **Port-position drag** — `setPortPosition` already RF-agnostic but for `rf.setNodes`→`rfSetNodes`.
11. **Edge kind change** — 3D hover popover; `setEdgeKind` already RF-agnostic.
12. **Edge midpoint drag** — `setEdgeMidpointOffset` already RF-agnostic.
13. **Fold create/collapse/expand** — `folds-state.ts`/`createFold` RF-agnostic; 3D must render fold placeholders.
14. **Grow-handle** — synth new port in connect flow (TODO in `rfCreateEdge`).
15. **Undo/redo hotkeys** — keydown → `rfUndo()`/`rfRedo()` (in `history.ts`).
16. **Fit-view hotkeys** (f / Shift-F) — retriggerable, selection-aware camera fit.
17. **View-save on camera settle** — `CameraSettleDetector` fires but never writes; add serialize-camera → `patchViewerState` + `scheduleViewSave()`.

## C. Mechanical deletion list (at cutover)

**Delete (RF components):** `rf/app.tsx`, `rf/app/AppView.tsx`, `AlignGuides.tsx`, `_app-view-props.ts`, `_ctx.ts`, `_decorate.ts`, `_handle-delete.ts`, `_on-connect.ts`, `_on-node-context.ts`, `_on-node-drag.ts`, `_on-reconnect.ts`, `_use-fit-view.ts`, `camera.ts`, `FoldNode.tsx`, `NoteNode.tsx`, `nodes/GenericNode.tsx`, `panels/NodePalette.tsx`, `PortRim.tsx`, `port-rim-grow.tsx`, `edges/SubstrateEdge.tsx`, `edges/MidpointDragHandle.tsx`, `_use-undo-redo.ts` (stub), plus the 2D/3D toggle.

**Partially port (logic RF-agnostic, hooks dropped):** `_use-edge-handlers.ts` (`setEdgeKind`/`setEdgeMidpointOffset`/`setPortPosition` survive).

**Rewrite:** `rf/history.ts` — snapshot from `rfGetNodes()`/`rfGetEdges()` instead of `rf.toObject()`; drop `ReactFlowInstance`.

**Keep (RF-free survivors — ~60% of logic):** `rf-imperative.ts` (the key survivor — rename, drop reactflow type imports), `adapter/flow-to-spec.ts`, `adapter/spec-to-flow.ts`, `spec-to-flow-helpers.ts`, `save.ts`, `viewer-state.ts`, `inline-edit.ts`, `folds-state.ts`. Also drop `react-hotkeys-hook` if unused after.

## D. Assessment

3D covers ~6/37 authoring ops fully (select, save, load, run, pulse, validation display). Camera nav works but differs. All create/edit/delete/undo is MISSING.

**Single biggest build item: node drag (move).** The whole spatial-edit model — drag, undo snapshots, fold, align, multi-select move — collapses into the absence of "move a node on the 3D plane." The 3D view is currently a read-only viewport into RF state, not an editable canvas. Until nodes can be repositioned in ThreeView, cutover is blocked.

**Good news:** `rf-imperative.ts` is already a clean non-RF module store; adapters are pure data; `save.ts`/`viewer-state.ts` are RF-free. ~60% of business logic survives deletion intact — the work is building 3D interaction surfaces that call already-correct logic.
