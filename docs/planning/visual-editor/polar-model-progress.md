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

**Nothing visible has changed yet** and **the lock blow-up is NOT fixed yet** (that is phase 5).
Everything so far is additive + back-compatible: positions can now round-trip through polar,
but live behavior is unchanged.

### Key files touched
- `nodes/Wiring/sphere_layout.go` — `sceneSphere` type + `contentFitSceneSphere`.
- `nodes/Wiring/scene_sphere_persist.go` (new) — `loadSceneSphere` / `writeSceneSphere` / `LoadSceneSphere`.
- `nodes/Wiring/scene_node_pos_persist.go` — `nodePosPersister.sceneCenter` closure; `writeNodePosition(..., sceneCenter, haveSceneCenter)` dual-write.
- `nodes/Wiring/loader.go` — `specNode.ScenePolar{R,Theta,Phi}`; `toNodeGeom(sceneCenter, hasScene)`; `buildFromSpec(..., sphere, hasScene)`; `LoadTopology` loads sphere first.
- `nodes/Wiring/node_move.go` — `md.sceneSphere` field; `EnableEditPersist` wires the pos-persister `sceneCenter` closure.
- `main.go` — `md.LoadSceneSphere(topologyPath)` in the load sequence.
- Tests: `scene_sphere_persist_test.go`, `scene_node_pos_polar_test.go`, `loader_scene_polar_test.go`.

## Remaining (not started)

- **Phase 3 — local polar owned per node.** Each node owns its LOCAL polar offset about its
  lock-center (the movement-link polar, made per-node-owned so it is race-free and
  authoritative). Stored; seeded once from good positions; never re-derived from live world.
- **Phase 4 — pure-polar composition.** Closed-form spherical vector sum along the path
  (scene-center → local-center → node), polar in / polar out, no cartesian exposed.
- **Phase 5 — cascade fix (THE BLOW-UP FIX).** Rewrite `lockRecalc`/`lockNeighbors`
  (`locks.go`) + the `moveMsgKindLockUpdate` handler (`node_move.go`) to CARRY and NUDGE the
  stored local polar, removing ALL `cart2polar(node − center)` reconstruction from the
  cascade. Violation-check termination; rigid-translate on a center move. Verify the
  center-and-satellite structure that blows up today stays bounded and terminates.
- **Phase 6 — pan moves the scene sphere.** New op: move `md.sceneSphere.Center`, hold node
  world positions fixed, recompute each node's scene polar about the new center, re-fit the
  radius. Distinct from camera orbit (separate entity, confirmed with David).

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
