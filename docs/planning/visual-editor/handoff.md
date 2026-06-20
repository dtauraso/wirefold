---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## Active branch: none — polar navigation SHIPPED to `main`

The polar-navigation work is merged to `main` (merge `3230850c`, pushed to origin) and
all its task branches are deleted. There is no in-flight branch. Start new work from
`main` with a fresh `task/<short-name>` branch.

## What shipped

**Navigation is polar; Cartesian is confined to two edges.** The camera stays
renderer-native (three.js `position` vector + orientation quaternion — the medium, owned
by TS, saved to `topology.scene.json` opaquely through Go's `writeScene`). All navigation
MATH is polar, and the only Cartesian lives in (a) the camera's storage and (b) the
conversion helpers in `tools/topology-vscode/src/webview/three/polar.ts`. The handler
`interaction-controls.ts` holds only polar quantities.

- **Pan** — wheel px → polar `(r, angle)` via `deltaToPolar` (input edge); `planeSlide`
  recombines onto the camera right/up basis (output edge); camera + target slide together;
  `set-origin` to Go re-bases the node frame.
- **Rotation** — MOTION-DRIVEN great circle (empty-space drag). Each frame rotates by the
  small arc between last and current cursor points; axis from `θ + 90°` (no cursor-point
  cross product, no quaternion), applied via `arcAxisAngle` + `rotateAboutAxis`. Roll works
  (it accumulates from curved drags); screen-relative, so a horizontal drag stays horizontal
  only when the view is upright — that is the accepted tradeoff of motion-driven.
- **Zoom** — `scaleRadius`: explicit polar `r` about the node nearest the cursor; angles
  held; floored at MIN_DIST; clamped inside the large sphere.
- **Diagram layout** is polar in Go (`(r,θ,φ)`); co-sphere radius coupling in
  `node_move.go` resizes the sphere(s) a dragged surface node sits on (this superseded the
  old `x-lock` and `drag-resize-sphere` branches, both deleted as redundant).

**Guards (code self-defends).** Two greps wired into the Stop hook
(`scripts/stop-checks.sh`):
- `tools/check-polar-only-nav.sh` — no Cartesian rotation math
  (`setFromUnitVectors`/`cross`/`setFromAxisAngle`/`setFromMatrixColumn`/`Spherical`/`Raycaster`/`unproject`)
  in the nav handler.
- `tools/check-no-camera-roundtrip.sh` — camera state never reconstructed from a position.

**Explainer.** `docs/polar-sphere.html` "Plan" tab is synced to the shipped architecture
(camera = Cartesian medium; rotation = motion-driven; Phases 0/1 angle-of-record and
5.2/5.3 angle-persistence marked superseded by design).

## Carry-forward (hard-won — READ THIS)

- **THE AI DEFAULTS TO CARTESIAN AND IT SURVIVES CORRECTION.** Across ~dozens of rotation
  rebuilds the assistant kept re-encoding the polar spec as Cartesian machinery (arcball +
  Bell/Holroyd edge map, frozen-grab single-big-arc → antipode flip, turntable → no roll,
  grab-a-point → finite-sphere rim + snap). Each time it added a clamp/guard/fold at the
  boundary instead of defining honest behavior. The structural defense is `polar.ts` + the
  guard: navigation goes THROUGH the toolkit; do not reach for `Vector3.cross`, sphere
  raycasts, or angle-stored orientation in nav logic. (Memory:
  `feedback_make_bug_class_unrepresentable`, `feedback_users_interaction_spec_is_the_model`.)
- **A finite sphere viewed from outside has a real rim** (visible cap ≈ `acos(R/D)`, not
  90°). "Grab a point on the sphere" inherits that rim; "motion-driven" has no sphere and no
  rim. The user chose motion-driven knowing the tradeoffs (path-dependent roll, not
  pixel-locked).
- **Run the guards before declaring nav work done**; the Stop hook does it automatically.
  NEVER run the sim in the foreground.

## Open / next (friction-driven, nothing in flight)

1. **Phase 8 hardening** (deferred per post-v0 posture): degenerate scenes (empty graph,
   single node), gesture-conflict edge cases, touch/pinch. Do when the friction appears.
2. **Phase 5 polish** (optional): animate the Home/Fit transition. Save/restore already
   works (position + quaternion) and is correct as-is — do NOT migrate it to angle
   persistence (that reintroduces the rejected angle-of-record representation).
3. Possible dead exports in `polar.ts` (`cameraFrame`, `screenToPolar`) after the
   motion-driven revert — prune if confirmed unused.

### Verify (NEVER run the sim)
`cd tools/topology-vscode && npx tsc --noEmit && rm -f out/webview.js && npm run build`,
then `bash tools/check-polar-only-nav.sh` and `bash tools/check-no-camera-roundtrip.sh`
from the repo root. Editor opens via the VS Code command; reload = close panel → Reload
Window → re-run command. Runtime logs are `.probe/*.jsonl`. Git: run from repo root
(`/Users/David/Documents/github/wirefold`).
