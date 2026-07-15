---
name: project_lock_persistence_survives_respawn
description: Polar node-node locks enforce only in mover memory; each follower must self-persist its polar or a run/respawn reloads stale positions
metadata:
  type: project
---

SUPERSEDED: this describes an older `RootMove`/`eqNodeNode` cascade model; the code
has since moved to the decentralized `moveMsgKindEqualize`/`moveMsgKindTrigger` model
in `nodes/Wiring/node_move.go` (see [[project_lock_propagation_decentralized]]).
`eqNodeNode`, `RootMove`, and `nodes/Wiring/locks.go` no longer exist — kept here as
historical record of the persistence bug and its original fix, not as a current map.

Polar node-node locks (formerly `eqNodeNode`) were enforced ONLY
in each mover's held geometry during a drag cascade originated from the old `RootMove`.
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
