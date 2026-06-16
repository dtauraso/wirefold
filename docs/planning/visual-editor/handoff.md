---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-16 — branch task/spherical-layout — POLAR coordinate model IMPLEMENTED, awaiting live-editor verify, NOT merged)

Context: `main` is at `c123b83e`. Full design: docs/planning/visual-editor/polar-coordinate-model.md.

### Polar coordinate model — replaces the Cartesian non-rooted layout
The diagram sits in a large container sphere (center = polar origin = center of the
axis-aligned PRISM built from the node points). Every node's authoritative position is
ONE outer polar coordinate (r,θ,φ) from the origin; ALL nodes are roots (flat, no
parent chain — kills the 8↔1 feedback cycle). Each node is also a sphere center; its
sphere R and surface coords are DERIVE-ON-READ measurements over the roots (soft
membership — R grows around its nodes, never pins them; a move touches exactly one
root, no cascade, no solver). Pole = +y. Cartesian appears only at three boundaries:
camera, render, and saved files (world Cartesian in the prism frame); never on a node
at runtime.

Go (nodes/Wiring): polar.go (conversions), prism.go (prism/rootSet/buildRoots),
derived.go (sphereR / surfaceCoord / ring normals), node_move.go RootMove (root =
authority, soft membership; replaced SphereDrag), lock.go chordLock (follower =
mirror_φ(leader); the perpendicular-chord lock as a one-line polar invariant).
Bridge: Go emits render-ready Cartesian + sphereR + two ring normals (vrx..frz) on
node-geometry; pump = pure plotter. Render: SphereRing orients tori from the emitted
normals. Camera: pan=2D-Cartesian, zoom=Cartesian, rotate=polar-orbit, camera
constrained inside the large sphere (interaction-controls.ts).

### Verified vs NOT verified
- VERIFIED: go build ./... + go test ./... green (10 pkgs); tsc --noEmit clean;
  vitest 71/71; npm build clean; all 8 guard scripts pass (incl. ts-computes-no-geometry,
  no-await-on-bridge). Unit tests cover conversions, prism/roots round-trip, sphereR,
  surface coords, ring normals, RootMove (one-root/soft-membership), chord lock.
