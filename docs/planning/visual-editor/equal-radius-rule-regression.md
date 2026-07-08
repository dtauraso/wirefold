---
branch: task/equal-radius-rule-regression
---

# Regression: `(1→2, r) = (1→3, r)` equal-edge-length rule

## Summary

The equal-edge-length lock — "the edges from node 1 to node 2 and from node 1 to
node 3 are the same length" — stopped enforcing the intended geometry after the
cartesian→polar rewrite. The rewrite kept the *formula* `A.r == B.r` but silently
changed **what `r` is measured from**: node 1 (the shared node) → the scene-sphere
center. Formulaically equal, geometrically a different constraint.

## What the rule is

- An **`eqNodeNode`** polar equation on the **r component** (`compR`, term-code 4, unsigned).
- **Center** = node 1; **terms A, B** = nodes 2 and 3.
- Authored at `nodes/Wiring/gesture.go:586`
  (`polarEq{Center: md.ruleCenter, A: ruleTerms[0], B: ruleTerms[1], ...}`);
  each term carries `Comp = compR` (`ruleTermCode`/`decodeTermCode`, gesture.go:766, :782).
- Struct: `polarEq` / `polarTerm` / `polarComp` at `nodes/Wiring/locks.go:31-73`.
- Applied in `lockRecalc` / `lockNeighbors` (`nodes/Wiring/locks.go`).

## The regression

Commit **`2484979f`** — *"refactor(polar) slice 5: locks/colinearity in pure
scene-polar (drop center frame)."*

### Before (`2484979f^`) — center-frame, cartesian length
`r` was measured **about the equation's Center (node 1)**:
```
np, _  := md.localPolarOf(m, eq.Center)        // self's offset about node 1
sp     := fromLocalPolar[eq.Center]            // sender's offset about node 1
target := fromTerm.Sign * compOf(sp, fromTerm.Comp) * selfTerm.Sign
setCompOf(&np, selfTerm.Comp, target)
newWorld = polar2cart(np).add(centerWorld)     // centerWorld = node 1's world
```
Old doc-comment: *"self's polar about the eq's Center is recomputed by
`cart2polar(selfWorld − centerWorld)`, the sender's polar about that SAME center by
`cart2polar(fromWorld − centerWorld)`."*
⇒ enforced **`|node2 − node1| == |node3 − node1|`** — true equal edge length.

### After (current) — scene-polar, distance from scene center
`nodes/Wiring/locks.go:109-146` (`lockRecalc`): the value written is the sender's
**`ScenePolar.R`** copied onto self's **`ScenePolar.R`** (`setCompOf(&sp, compR, target)`).
`ScenePolar` is `(r,θ,φ)` **about the scene-sphere center** (world =
`SceneCenter + polar2cart(ScenePolar)`). **`eq.Center` (node 1) is never read.**
⇒ enforces **`|node2 − sceneCenter| == |node3 − sceneCenter|`**.

## Geometric divergence

The two agree **only when node 1 sits exactly at the scene center**. Otherwise they differ.

Concrete case (scene center at origin): node 1 = (10,0,0), node 2 = (11,0,0), node 3 = (10,1,0).
- **Old:** `|2−1| = 1`, `|3−1| = 1` → satisfied; 2 and 3 stay one unit from node 1 (equal edge length).
- **New:** `|2−origin| = 11`, `|3−origin| = √101 ≈ 10.05` → violated; the lock forces node 3's
  scene-radius to 11, sliding it radially away from the origin — unrelated to its distance from node 1.

## Why the rewrite did this (fix constraint)

The old center-frame version computed `r` via `cart2polar(selfWorld − centerWorld)` — a radius
**reconstructed from live world positions about a moving node**. That is exactly the
"reconstruct an offset from a moving reference" pattern MODEL.md blames for position blow-ups.
`2484979f` almost certainly dropped the center frame to kill that blow-up; equal-edge-length was
collateral.

**So the fix is not "revert `2484979f`."** It is to express "equal length about the shared node
(node 1)" as a **stored polar-offset constraint**, not a re-derivation from live world — the
blow-up-safe polar formulation of equal edge length.

## Status

Diagnosis confirmed. Theory (user): the polar rewrite substituted a formulaically-equal
but geometrically-different polar setup. **Confirmed.**

**Fixed** on `task/equal-radius-rule-regression`: `lockRecalc`'s compR path in
`nodes/Wiring/locks.go` no longer copies the sender's scene-R onto self's scene-R. It now
resolves `eq.Center`'s current world position (falling back to the old scene-R copy if the
Center node can't be resolved), computes the sender's distance from that Center, and rescales
self's world position along self's own direction from Center to match that distance — then
converts the result back to scene polar. This equalizes edge length about `eq.Center`
regardless of where Center sits relative to the scene sphere's origin. compTheta/compPhi are
unchanged. Regression test: `nodes/Wiring/lock_compr_center_test.go`
(`TestLockCompRAboutOffCenterNode`), which fails on the pre-fix code and passes after.
