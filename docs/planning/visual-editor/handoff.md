# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-26, editor-r3f — audit merged, blank-diagram fixed)

**Active branch:** `editor-r3f` (long-lived R3F source line; NOT merged to main). This session: fixed the blank-diagram regression, ran a four-stream code audit on task/full-code-audit, fixed the actionable findings, and fast-forward-merged the audit back into editor-r3f. task/full-code-audit remains on origin (not deleted).

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

### Fixed this session (all committed + pushed, build green)

- **Blank diagram:** the R3F cutover dropped the webview→host "ready" post that the old RF App owned. The host waits for "ready" before sending load/view-load (handle-message.ts case "ready"), so the store was never fed. main.tsx now posts { type: "ready" } after the message listener registers. (Plus a tsc fix: computeOcclusionCounts now takes {w,h}.)
- **View-save + spec-metadata dead wiring (audit H1/H2):** markViewSynced and setSpecMeta were never called in the R3F path → view-saves permanently early-returned (drag positions dropped) and flowToSpec stripped passThrough/notes metadata on first save. Now called in store.loadView / store.loadSpec respectively.
- **Undo/redo repointed to the store:** rf/history.ts no longer writes to the dead rf-imperative; pushSnapshot reads useThreeStore, undo/redo call a new store.restoreNodesEdges action; Cmd/Ctrl+Z + Cmd/Ctrl+Shift+Z wired into ThreeView's existing keydown handler. rf-imperative.ts is now FULLY ORPHANED (zero importers).
- **createEdge silent rejections:** the three return-null guards (self-loop, unresolved handles, input-already-wired) now console.warn with reasons.
- **Audit tooling/docs:** audit-spec-view-hygiene.mjs repointed from nonexistent cmd/topogen/main.go to nodes/Wiring/loader.go (now runs); stale doc refs fixed (Wire.go→paced_wire.go in MODEL.md/CLAUDE.md, SubstrateEdge.tsx→SingleEdgeTube, handoff path depths, 2 memory files, ARCHITECTURE.md).

### Audit verdict (docs/planning/visual-editor/audit-*.md — branch-tagged)

- **Substrate: CLEAN.** No MODEL.md violations; go build/test pass; all five guard scripts green; pump.ts is render-only with its slot-phase write at the canonical home. Only the substance was verified healthy — every real finding was in the TS editor layer or docs.
- Three audit docs are on disk (substrate-integrity, correctness, hygiene). audit-r3f-residue.md was NOT written to disk (the residue findings live in the cutover-residue summary; regenerate if a standalone doc is wanted).

### Open items NOT yet done (need decision or sign-off)

- **Trace-event animations (residue #3 / DECISION NEEDED):** pump.ts / handleTraceEvent is never imported by the R3F path → run animations (fire flashes, pulse dots, held-value badges) are inert. This is feature-vs-cleanup: either WIRE handleTraceEvent into main.tsx's message handler AND build the ThreeView render layer for pulse/fire/held-value state (real feature work), OR DELETE pump.ts + its 4 state modules (fire-flash-state, slots-state, held-values, trace-kinds) as dead RF code. Not yet decided.
- **Safe deletions pending sign-off (destructive):** rf-imperative.ts (now zero importers), rf/nodes/node-defs.ts + rf/nodes/registry.ts (zero importers — but CLAUDE.md's substrate landing rule still NAMES registry.ts as THE node-kind registry; deleting it means updating that rule to the R3F reality first). pump.ts cluster depends on the #3 decision.
- **reactflow dep removal (sign-off + prep):** all 7 reactflow imports are TYPE-only (RFNode/RFEdge) + one dead CSS import in main.tsx. Removable once those type shapes are re-homed into our own types file and the CSS import dropped. Needs user sign-off (dependency removal).
- **No transient-message UI surface:** createEdge rejections are console-only; run-status is reserved for sim state. A small editorMessage store field + auto-clear is the missing piece if UI feedback is wanted.
- **LIVE VERIFICATION STILL PENDING:** reload and confirm — diagram renders (ready-handshake fix), drag persists across reload (H1 fix — the handoff's prior "confirmed persisting" predates today's fixes and is suspect), undo (Cmd+Z) reflects in R3F, connect A→B. Triage in one pass.

### Deferred-until-friction (do NOT speculatively patch)

- z still 0 for all nodes (z-derivation deferred). The 3 in-code low-sev items: pick-by-position scene.traverse fragility, roll-slider zero drift, CameraSettleDetector per-frame string snapshot. parseCamera is 2D-only (will need a 3D branch WHEN 3D camera persistence is built — not before).

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
- `tools/topology-vscode/src/webview/main.tsx` — renders only ThreeView; feeds store on load; hoisted run/save toolbar; posts { type: "ready" } to unblock host load sequence.
- `tools/topology-vscode/src/webview/save.ts`, `tools/topology-vscode/src/webview/rf/pump.ts` — read from the store now. pump.ts is INERT in the R3F path (handleTraceEvent not wired; see open items).
- `tools/topology-vscode/src/webview/rf/rf-imperative.ts` — FULLY ORPHANED (zero importers); pending deletion sign-off.
- `tools/topology-vscode/src/webview/rf/adapter/{spec-to-flow,flow-to-spec}.ts`, `tools/topology-vscode/src/webview/state/viewer/*` — pure adapters/state, shared and RF-free.
- `docs/planning/visual-editor/rf-to-r3f-cutover.md` — note it is now partially superseded (toggle/staged-removal framing is gone; the cut happened).

Pseudo files below are for the **deferred** `task/inhibitright-pseudo` branch only, not this one:

- `tools/pseudo/chaininhibitor.go`, `tools/pseudo/readgate.go` — pseudo pattern references
- `cmd/pseudo/main.go` — pseudo subcommand dispatch
- `nodes/inhibitrightgate/{node.go,SPEC.md}` — target to regenerate / mark `hasPseudo:true`
- `tools/topology-vscode/src/extension/handle-message.ts` — handleChainInhibitor{Render,Save} + pseudoTable
- `tools/topology-vscode/src/webview/rf/PseudoPanel.tsx` — double-click-to-edit panel (deleted in Slice 3; must be re-created for this task)

### Substrate model contract (stable)

See [MODEL.md](../../../MODEL.md#slot-phase-lifecycle). Unchanged by the
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
