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
- **Phase 6 — DONE** (`4b28b293`). `PanSceneSphere(delta)` moves `md.sceneSphere.Center`,
  re-fits `Radius` (`fitSceneRadius`), holds node worlds fixed (scene polars recompute on the
  next save via the dual-write). Trigger (David's call): the **camera pan** drives it —
  `PanViewpoint` calls `PanSceneSphere(delta)`; ORBIT does not (separate `OrbitViewpoint`
  methods). The bare `save` command `flushNow`s the sphere into `scene.json`, ACTIVATING the
  polar-load path. Guards: `TestPanSceneSphereHoldsNodesWorldFixed`,
  `TestPanSceneSphereThenNodeSaveUpdatesScenePolar`, `TestSceneSpherePersisterFlushNow`.

## All phases complete

The polar model is fully implemented (phases 1–6) and the drag blow-up is fixed. Remaining
before merge: live-verify in the editor (reload, drag the 5/6 pair, pan), and decide on
merging `task/decentralized-lock-propagation` (+ the two stacked branches) to `main`.

### Known pre-existing issue (not from this work)
`md.polarEqs` is appended during lock authoring while node goroutines read it unsynchronized
— a `-race` failure in `TestEquationAppliesImmediatelyOnCompletion`, present on the
pre-change base too (`stop-checks` does not run `-race`, so it is masked). Worth a follow-up
guard on `md.polarEqs` access.

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
