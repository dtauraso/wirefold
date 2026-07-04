---
branch: task/decentralized-lock-propagation
---

# Polar-model implementation — progress & remaining

Spec: [polar-model.md](polar-model.md). Branch: `task/decentralized-lock-propagation`.
Status recorded at the phase-2b pause.

## Done (committed, suite green)

| Commit | What |
|---|---|
| `fa41bccc` | Model doc `polar-model.md` (scene sphere root, two polars, stored offsets, pan) |
| `733ba856` | **Phase 1** — `sceneSphere{Center,Radius}` entity; persisted in `scene.json` (`sceneSphere` key); `LoadSceneSphere` (file, else content-fit default). Additive. |
| `c3a3ed6f` | **Phase 2a** — node-position save DUAL-WRITES `scenePolarR/Theta/Phi = cart2polar(world − sceneCenter)` into `meta.json` alongside `x/y/z`. Persister reads live scene center via a closure. |
| `ae2cdc56` | **Phase 2b** — load derives node world = `sceneCenter + polar2cart(scenePolar)` when a node has scene-polar fields AND a persisted sphere exists; else legacy `x/y/z`. `LoadTopology` loads the sphere before positioning and installs `md.sceneSphere`. |
| `8918f9a4` | **Phases 3–5** — each node owns its LOCAL polar offset about its lock-center (`nodeMover.localPolar`), taken from the EXISTING movement-link value on first touch (`localPolarOf`) — NO seed step. `lockRecalc` nudges the stored offset from the sender's owned offset (`moveMsg.FromLocalPolar`), never re-deriving via `cart2polar` of a moving center. **The drag blow-up is FIXED.** Guard: `TestPolarLockNoBlowup` (1.2e11 before → bounded now, `-race` clean). |

**The lock blow-up is FIXED** as of `8918f9a4`. The node-position persistence is polar-capable
(phases 1–2) and back-compatible. Live drag of a node that is both a lock center and a
torus-reached satellite no longer flies away.

### Key files touched
- `nodes/Wiring/sphere_layout.go` — `sceneSphere` type + `contentFitSceneSphere`.
- `nodes/Wiring/scene_sphere_persist.go` (new) — `loadSceneSphere` / `writeSceneSphere` / `LoadSceneSphere`.
- `nodes/Wiring/scene_node_pos_persist.go` — `nodePosPersister.sceneCenter` closure; `writeNodePosition(..., sceneCenter, haveSceneCenter)` dual-write.
- `nodes/Wiring/loader.go` — `specNode.ScenePolar{R,Theta,Phi}`; `toNodeGeom(sceneCenter, hasScene)`; `buildFromSpec(..., sphere, hasScene)`; `LoadTopology` loads sphere first.
- `nodes/Wiring/node_move.go` — `md.sceneSphere` field; `EnableEditPersist` wires the pos-persister `sceneCenter` closure.
- `main.go` — `md.LoadSceneSphere(topologyPath)` in the load sequence.
- Tests: `scene_sphere_persist_test.go`, `scene_node_pos_polar_test.go`, `loader_scene_polar_test.go`.

## Remaining

- **Phases 3–5 — DONE** (`8918f9a4`). (Phase 4's "pure-polar composition" landed as the
  polar2cart(offset)+liveCenter derivation; a closed-form spherical sum was deemed the same
  operation and not separately needed. The "no seed" decision: the offset comes from the
  existing link value via `localPolarOf`, not a seed function.)
- **Phase 6 — pan moves the scene sphere (NOT started).** The OPERATION: move
  `md.sceneSphere.Center`, hold node world positions fixed, re-fit the radius; the node scene
  polars then recompute about the new center on the next save (the dual-write already does
  `cart2polar(world − sceneCenter)`). Also: **persist the scene sphere on `save`** so a
  reload actually loads positions as polar (until the sphere is persisted, `loadSceneSphere`
  returns not-ok and load stays on cartesian `x/y/z`).
  - **OPEN — trigger:** scene-sphere pan is a NEW op distinct from camera-pivot pan
    (`viewpoint.pan`). Which gesture invokes it? Needs David's decision before wiring
    (`gesture.go` `gestWheel` / a new gesture). The method + persistence can be built first,
    trigger wired after.

## Open threads / notes

- The branch still carries the earlier **unified cascade** (`a7287058`) that HAS the
  blow-up; phases 3–5 replace its internals. Until phase 5 lands, dragging the 5/6 pair (or
  any node that is both a lock center and a moving satellite) still flies away.
- Scene sphere is a SEPARATE entity from the camera pivot (David confirmed). Camera orbit
  must NOT rescramble node scene-polars.
- Pan detail (David confirmed): on pan the node's WORLD position is the invariant; its scene
  polar is recomputed about the moved center. (Mirror of move/lock, where polar is the input.)
- `contentSphereOf` (derived centroid) stays only as the phase-1 default and for the nav
  overlay display; it is NOT the authoritative reference.
- `scene.json` for the live `topology/` currently holds 15 locks incl. a radius cycle; the
  `buffer-log-equivalence` vitest drives that scene, so phase-5 changes must keep it bounded.