- NOT VERIFIED (needs user eyeball, why it's unmerged): live editor — spheres/tori/nodes
  render from the Go stream (P6.4), camera feel (P7.7), chord-lock drag live (P8.4),
  8-node load preserves positions in the editor (P2.6).

### Follow-ups / known limitations
- nodeGeom.Center is KEPT as the derived render/geometry coordinate (port/edge arc math
  still reads it); it is updated from roots on move. Full removal would mean rederiving
  all port/edge geometry from roots — a larger follow-up, intentionally out of scope.
- Prism is recomputed deterministically from points at load (not separately persisted);
  on-disk format stays world Cartesian, so the 8-node topology needs no file migration.
- Chord lock is registered in code (lock.go addChordLock); no editor UI to create locks yet.

### Next step
User verifies in the editor (P6.4, P7.7, P8.4, P2.6). Then merge task/spherical-layout
(run tools/strip-branch-local-docs.sh first — the spec + this section ride the branch).

### Recently shipped to main (newest first)
- **Node pick respects occlusion** (task/pick-occlusion): selecting/hovering a node now returns the FRONTMOST node body the cursor ray actually hits (true nearest-hit occlusion). Root cause: the node body sphere mesh had no nodeId tag, so a body hit was mapped to a node by scanning node positions for matching x,y ONLY (ignoring z) — so a node directly BEHIND another (same screen x,y, deeper z, e.g. node 7 behind node 3 after a depth drag) resolved to the front node. Fix: tag each body sphere with userData.nodeId and resolve the hit directly (z-aware); same path serves hover-highlight and click. File: tools/topology-vscode/src/webview/three/scene-content.tsx.
- **Excitatory node kind + nodes 6,7** (merge 2e2154f0): new `Excitatory` node kind (nodes/excitatory/) — a sample-and-hold/excitatory-neuron: holds an int (starts -1), continuously PULSES its held value downstream, and UPDATES the held value when an input arrives. It DRIVES its output on its OWN goroutine (spawned in Update) while the main loop blocks on input — so the held value + its interior bead (EmitHeldBead, the general reflectBuild-injected single-bead path ChainInhibitor/HoldFlip use) update INSTANTLY on input, no drive-lag. Sized 90x60 like ChainInhibitor, distinct blue (#e1f5fe/#01579b). Wiring: node 6 Excitatory on path 1->6->5 (node 1 Input now FANS OUT to node 2 AND node 6 on SEPARATE goroutines node 1 initiates — concurrent, same value; required a second Input output port ToExcitatory since one Out port binds only one edge; 6->5.FromLeft; 2->5 dropped). Node 7 Excitatory on path 2->7->4 (2.ToNext1->7; 7->4.In; 3->4 dropped, node 3 output now dangles). Files: nodes/excitatory/{node.go,SPEC.md}, nodes/input/node.go, regenerated registries, topology/nodes/{6,7}/** + edges.
- **Camera restores on reload** (same merge): the extension host now reads topology/view/scene.json and sends `sceneText` on the `load` post (extension.ts), so viewerState.camera3d is populated synchronously and the saved camera pose restores instead of auto-fitting to home every reload. Root cause was the host never delivering the scene sidecar to the webview (both load posts omitted sceneText).
- **Node lattice placement** (merge da2564ec): node positions are discrete integer lattice cells (i,j,k) instead of free x/y — the node-scale twin of the port ring-anchor work. Go owns the cell->world mapping (worldPos = origin + cell*spacing) and streams positions; SPACING and BOX are DERIVED from the initial layout (min pairwise node distance / extent), NOT a hand-picked constant, so the graph keeps its shape (a magic 330 spacing had collapsed the graph into one cell and stacked nodes in depth -> occlusion; deriving it fixed that). Dragging snaps a node to a cell via zoom-INDEPENDENT pixel->cell steps (PX_PER_CELL=80); the two editable axes are chosen by camera orientation (orbit to expose depth). Migration snapped existing positions to nearest cell; node cell stored in topology/nodes/<id>/meta.json, schema+parser parity. Files: nodes/Wiring/lattice.go (latticeToWorld/worldToLattice), loader/loader_tree/node_move/stdin_reader/port_geometry, TS geometry-helpers.ts + parse-nodes-edges.ts + types. Also resolved a camera regression chain the lattice exposed: zoom focus now follows the node nearest SCREEN CENTER (was a fixed-depth target that, once nodes had depth, made zoom drift/stall on the wrong node).
- **Edge direction arrowheads** (merge 376757c0): each edge renders a cone arrowhead at its TARGET end (apex on the target IN-port, oriented along (end-start)), showing source->target flow direction. Matches the tube's SHADING_PARAM_TUBE_COLOR/emissive and fade (faded edges' arrows fade too); raycast disabled (decorative). Derived from the Go-streamed edge segment (useEdgeGeometryStore), so it follows on node-move. Consts ARROW_HEIGHT=6, ARROW_RADIUS=3 (tube radius is 1.5) — tunable. File: tools/topology-vscode/src/webview/three/scene-content.tsx (inside SingleEdgeTube).
- **Persistent-target camera navigation** (merge 6b3094ed): replaced per-gesture z=0-plane raycast anchoring with a camera-owned PERSISTENT TARGET point, so orbit/pan/dolly are TOTAL operations of (camera pose, target) — the edge-on/grazing singularity is structurally unreachable (it was the ray-vs-z=0 partial function diverging when the view went parallel to the ground). Orbit pivots around the target (the selection if one is selected, else a seeded scene-depth point); pan slides camera+target together on the view plane (scale = distance to target, fixes the earlier zoom-leak/swing/reversed-direction); dolly zooms toward the target; Fit sets target = scene center. Free 6-DOF arcball rotation unchanged (no clamp, no turntable). The target is RUNTIME-ONLY (re-seeded on load by projecting scene center onto the camera forward ray) — NOT persisted to scene.json yet. unprojectToPlane is retained ONLY for node-drag / port-drag / click-pick (placing things on the z=0 layout), not for camera gestures. Files: tools/topology-vscode/src/webview/three/interaction-controls.ts, ThreeView.tsx, camera-ui.tsx. Doc updated: docs/planning/visual-editor/camera-navigation.html. PRINCIPLE (carry-forward): camera gestures must be TOTAL functions of camera state; a singularity means the REPRESENTATION is wrong — fix it by anchoring on always-finite camera-owned state, NOT by clamping inputs (6-DOF is substance) or adding fallback band-aids (which only mask the partial function). Industry carries a target too (OrbitControls/Blender/Maya/CAD); adopt the target, reject their rotation constraints.
- **3D camera navigation reference page** (merge 7a4ea597): added docs/planning/visual-editor/camera-navigation.html — a 5-tab visual reference (Controls / Camera model / Motions / Fit & persistence / Design) with 6 inline SVG diagrams documenting the editor's custom (non-OrbitControls) arcball camera: orbit = drag empty space (h->world Y, v->world X, pivot fixed at gesture start), pan = two-finger scroll, dolly = ctrl/pinch scroll (1.01^deltaY), Fit = Home button (bbox -> fov dist -> 1.2x pad -> square-on), roll reachable in math but NOT wired to input. Camera = PerspectiveCamera(fov50/near0.1/far20000), Camera3D{position,quaternion}; persistence via commitCamera -> scheduleViewSave(400ms) -> scene.json, restore via CameraRefBridge/CameraFitter. Source: tools/topology-vscode/src/webview/three/ (interaction-controls.ts, camera-ui.tsx, scene-content.tsx, ThreeView.tsx).
- **Lint cleanup** (merge 9d405243): dead `comparePortLists` removed, `min`/`max`/range-over-int modernizations; removed the branch-local `port-ring-anchors-spec.html` from main (tracking + disk).
- **Port ring-anchor layout** (merge 04704a57): node port PHYSICAL layout redesigned — replaced the directional `side`x`slot` grid (4 buckets) + free `anchor` vectors with ONE FLAT ARRAY of evenly-spaced ring anchors. Each port is a single `anchorId` index into the array. N = floor(2*pi*R/(d+p)), R = nodeRadius (node size per kind) so N varies by kind; d=8,p=2 (tunable consts in nodes/Wiring/port_geometry.go). Go owns the ring geometry + the drag-snap (snapToRingAnchorIndex); TS only unprojects the pointer (camera) to a world-space target and renders Go's stream. Editing a port reuses the `port-anchor` edit op (parity: messages.ts / handle-message.ts / stdin_reader.go). Verified working in editor (ports on ring, network runs, drag snaps). Key files: nodes/Wiring/port_geometry.go (ringAnchorCount/ringAnchorDir/nodeRadius/snapToRingAnchorIndex; portDir resolves ONLY via the ring), loader.go specPort {Name, AnchorId *int}, topology/nodes/*/{inputs,outputs}/*.json ({name, anchorId}), TS types.ts/parse-nodes-edges.ts/geometry-helpers.ts.
- **Remove ReadGate kind + inactive subtree** (merge e699cc17): deleted the unused ReadGate node kind (package, generated registries, fixtures), removed topology/inactive/, renamed Input's port ToReadGate -> ToChainInhibitor.
- **One-bead, node-drives-own-edges** (earlier merge 1a3bdb7f): network emits ONE bead per fire; each node drives its own outbound bead(s) to delivery on its OWN goroutine (no train, no seq, no per-bead walker). See nodes/Wiring/paced_wire.go (DriveBeadsToDelivery/advanceBeadLocked, EmitOneDriven, DriveAll). Memory: feedback_place_all_then_drive_concurrently.

### Active node kinds (current topology, nodes 1-7)
Kinds: Input, ChainInhibitor, HoldFlip, WindowAndGate, Excitatory (holds/pulses a value; sample-and-hold). Nodes: 1 Input, 2/3 ChainInhibitor, 4 HoldFlip, 5 WindowAndGate, 6/7 Excitatory. Paths: 1->{2,6}, 2->{3,7}, 6->5.FromLeft, 4->5.FromRight, 7->4, 2->1 feedback; dropped 2->5 and 3->4; node 3 output dangles.

### Carry-forward (hard-won this session)
- GEOMETRY IS GO-OWNED (MODEL.md/CLAUDE.md): Go computes positions/rings/snaps + streams; TS only handles camera/input and renders Go's stream. TS computes no layout.
- SWARM CAUTION: a background Agent dispatch fanned out into ~20 duplicate agents, duplicated commits, and committed one unrequested change (reverted). Keep subagents FOREGROUND; spot-check git log before pushing.
- IDE DIAGNOSTICS WERE REPEATEDLY STALE this session (phantom broken imports / unused funcs that were actually wired). Verify against `go build` / grep, not the diagnostic panel.
- When a node has multiple outbound edges, PLACE all beads then DRIVE all together in one loop (never per-edge in series) — see memory feedback_place_all_then_drive_concurrently.
- Magic constants in spatial code (spacing/box) should be DERIVED from the content, not hand-picked — a fixed 330 lattice spacing collapsed the graph; deriving from layout fixed it. (Mirrors: a singularity/occlusion means the representation/scale is wrong, not the inputs.)
- Fan-OUT needs separate Out ports (one Out binds only labels[0]); fan-IN needs separate In ports. To send the same value to N targets concurrently, the node spawns a goroutine per wire (node 1 does this for 2 and 6) or uses place-all + DriveAll.
- Dragging at a tilted camera can move a node along the DEPTH axis (the editable axes are the two facing the camera), landing it directly behind another node (same i,j, different k) — an occlusion hazard; the pick now handles it (nearest body wins), but be aware nodes can stack in depth.
- Sphere move/resize re-propagate from a fixed anchor — true "grab any node, rest flexes" needs per-edge directions + re-rooting (stored Dir is per-node relative to one BFS parent).

### Carry-forward ideas (not blocking node-lattice)
- Node 3's dangling ToNext1 port could be removed if a single-output ChainInhibitor is wanted.
- Camera target is runtime-only — could be persisted to scene.json (extend Camera3D with a target, update serialize/parse + CameraRefBridge restore) so orbit pivot/zoom survive reload exactly; deferred — re-seeding from scene center is fine for now.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored to the
state you're leaving the branch in, and commit the re-rendered handoff ON THE ACTIVE TASK
BRANCH so it merges to main with the work — do NOT make standalone handoff commits directly
to main. When there is no task branch in flight, fold the handoff update into the next task
branch when it starts (the handoff is shared, not branch-local-stripped, so it merges
cleanly); if you genuinely must commit it on main, PUSH immediately so main is never left
with a loose unpushed commit. The principle: main advances only through merges.
Do not rely on chat history; the next AI may be a fresh model with no
transcript. The rendered handoff must itself contain this same ALWAYS clause so the loop
is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the structural
source of truth; update the template when an invariant changes.
