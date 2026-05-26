# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-26, editor-r3f — R3F cutover complete)

**Active branch:** `editor-r3f` (renamed from `task/editor-3d-r3f-canvas`; old name deleted from origin). This is a **long-lived R3F source line** — a deliberate, conscious override of CLAUDE.md's "avoid long-lived feature branches" hygiene rule. R3F is a whole new editor, not a task. Inheritor branches track this line; RF leaves `main` only when this branch merges and supersedes it. NOT merged to main.

### Why 3D

Not cosmetics. The topology genuinely has depth — the wire structures
(inhibitor chain, rings, lateral-inhibition lattices) have real
geometry that the current 2D React Flow canvas flattens into
misleading edge crossings and false adjacencies. The move is about
**representational honesty**: make the rendered structure match the
actual structure.

### Direction (locked)

**ONE 3D view, ONE store.** R3F is THE editor. RF is being retired, not maintained as a peer or fallback. RF removal from `main` = the merge event, not an in-branch deletion debate. This branch carries only R3F things.

Drift guard still applies: interaction CONTROL is substance (no OrbitControls); rendering is medium (R3F yes). zustand is a medium choice (already a dep) — fine to adopt as the store.

### Governing principle (most important — drift guard)

**Interaction CONTROL is substance, not medium.** This is a
**classification clause** of CLAUDE.md's medium-vs-substance rule, NOT
a competing rule. One rulebook, correctly applied.

Decision procedure:

1. Is this rendering/plumbing, or control over the system?
2. Rendering → industry default (react-three-fiber: yes).
3. Control → substance → design from need → apply the
   **recoverable-by-device test**: if a better input device does NOT
   restore a lost capability without changing the design, the loss is
   baked into the design → wrong industry pattern-match → REJECT.

`drei`'s `OrbitControls` FAILS this test (a SpaceMouse still leaves a
fixed pivot and locked roll — the loss is in the design) and must
**NOT** be adopted. Adopting R3F (medium, yes) does not imply adopting
OrbitControls (substance, no).

This principle is also saved in
[`memory/project_interaction_control_is_substance.md`](../../../memory/project_interaction_control_is_substance.md)
(rides to main on merge). Full design lives in
[3d-editor.md](3d-editor.md) (branch-local — does **not** ride the
merge).

### Cutover completed this session (3 slices, build green each)

- **Slice 1:** new zustand store `src/webview/three/store.ts` (`useThreeStore`) holds nodes/edges/selection; `loadSpec`/`loadView` via the pure adapters (`specToFlow`/`parseViewerState`); ThreeView reads/writes it. `main.tsx` feeds it on load/view-load. Replaced ThreeView's `rf-imperative` borrow.
- **Slice 2:** ThreeView is the ONLY mounted view — 2D/3D toggle + React Flow 2D App removed from `main.tsx`. Run/pause/stop + save-status toolbar still works (reads imperative getters).
- **Slice 3:** store is the SOLE source of truth — repointed `save.ts` (`performSave`), `pump.ts`, `RunButton`, `inline-edit.ts`, and the `__wirefold_test` hook to `useThreeStore.getState()`; removed the transitional rf-imperative mirror. Deleted the dead 2D RF component tree: `rf/app.tsx`, all `rf/app/*` (26 files), RF node/edge components (`GenericNode`, `PortRim`, `port-rim-grow`, `SubstrateEdge`, `FoldNode`, `NoteNode`, `MarkerDefs`, etc.), panels (`NodePalette`, `PseudoPanel`, etc.) — each proven import-dead.

### Remaining RF residue (not yet removed)

- `rf/rf-imperative.ts` KEPT — only `rf/history.ts` still uses it (undo/redo).
- `reactflow` dependency still in `package.json`: live code has only TYPE-only imports (`RFNode`/`RFEdge` in `store.ts`, `rf/types.ts`, `rf/adapter/*`, `rf/history.ts`) + one CSS import `reactflow/dist/style.css` in `main.tsx`. No RF component or hook is instantiated anywhere. Removing the dep needs BOTH: (a) user sign-off [CLAUDE.md dependency-removal rule], and (b) re-homing the `RFNode`/`RFEdge` type shapes into our own types first.

### Known gaps / verification pending

- **LIVE VERIFICATION PENDING for the whole cutover** — user to reload and check: load+render (R3F sole view), node drag + persist-across-reload (drag CONFIRMED working + persisting earlier this session), **connect A→B (the never-confirmed-live bug; store fix in place but not yet visually confirmed)**, run/pause/stop + save-status. Triage findings in one pass.
- **Undo/redo wired-but-inert:** `rf/history.ts` restore path writes to `rf-imperative`, which nothing live reads anymore → undo won't reflect in R3F. Was already a missing 3D op (not a regression); must be repointed to the store in a follow-up slice.
- **z still 0 for all nodes** — z-derivation deferred until friction. Node-position persistence now works through the store + node drag.
- **3 in-code TODOs remain** (pick-by-position `scene.traverse` fragility, roll-slider zero drift, CameraSettleDetector per-frame string snapshot).

### Next concrete step: edge BEND/route (view-only midpoint handle)

