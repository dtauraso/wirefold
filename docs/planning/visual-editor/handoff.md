---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-14 — branch `task/node-lattice` IN FLIGHT — design complete, implementing)

Context: `main` is at `f1633bf7`. Branch `task/node-lattice` (off main) makes node placement a discrete 3D LATTICE — the node-scale twin of the port ring-anchor work. Spec DONE (all questions resolved); IMPLEMENTATION starting.

### The design (FINAL — see docs/planning/visual-editor/node-lattice-spec.html)
- A node's position is an INTEGER lattice cell (i,j,k) in a FINITE BOX, replacing free continuous x/y. worldPos = origin + (i,j,k)*spacing, clamped to the box.
- Go OWNS the lattice->world mapping and STREAMS node positions (geometry Go-owned, like it already streams node centers).
- DRAG snaps to a cell: TS unprojects the 2D drag onto the VIEW-PERPENDICULAR plane through the node (camera math = TS) -> world target; GO snaps to the nearest lattice cell (round per axis, clamped to box), writes (i,j,k), re-streams. The two editable dimensions are whichever face the camera; ORBIT changes them; depth is held with no special gesture. Reuse the existing node-move edit op (payload = world target, not a coord; Go snaps). Total op, no z=0 singularity.
- spacing = tunable Go constant (~2-3x node size, by eye); box dimensions = Go-owned param (sized to hold the graph + margin).
- Migration: existing free positions -> nearest cell (round, clamped). Snap-MODEL only (lattice invisible; visible reference deferred). Storage: node json stores (i,j,k) replacing free x/y, schema+parser parity (mirrors the port anchorId migration).
- Parallel: ports replaced free `anchor` with `anchorId` index into a ring; nodes replace free x/y with (i,j,k) into a lattice. Same philosophy (discrete, Go-owned mapping, snap is total).

### Implementation plan (follows the port-rollout shape)
1. Go lattice geometry: node world pos resolves from (i,j,k) via origin+coord*spacing clamped to box; node spec gains (i,j,k). Additive/non-breaking first (keep free x/y path).
2. Migrate topology node positions free x/y -> nearest (i,j,k).
3. TS schema/parser: node position accepts (i,j,k) (parity).
4. Drag-snap interaction: node drag -> view-plane target -> Go snaps to nearest cell -> writes (i,j,k) -> re-stream.
5. Cleanup: remove the free x/y path.
Then verify (go build + go test -race + tsc + vitest + editor eyeball).

### Next step
Step 1 (Go lattice geometry), additive: node world pos resolves from (i,j,k) via origin+coord*spacing clamped to box; node spec gains (i,j,k). Keep the free x/y path in place (non-breaking).

### Recently shipped to main (newest first)
- **Edge direction arrowheads** (merge 376757c0): each edge renders a cone arrowhead at its TARGET end (apex on the target IN-port, oriented along (end-start)), showing source->target flow direction. Matches the tube's SHADING_PARAM_TUBE_COLOR/emissive and fade (faded edges' arrows fade too); raycast disabled (decorative). Derived from the Go-streamed edge segment (useEdgeGeometryStore), so it follows on node-move. Consts ARROW_HEIGHT=6, ARROW_RADIUS=3 (tube radius is 1.5) — tunable. File: tools/topology-vscode/src/webview/three/scene-content.tsx (inside SingleEdgeTube).
- **Persistent-target camera navigation** (merge 6b3094ed): replaced per-gesture z=0-plane raycast anchoring with a camera-owned PERSISTENT TARGET point, so orbit/pan/dolly are TOTAL operations of (camera pose, target) — the edge-on/grazing singularity is structurally unreachable (it was the ray-vs-z=0 partial function diverging when the view went parallel to the ground). Orbit pivots around the target (the selection if one is selected, else a seeded scene-depth point); pan slides camera+target together on the view plane (scale = distance to target, fixes the earlier zoom-leak/swing/reversed-direction); dolly zooms toward the target; Fit sets target = scene center. Free 6-DOF arcball rotation unchanged (no clamp, no turntable). The target is RUNTIME-ONLY (re-seeded on load by projecting scene center onto the camera forward ray) — NOT persisted to scene.json yet. unprojectToPlane is retained ONLY for node-drag / port-drag / click-pick (placing things on the z=0 layout), not for camera gestures. Files: tools/topology-vscode/src/webview/three/interaction-controls.ts, ThreeView.tsx, camera-ui.tsx. Doc updated: docs/planning/visual-editor/camera-navigation.html. PRINCIPLE (carry-forward): camera gestures must be TOTAL functions of camera state; a singularity means the REPRESENTATION is wrong — fix it by anchoring on always-finite camera-owned state, NOT by clamping inputs (6-DOF is substance) or adding fallback band-aids (which only mask the partial function). Industry carries a target too (OrbitControls/Blender/Maya/CAD); adopt the target, reject their rotation constraints.
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

### Carry-forward ideas (not blocking node-lattice)
- Node 3's dangling ToNext1 port could be removed if a single-output ChainInhibitor is wanted.
- Camera target is runtime-only — could be persisted to scene.json (extend Camera3D with a target, update serialize/parse + CameraRefBridge restore) so orbit pivot/zoom survive reload exactly; deferred — re-seeding from scene center is fine for now.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored to the
state you're leaving the branch in, and commit on the active branch (main if no task is
in flight). Do not rely on chat history; the next AI may be a fresh model with no
transcript. The rendered handoff must itself contain this same ALWAYS clause so the loop
is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the structural
source of truth; update the template when an invariant changes.
