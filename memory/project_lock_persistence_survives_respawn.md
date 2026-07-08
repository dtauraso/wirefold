---
name: project_lock_persistence_survives_respawn
description: Polar node-node locks enforce only in mover memory; each follower must self-persist its polar or a run/respawn reloads stale positions
metadata:
  type: project
---

Polar node-node locks (`eqNodeNode` in `nodes/Wiring/locks.go`) are enforced ONLY
in each mover's held geometry during a drag cascade originated from `RootMove`.
Nothing re-runs the cascade at load: `LoadPolarEqs` reinstalls the equations but
re-emits only port-torus geometry. And clicking **run respawns Go**, which reloads
every node's `scenePolar` fresh from disk (`buildFromSpec`).

Consequence: a lock-adjusted FOLLOWER position that is not persisted to its own
`meta.json` is lost on run — the node reloads off-constraint even though the lock is
still active. `RootMove` persists only the *dragged* node; followers must persist
themselves. Fix (commit 94666680): each `nodeMover` self-persists its polar in the
`moveMsgKindLockUpdate` handler via a `persistPos` hook — decentralized, matching the
per-mover ownership model. `nodePosPersister.schedule` is mutex-guarded so concurrent
per-id schedules from multiple mover goroutines are race-free.

Open gap (deliberately not fixed): there is no load-time re-solve of the `eqNodeNode`
cascade, so an already-inconsistent-on-disk state is not self-healed at load — it's
only kept consistent going forward by the self-persist. Also `RootMove` still does not
rigidly translate a shared Center node's satellites when the Center itself is dragged
(the Center is not a lock-neighbor of its terms), despite MODEL.md prescribing it.

Related: [[feedback_headless_repro_verifies_persistence]] (green unit tests hid live
persistence failures — this bug only manifested through the real respawn path).
