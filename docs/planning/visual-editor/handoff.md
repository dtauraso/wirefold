---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## Active branch: task/roll-axis-camera — NAVIGATION GOING FULLY POLAR (NOT merged)

This branch started as "make roll/rotation work" and turned into a deeper decision:
**navigation should be polar end-to-end (pan, zoom, rotate), with Cartesian demoted to
pure plumbing (render + the cursor's raycast).** Driven by a hyperphantasic user whose
spatial model is exact, so the usual Cartesian approximations read as *wrong*, not
"fine."

### The decision (user endorsed)
- **Everything polar.** The diagram is already polar (the merged polar layout model);
  navigation is the last Cartesian holdout, and that's where all the friction lived.
- **Diagram-sphere pole = the diagram's own top axis = world Y, FIXED** (the user chose
  this — "do b" — over a camera-up pole). The diagram's polar frame is anchored to the
  diagram, not the view. (The MOUSE/pan sphere is a SEPARATE sphere with a different
  pole — see the pan construction below.)
- **Cartesian is plumbing, not the model.** It survives only at two edges: the cursor's
  raycast (input) and converting an angle pair to a world point for drawing (output).
- This **overturns the old `pan/zoom = Cartesian prism` doctrine** in MODEL.md/CLAUDE.md.
  The docs have NOT been updated yet — doing so is an open task.

### The polar PAN construction (user's exact spec — NOT yet coded)
This lives on the **mouse's own polar sphere**, which is distinct from the diagram sphere:
- **Pole = the VIEW** (the view axis) — the mouse-sphere's pole is the view, not world Y.
- **refX = the first line segment; `r` lies along it.** The mouse's horizontal travel is
  reinterpreted as a radius: `Δx_cartesian → r_polar`. The top of the sphere sits at
  distance `r` out along refX. (This `Δx → r` is the ONLY thing specified about the mouse;
  do not extrapolate θ/φ mappings beyond it.)
- **The pole and refX meet at 90°** (a quarter-turn of φ apart).
- **A 3rd line sits at 90° of φ to BOTH** the pole and refX (the third orthogonal axis).
  **That 3rd line is the polar pan** — the polar version of the Cartesian pan offset.
  Pan = movement along this 3rd line (perpendicular to both the view and the r-axis).
- So the frame is `{pole = view, refX (carries r = Δx), 3rd line = pan}`, mutually
  perpendicular. Functionally pan then rebases the diagram-sphere origin + Go `reOrigin`
  recomputes every node's (r,θ,φ) — but the OFFSET comes from this 3rd-line construction,
  not a Cartesian screen delta.

### What is built (this branch)
- **`polar.ts` — the polar toolkit** (new). `Polar {theta, phi}`, `PolarFrame`,
  `makeFrame` / `toWorld` / `fromWorld` / `equatorDir` / `equatorPoint`. The ONLY
  Cartesian is quarantined inside `makeFrame` (builds the equatorial ref axes once) and
  `toWorld` (render output). Navigation code importing only this has no cross product in
  reach — so the cross-product degeneracy (the 180° flips) cannot be expressed.
- **Rotation = polar** (`interaction-controls.ts`, build marker `polar-pole-v17`).
  Cursor screen-polar (ρ,θ) → spherical (Φ from the top pole, Θ around it); rotation is
  built from the angle CHANGES about polar-named axes — ΔΘ rotates about the POLE, ΔΦ
  about the EQUATORIAL axis at azimuth Θ+90°. No cross products of cursor points; log
  reads in polar. NOTE: predates `polar.ts` and still builds its axes with a couple of
  `Vector3` ops — moving it onto `polar.ts` is an open task.
- **Pan GUIDE (visualization only)** — `PanGuide.tsx`, rebuilt on `polar.ts`, pole =
  world Y. Draws from the cursor as (θ,φ): the **disk** (meridian at azimuth θ —
  angle-defined, can't degenerate), the **green line** (equator/horizontal-torus diameter
  at θ), the **red radius** (C→P), and the **trig right triangle** with the right angle at
  the foot **G on the green line** (`R·sin φ` along θ), height `R·cos φ` up the pole,
  hypotenuse = radius. Right angle on the green line by construction, every frame, no
  flips. THIS IS A GUIDE — note it currently uses the world-Y diagram pole; the user's
  pan construction above uses the VIEW as the pole, so the guide may need re-pointing when
  pan is actually wired.
- **Zoom GUIDE** — `ZoomGuide.tsx`: triangle C → camera(tip of r) → selected node,
  hypotenuse = the zoom travel line. Visualization only.
- **Tori reverted to WORLD-FIXED** (`NavGuides.tsx`): 4 great-circle tori at 0/45/90/135
  about X; horizontal torus normal = world Y = the diagram pole.
- **`docs/polar-sphere.html`** — 7-tab visual explainer (2D polar / 3D spherical / what 3D
  adds / the frame / the pan triangle / Cartesian pan / mouse-as-polar), SVG diagrams from
  one shared tilt-projected sphere fn. The "mouse-as-polar" tab over-states beyond `Δx→r`
  (it sketches θ too) — trim it to just `Δx→r` and the 3rd-line pan when revisited.
