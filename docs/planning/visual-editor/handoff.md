---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-14 — NO TASK IN FLIGHT — on `main`)

Context: `main` is at `7a4ea597`. No task branch is active. Recent work is all merged and verified. New work is friction-driven (post-v0 posture) — pick from real editor use, no phase plan.

### Recently shipped to main (newest first)
- **3D camera navigation reference page** (merge 7a4ea597): added docs/planning/visual-editor/camera-navigation.html — a 5-tab visual reference (Controls / Camera model / Motions / Fit & persistence / Design) with 6 inline SVG diagrams documenting the editor's custom (non-OrbitControls) arcball camera: orbit = drag empty space (h->world Y, v->world X, pivot fixed at gesture start), pan = two-finger scroll, dolly = ctrl/pinch scroll (1.01^deltaY), Fit = Home button (bbox -> fov dist -> 1.2x pad -> square-on), roll reachable in math but NOT wired to input. Camera = PerspectiveCamera(fov50/near0.1/far20000), Camera3D{position,quaternion}; persistence via commitCamera -> scheduleViewSave(400ms) -> scene.json, restore via CameraRefBridge/CameraFitter. Source: tools/topology-vscode/src/webview/three/ (interaction-controls.ts, camera-ui.tsx, scene-content.tsx, ThreeView.tsx).
- **Lint cleanup** (merge 9d405243): dead `comparePortLists` removed, `min`/`max`/range-over-int modernizations; removed the branch-local `port-ring-anchors-spec.html` from main (tracking + disk).
- **Port ring-anchor layout** (merge 04704a57): node port PHYSICAL layout redesigned — replaced the directional `side`x`slot` grid (4 buckets) + free `anchor` vectors with ONE FLAT ARRAY of evenly-spaced ring anchors. Each port is a single `anchorId` index into the array. N = floor(2*pi*R/(d+p)), R = nodeRadius (node size per kind) so N varies by kind; d=8,p=2 (tunable consts in nodes/Wiring/port_geometry.go). Go owns the ring geometry + the drag-snap (snapToRingAnchorIndex); TS only unprojects the pointer (camera) to a world-space target and renders Go's stream. Editing a port reuses the `port-anchor` edit op (parity: messages.ts / handle-message.ts / stdin_reader.go). Verified working in editor (ports on ring, network runs, drag snaps). Key files: nodes/Wiring/port_geometry.go (ringAnchorCount/ringAnchorDir/nodeRadius/snapToRingAnchorIndex; portDir resolves ONLY via the ring), loader.go specPort {Name, AnchorId *int}, topology/nodes/*/{inputs,outputs}/*.json ({name, anchorId}), TS types.ts/parse-nodes-edges.ts/geometry-helpers.ts.
- **Remove ReadGate kind + inactive subtree** (merge e699cc17): deleted the unused ReadGate node kind (package, generated registries, fixtures), removed topology/inactive/, renamed Input's port ToReadGate -> ToChainInhibitor.
- **One-bead, node-drives-own-edges** (earlier merge 1a3bdb7f): network emits ONE bead per fire; each node drives its own outbound bead(s) to delivery on its OWN goroutine (no train, no seq, no per-bead walker). See nodes/Wiring/paced_wire.go (DriveBeadsToDelivery/advanceBeadLocked, EmitOneDriven, DriveAll). Memory: feedback_place_all_then_drive_concurrently.

### Active node kinds (current topology, nodes 1-5)
Input(1) -> ChainInhibitor(2,3) -> HoldFlip(4) -> WindowAndGate(5); 2->1 feedback ring. Edges: 1To2, 2FeedbackTo1, 2To3, 2To5, 3To4, 4To5. Node 3's ToNext1 output is a dangling fan-out port (no active edge) — left intentionally.

### Carry-forward (hard-won this session)
- GEOMETRY IS GO-OWNED (MODEL.md/CLAUDE.md): Go computes positions/rings/snaps + streams; TS only handles camera/input and renders Go's stream. TS computes no layout.
- SWARM CAUTION: a background Agent dispatch fanned out into ~20 duplicate agents, duplicated commits, and committed one unrequested change (reverted). Keep subagents FOREGROUND; spot-check git log before pushing.
- IDE DIAGNOSTICS WERE REPEATEDLY STALE this session (phantom broken imports / unused funcs that were actually wired). Verify against `go build` / grep, not the diagnostic panel.
- When a node has multiple outbound edges, PLACE all beads then DRIVE all together in one loop (never per-edge in series) — see memory feedback_place_all_then_drive_concurrently.

### Next step
None pending. Friction-driven: pick the next change from real editor use / observed friction. (Open idea, not committed: node 3's dangling ToNext1 port could be removed if a single-output ChainInhibitor is wanted.)

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored to the
state you're leaving the branch in, and commit on the active branch (main if no task is
in flight). Do not rely on chat history; the next AI may be a fresh model with no
transcript. The rendered handoff must itself contain this same ALWAYS clause so the loop
is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the structural
source of truth; update the template when an invariant changes.
