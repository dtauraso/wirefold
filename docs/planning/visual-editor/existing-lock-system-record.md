# Existing lock system — record before the double-link rewrite

Snapshot of the lock system as it stands on `main` (commit ec3d7438), to capture exactly
what the double-link model replaces. Files: `nodes/Wiring/lock.go`,
`nodes/Wiring/lock_registry.go`, `nodes/Wiring/node_move.go` (co-sphere).

## 1. Lock types (data structures, lock.go)

| type             | fields                     | meaning |
|------------------|----------------------------|---------|
| `thetaLock`      | Center, Leader, Follower, MirrorPhi | Follower shares Leader's θ about Center. MirrorPhi=false → Follower keeps own φ (shared latitude). MirrorPhi=true → Follower gets φ = −Leaderφ (mirror across the φ=0 meridian). |
| `phiZeroLock`    | Center, Follower, Drive    | Couple Follower↔Center on Center's φ=0 meridian plane. Drive=`moveFollower` (anchor Center, write Follower) or `moveCenter` (write Center, anchor Follower). |
| `equalRadiiLock` | Mid, A, B                  | r(A about Mid) == r(B about Mid). A = authority, B = rescaled. **Currently registered nowhere — inert.** |
| `bisectorMidLock`| Mid, A, B                  | Mid constrained to the perpendicular-bisector plane of feeders A,B (\|A→Mid\| == \|B→Mid\|). Feeders free, Mid follows. |

Constructors: `addThetaLock`, `addMirrorLock`, `addPhiZeroFollowerLock`,
`addPhiZeroCenterLock`, `addEqualRadiiLock`, `addBisectorMidLock`.

## 2. What is registered (lock_registry.go, applied in loader.go order)

```
registerNode2MirrorLocks : mirror(2,3,7), mirror(2,7,3)            # 3/7 mirror about 2
registerBisector5Locks   : bisectorMid(5;6,7)                      # equal radii at 5
                           phiZeroFollower(6,5), phiZeroCenter(7,5) # coplanarity (5,6,7)
registerNode9MirrorLocks : mirror(9,6,2), mirror(9,2,6)            # 6/2 mirror about 9
registerBisector11Locks  : bisectorMid(11;10,6)                    # equal radii at 11
                           phiZeroFollower(10,11), phiZeroFollower(6,11) # coplanarity (10,11,6)
registerNode1MirrorLocks : mirror(1,9,10), mirror(1,10,9)          # 9/10 rotation about 1
```

No load-time seeding — saved positions stand; locks fire only on a drag.

## 3. Propagation (applyLocks, lock.go)

A drag calls `applyLocks(movedID, fromDrag=true)`. It is a BFS from the moved node with a
**move-once guard** (`processed` set): each node is written at most once, which is the
ad-hoc conflict resolution — first lock to write a node wins, the rest are shadowed. Order
inside the BFS per node: θ-locks → φ=0 locks → bisector locks. Plus two pre-passes before
the BFS:

- **Meridian-carry pre-pass** (the `if fromDrag && len(bisectorMidLocks)==0` block):
  dragging a φ=0 *written* node carries the whole φ=0 trio perpendicular onto the dragged
  node's meridian. From commit f58def86. **Gated OFF whenever any bisectorMidLock exists**
  (i.e. the live topology), so currently inert there; still live for old-model fixtures.
- **Dragged-mid bisector pre-pass**: dragging a bisector Mid projects it back onto its
  bisector plane.

## 4. The coplanarity parts (polar-ish, φ=0 meridian)

Coplanarity comes from `phiZeroLock`. The φ=0 meridian plane has normal = the φ=90° axis
`polar2cart{R:1, θ:π/2, φ:π/2}` (a fixed direction). The lock projects the written node
onto the kept node's meridian plane by **dropping the off-plane component**:

```
v := written − kept
v  = v − perp·(v·perp)   // drop component along the φ=90° axis
nw := kept + v
```

Chaining these (5 onto 6's plane, 7 onto 5's plane, etc.) makes the cluster share the
perp-coordinate → coplanar. Registered for {6,5},{7,5} and {10,11},{6,11}. The off-plane
drop is itself a **Cartesian** operation (dot/scale on vec3), even though it stands in for
"φ-plane."

## 5. The Cartesian parts (what the double-link model is meant to remove)

These are the operations that reach into world x/y/z because the constraint spans nodes
with no shared polar frame:

1. **`bisectorProject(mw, aw, bw)`** (lock.go:128) — projects a mid onto the perpendicular-
   bisector PLANE of its feeders: `mw − n·((mw−midpoint)·n)`, `n = (bw−aw)/|bw−aw|`. Pure
   Cartesian. This is the equal-radii behavior. Used in both the dragged-mid pre-pass and
   the BFS bisector loop.

2. **Off-plane drop** (lock.go:454, §4 above) — `v − perp·(v·perp)`. Cartesian projection
   onto the meridian plane.

3. **Co-sphere radius coupling** (node_move.go:635, inside `RootMove`, NOT a declared
   lock) — dragging a surface node resizes its sphere; every other surface node of that
   center moves radially to the new radius (`cw + dir.normalize()·newR`). This is the
   hidden link that made node 11 drag node 5. Now excludes `bisectorMid` nodes on both
   sides (the patch), but the coupling itself is still imperative Cartesian.

4. **`rescaleAboutMid(mw, nw, refR)`** (lock.go:141) + **`equalRadiiAdjust`** (lock.go:159)
   — rescale a node about a mid to a reference radius, keeping direction. The equal-radii
   fold for the OLD chain. Cartesian (sub/normalize/scale). Now largely inert (no
   equalRadiiLock registered) but still referenced by the node-3 flip path.

5. **Node-3 authority flip** (lock.go:386, `if movedID == "3"`) — special case: node 3
   drives node 7's radius, node 6 follows via `rescaleAboutMid`. Hand-folded composition
   that a uniform mechanism wouldn't need. Inert now (depends on equalRadiiLock) but still
   in the code.

6. **Meridian-carry pre-pass** (lock.go, §3) — `perp.scale(targetPerp − ow·perp)` shifts
   the trio along the perp axis. Cartesian; gated off in the live model.

## 6. Behaviors this currently produces (the target to preserve)

- Node 5 equidistant from feeders 6,7 (bisector) + coplanar with them (φ=0). Feeders free.
- Node 11 equidistant from feeders 10,6 + coplanar. Node 11 moving does NOT drag node 5
  (the shared-feeder isolation patch).
- 3↔7 mirror about node 2; 6↔2 mirror about node 9; 9↔10 mirror about node 1 (this last
  is what makes 9 and 10 **swap** — φ flips sign).

## 7. Why the rewrite

Items 1–6 in §5 are all Cartesian or special-cased because the constraint had no direct
movement link to ride on. The double-link model gives every constrained pair a link so
each becomes a polar (R/θ/φ) equation, the locks chain, and §5 disappears.
</content>