- `cursor-store.ts` (new): last canvas cursor px, feeds the guides.

### NOT built / open (the real next steps)
1. **Actual polar PAN is not wired.** The pan that runs is still the OLD Cartesian camera
   translation + throttled `set-origin` (interaction-controls.ts ~line 904). Wire the
   construction above: gesture → `Δx → r` on the mouse-sphere (pole = view) → the 3rd-line
   pan offset → rebase the diagram-sphere origin → Go `reOrigin` recomputes node (r,θ,φ).
2. **Actual polar ZOOM is not wired** (partly designed). `r` = camera's radial distance
   from center (R on the sphere, <R inside); each zoom unit travels the hypotenuse toward
   the SELECTED node; rotation/pan clears the zoom line.
3. **Rotation needs hands-on editor verify** (direction/feel). Polar in code, untested by
   the user this session; a reversed sense is a one-line flip.
4. **Move rotation onto `polar.ts`** (it has its own vector math now).
5. **Update MODEL.md + CLAUDE.md** to "navigation is polar end-to-end; Cartesian is
   plumbing" — overturns the written `pan/zoom = Cartesian prism` line. Pending go-ahead.
6. **Trim the explainer's mouse-as-polar tab** to just `Δx→r` + the 3rd-line pan.

### Verify (NEVER run the sim)
`cd tools/topology-vscode && npx tsc --noEmit && rm -f out/webview.js && npm run build`.
Editor opens via the VS Code command (no backing file); reload = close panel → Reload
Window → re-run command. Build markers grepped in `.probe/ts.jsonl` confirm the live
bundle loaded (rotation logs `"build":"polar-pole-v17"`). Diagnostic screenshots from this
session are under `docs/planning/visual-editor/screenshots/`. Git: run from repo root
(`/Users/David/Documents/github/wirefold`) — Bash cwd resets, and `cd tools/...` then a
repo-root path fails.

### Carry-forward (hard-won this session — READ THIS)
- **THE AI DEFAULTS TO CARTESIAN AND IT SURVIVES CORRECTION.** Across ~17 rotation
  rebuilds the assistant kept re-encoding the user's polar spec as Cartesian machinery
  (cross products, world axes, `+Y`, orthographic maps) below the level it was watching,
  reintroducing the exact pathologies (180° flips, cross-product degeneracy, world-vs-view
  mismatch) the user objected to. "Be more careful" FAILED. The structural fix is
  `polar.ts`: make the code only able to express polar so Cartesian has nowhere to sneak
  in. New navigation work goes THROUGH the toolkit; do not reach for `Vector3.cross` in
  navigation logic. (Memory: feedback_make_bug_class_unrepresentable,
  feedback_users_interaction_spec_is_the_model.)
- **The cross product IS the degeneracy.** `a × b → 0 with undefined direction` when a∥b
  is the noise-flip. Angle arithmetic (`θ+90°`) has no such case. If a "fix" cycle starts
  chasing flips/jitter/degeneracy, the cause is a cross product / Cartesian frame, not a
  knob to tune.
- **David's plain-language spec already names the exact model** — restate it as a concrete
  angle/axis rule and implement that literally; don't pattern-match onto a textbook
  algorithm (the Shoemake-arcball detour cost many cycles). "polar," "same rate at any r,"
  "not cartesian" are correctness requirements, not vibes.
- **Two great circles always meet at 2 points; only coincident circles degenerate.** The
  pan green line was a `pole × n` that collapsed when the motion-disk coincided with the
  horizontal torus — fixed structurally by making the disk a meridian (angle-defined).

## Background: POLAR coordinate model is MERGED to main (2026-06-16)

`main` has the polar layout (every node = one outer polar (r,θ,φ) from the prism-center
origin; all nodes flat roots; sphere R/surface coords derive-on-read; pole=+y; Cartesian
only at camera/render/save). Go: nodes/Wiring/{polar,prism,derived,node_move,lock}.go;
bridge emits render-ready Cartesian + sphereR + ring normals; pump = plotter. This branch's
navigation work is the camera-side counterpart: make navigation polar against that already-
polar model. The full layout design doc was branch-local (stripped at merge); recover from
task/spherical-layout history if needed. (Earlier shipped-to-main history — pick occlusion,
Excitatory nodes 6/7, camera restore, node lattice, edge arrowheads, persistent-target
camera, port ring-anchors, one-bead drive — is in git log; trimmed here to keep the
active-branch state in focus.)

### Active node kinds (topology, nodes 1-7)
Input, ChainInhibitor (2/3), HoldFlip (4), WindowAndGate (5), Excitatory (6/7).
Paths: 1→{2,6}, 2→{3,7}, 6→5.FromLeft, 4→5.FromRight, 7→4, 2→1 feedback; node 3 dangles.

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