User-requested. Drag a midpoint handle to pull a 3D edge's bezier tube through space (route around occluders). VIEW-ONLY — no topology change. Design (record fully):

- **Persist** to a NEW field on `EdgeView` in `src/webview/state/viewer/types.ts` + its parser `parse.ts` — e.g. `bend?: { x: number; y: number; z: number }`. **Do NOT use the substrate `midpointOffset` WireProp** (that's spec-level / topology; this is view-only and must stay in `topology.view.json`).
- **Render:** `SingleEdgeTube` (`ThreeView.tsx` ~L295–327) currently auto-computes control point `_p1 = mid + lift*(span*0.25)` where `lift = (0,0,1)×edgeDir`. Add the stored bend: `_p1 = mid + autoLift + bend`. Render a small DRAGGABLE handle sphere at the curve midpoint, shown on hover/selected edge (edge hover/selection state does not exist yet — add minimal hover).
- **Interaction:** mirror the just-added `nodeDragRef` pattern with `edgeHandleDragRef { edgeId, planePointAtStart, midAtStart, snapshotPushed }`. Handles must be pickable (extend the pickRequest raycast to recognize handle meshes via `userData`). **Drag on a CAMERA-FACING plane through the handle (NOT the z=0 plane node-drag uses)** so the curve can be pulled out-of-plane in true 3D. `newMid = planePoint + (midAtStart − planePointAtStart)`; `bend = newMid − autoMid`. Persist on pointer-up: `patchViewerState(v => v.edges[id].bend = …)` + `scheduleViewSave()`; `pushSnapshot()` at drag start. Reuse/generalize `unprojectToPlane` to accept an arbitrary plane.

### Separate deferred task (paused — NOT this branch)

Branch `task/inhibitright-pseudo` exists on origin:
**InhibitRightGate pseudo-text projection** (same
Input/ReadGate/ChainInhibitor pattern). Params L/R; semantic "L pass /
R inhibit" → result = `Left==1 && Right==0`. Steps: `cmd/pseudo`
subcommand (render/save) + `nodes/inhibitrightgate/SPEC.md`
`hasPseudo:true` + `handle-message.ts` handler + Go template regen of
`node.go`. **Watch:** apply the ChainInhibitor OutMulti handle-matching
lesson (suffix-strip ToNext0/ToNext1 → base ToNext) if InhibitRightGate
has multiple outputs. Paused while 3D work is in flight.

### Key files

- `tools/topology-vscode/src/webview/three/ThreeView.tsx` — the whole (sole) 3D view: node drag, edge tubes, pointer state machine.
- `tools/topology-vscode/src/webview/three/store.ts` — the single zustand source of truth (nodes/edges/selection, load/save actions).
- `tools/topology-vscode/src/webview/main.tsx` — renders only ThreeView; feeds store on load; hoisted run/save toolbar.
- `tools/topology-vscode/src/webview/save.ts`, `tools/topology-vscode/src/webview/rf/pump.ts` — read from the store now.
- `tools/topology-vscode/src/webview/rf/rf-imperative.ts` — LEGACY, only `history.ts` uses it (undo/redo); to be retired.
- `tools/topology-vscode/src/webview/rf/adapter/{spec-to-flow,flow-to-spec}.ts`, `tools/topology-vscode/src/webview/state/viewer/*` — pure adapters/state, shared and RF-free.
- `docs/planning/visual-editor/rf-to-r3f-cutover.md` — note it is now partially superseded (toggle/staged-removal framing is gone; the cut happened).

Pseudo files below are for the **deferred** `task/inhibitright-pseudo` branch only, not this one:

- `tools/pseudo/chaininhibitor.go`, `tools/pseudo/readgate.go` — pseudo pattern references
- `cmd/pseudo/main.go` — pseudo subcommand dispatch
- `nodes/inhibitrightgate/{node.go,SPEC.md}` — target to regenerate / mark `hasPseudo:true`
- `tools/topology-vscode/src/handle-message.ts` — handleChainInhibitor{Render,Save} + pseudoTable
- `tools/topology-vscode/src/webview/rf/PseudoPanel.tsx` — double-click-to-edit panel

### Substrate model contract (stable)

See [MODEL.md](../../MODEL.md#slot-phase-lifecycle). Unchanged by the
3D move — going 3D is a medium change; the Go substrate,
slot-phase/AND-gate/backpressure model, and `pump.ts` firing logic stay
untouched.

## Dev-loop

After TS edit: `npm run build` from `tools/topology-vscode/`.
After Go change: `go build ./...` from repo root, `go test ./nodes/Wiring/...`.
After pseudo change (deferred branch): `go test ./tools/pseudo/...`.
To repro / inspect: clear `.probe/*.jsonl`, reload window in VS Code, Run once, inspect logs.

Check: `go test ./...`. All five guard scripts — the four boundary guards plus
`check-substrate-vocabulary` — run automatically via the Stop hook (`scripts/stop-checks.sh`).

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt
tailored to the state you're leaving the branch in, and commit on the
active branch (main if no task is in flight). Do not rely on chat
history; the next AI may be a fresh model with no transcript. The
rendered handoff must itself contain this same ALWAYS clause so the
loop is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as
the structural source of truth; update the template when an invariant
changes.
