# Polar-frame rewrite — the plan (preserved from the deleted task branch)

This is the model and plan David specified. The prior attempt (branch
`task/rebuild-port-edge-polar`, tip `26ebf99d`, deleted) got the storage layer done
but retreated from the full frame by mislabeling it "purity." Keep this so the next
attempt starts from the model, not from scratch.

## The frame (verbatim intent)

- The **center of the scene sphere is the ONLY saved cartesian** value. Everything else
  in the files is **polar**.
- **Cartesian happens ONLY at display** — the `polar2cart` conversion that sends geometry
  to the GPU. No cartesian center as a source of truth anywhere in the middle.
- This is not optional "purity" — it is the requirement. It inverts the codebase's (and
  the industry/default) cartesian habit on purpose; that inversion is the point of the
  project. The failure mode to avoid: reverting to a cartesian working representation
  "for convenience" and calling it done.

David's own check questions (and the honest answers): it is **not** a logical conflict,
**not** mathematically impossible; it is only **against-the-grain / inconvenient** vs how
the code and coders default. That inconvenience is not a reason to stop.

## The feature the frame enables

- A **port owns its own `(θ, φ)`** (polar, stored: `ownTheta`/`ownPhi`). Not aimed at the
  partner, not a ring-anchor slot.
- The **edge leaves the port along the port's `(θ, φ)`** — the edge lines up with the port.
- When you **drag a node, the connected node moves** so the edge stays lined up with the
  port's r. Verification target: node 9's port and 9's edge share the same `(θ, φ)`, and
  dragging holds it — no thrash, no freeze.

## Co-located ports (resolves the bidirectional case — NOT a conflict)

A bidirectional connection X↔Y has two edges through (up to) four ports. The two ports on
one node that connect to the **same partner** are **one co-located port** — they share a
single polar location/direction. Then both edges leave the node at the same point in the
same direction and **both line up**. Treating them as separate ports is the error that
makes it look like an over-constraint; there is no conflict once they co-locate.

## Torus-lock rule (also NOT a conflict)

If a port carries a `port ∈ torus` lock, that port **stays locked to the torus (ring)** —
that rule holds, untouched. The edge then lines up with the **other** port: move the
partner node so the edge lands on **its** port's r. One port obeys its torus rule; the
other node moves to meet it. No contradiction.

## Phases & tasks (many small commits)

Storage was completed on the deleted branch (re-do or cherry-pick from `26ebf99d`):

1. **Storage is polar** — DONE previously:
   - node `meta.json` holds `scenePolar (r,θ,φ)` about the sphere center; `x/y/z` removed.
   - loader (`toNodeGeom`, both the monolithic AND the tree `jsonMeta` — the tree one was
     the blob bug) reads scene-polar as source of truth.
   - persist writes scene-polar only, deletes `x/y/z`.
   - `x/y/z` survives ONLY as a no-sphere legacy/fixture INPUT converted at the load boundary.
2. **Port direction is polar-owned** — port carries `ownθ/φ`; delete `portDirAimed`/aim as
   the direction source; torus-lock ring-projection is the only override.
3. **Edge geometry from the port's polar** — edge endpoint/direction come from the port's
   `(θ,φ,r)`, not computed from two moving endpoints.
4. **Movers hold polar (the part that was retreated from)**:
   - `nodeGeom` holds `ScenePolar` + `SceneCenter` (anchor); **remove the cartesian
     `Center` field**. `nodeWorldPos` derives world = `SceneCenter + polar2cart(ScenePolar)`.
   - Convert every mover center-set site (nodeMover/edgeMover handlers, `RootMove`,
     `fanCenters`, snap seeding) to update `ScenePolar` (from the pointer/world at the
     boundary), never a held cartesian center.
   - Colinearity IN POLAR: on a drag, move the partner so the edge lands on the port's
     `(θ,φ)` — the partner's **port** on the ray (not its center). Co-located ports so a
     bidirectional pair uses one shared port. Torus-locked port stays; partner moves.
     One-hop origination only (no re-propagation) so a cyclic edge graph can't storm/freeze.
5. **Cartesian only at the emit/GPU boundary** — `polar2cart` appears only in the buffer
   emit (node world, port world, edge segment) and at the two input boundaries (pointer,
   legacy x/y/z). Audit that nothing holds a cartesian center as a source of truth.
6. **Verify**: node 9 port `(θ,φ)` == 9 edge `(θ,φ)`; drag holds it; bidirectional 9↔6
   lines up; torus-locked ports stay on the ring; `stop-checks.sh` + `go test -race` green.

## What actually worked last time (reuse)

- `specPort.ownTheta/ownPhi` + TS `Port` parity; `portOwnPolar`; `portDirAimed`/
  `portWorldPosAimed` preferring `OwnDir`.
- Colinearity as a one-hop constraint in `lockRecalc`/`RootMove` with an immutable
  per-edge table (race-clean) — but it aligned the partner's CENTER, and needed the
  partner's PORT on the ray (the fix that made edge-dir == port-dir headlessly).
- The blob bug: the tree loader's `jsonMeta` MUST carry `scenePolar` or all nodes collapse
  to the origin.

## The one thing to not do again

Do not stop at the storage layer and call the movers-hold-polar part optional. The frame
is the requirement. Finish it.
</content>
