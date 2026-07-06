---
branch: task/polar-frame-finish
---

# Polar-frame rewrite — remaining phases (dependency order)

Companion to [polar-frame-rewrite.md](polar-frame-rewrite.md). That doc is the full
plan; this one tracks **what is left** and the order the remaining phases must land in.

## Already done

- **Phase 1 — storage is polar.** meta.json holds scene-polar (r,θ,φ); `x/y/z` removed
  from the tree meta shape; loader + persist are polar-only.
- **Phase 4 keystone — movers hold polar.** `nodeGeom` holds `ScenePolar`/`SceneCenter`
  (no cartesian `Center`); `nodeWorldPos` derives world at the display boundary; every
  center-set site updates polar via `setNodeWorld`. The lock cascade + move path already
  operate on polar (tests confirm).

## Remaining (dependency-ordered)

### 1. Phase 2 — port owns `(θ,φ)`
Foundation: nothing else can read a port's own polar until it is stored. A connected port
carries its own `ownTheta`/`ownPhi` (and `r`); projection is along that stored polar, not a
ring-anchor slot or an aim at the partner.
- **Depends on:** nothing new (frame/storage already in).
- **Blocks:** Phase 3, Phase 4-remainder.

### 2. Phase 3 — edge geometry from the port's `(θ,φ,r)`
The edge leaves the port along the port's stored polar, instead of being computed from two
moving endpoints (today: `segmentBetweenPortsAimed` → `portDirAimed`, `targetCenter −
nodeWorldPos`). This is what makes "node 9's port and edge share the same `(θ,φ)`".
- **Depends on:** Phase 2 (there must be a port polar to read).

### 3. Phase 4-remainder — colinearity in polar
On drag, move the partner so the **edge** lands on the port's `r` — the partner's **port** on
the ray, not its center. Includes **co-located ports** (a bidirectional pair uses one shared
port polar) and the **torus rule** (a torus-locked port stays on the ring; the partner node
moves to meet it). One-hop origination only (no re-propagation storm).
- **Depends on:** Phase 3 (the constraint being satisfied is "edge lines up with port").
  Co-located ports also depend on Phase 2 (two ports sharing one stored polar).

### 4. Phase 5 — purge remaining cartesian *(splits in two)*
- **5a.** Remove inert `specNode.X/Y/Z` + `specPosition` / `view.nodes` x/y/z.
  *Independent — doable anytime, even before Phase 2.*
- **5b.** Remove the aimed-diff center subtraction (`portDirAimed`).
  *Gated on Phase 3, which replaces it — deleting it earlier breaks edge geometry.*

### 5. Phase 6 — verify
node 9 port `(θ,φ)` == 9 edge `(θ,φ)`; drag holds it; bidirectional 9↔6 lines up;
torus-locked ports stay on the ring; `stop-checks.sh` + `go test -race` green.
- **Depends on:** Phases 2, 3, 4-remainder all landed.

## Critical path

```
2  →  3  →  4-remainder  →  6
```

- `5b` folds into Phase 3 (it removes what Phase 3 replaces).
- `5a` is a free-standing cleanup — slot it in whenever.
