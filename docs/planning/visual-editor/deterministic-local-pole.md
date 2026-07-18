---
branch: task/deterministic-local-pole
---

# Deterministic local pole — pole as a pure function of geometry

## Why

The rotating per-node local pole (merge `085e3094`) made the local-polar bearing
quantization well-conditioned by *moving* each node's measurement pole to dodge the
azimuth singularity, and *persisting* that pole as per-node state. That single decision
(the pole is mutable, stored, shared) is the root of every hard bug this session:

- a cross-goroutine RMW race on the pole/bearings (fixed `669faab2`),
- a blocking-send deadlock from decentralizing the pole update (fixed `386501f8`),
- a self-send deadlock in the gate cascade (fixed `c83e8609`),
- a flaky test racing the async pole kick,
- non-convergence of the kick loop, and non-deterministic map-order tie-breaks.

All of it is machinery to keep a *moving* reference frame in lockstep with the bearings
measured against it — a frame-consistency invariant over separately-stored, separately-
updated, cross-goroutine state, plus a circular pole↔bearings fixed-point solve.

## The model (agreed)

**The pole is not state. It is a pure, deterministic function of the current geometry:**

```
pole = localPole(current neighbour offset directions)
```

- **Home** is world `+y` (`dir{Theta:0, Phi:0}`), the same fixed convention scene-polar
  uses. Away from the singularity the pole IS home.
- When the offset **closest to `+y`** falls inside the singular zone (colatitude
  `< poleKickTheta`, 20°), the pole **dodges a little** — tilted away from that offset
  by exactly `poleKickTheta - c`, so the offset lands at colatitude `poleKickTheta` (just
  clear of the zone). A *small* dodge (≤20°), not the old kick-to-equator (90°).
- **One closed-form step**, computed from the incoming angle — no iteration, no
  convergence question, no map-order tie-break, no free axis knob.
- The dodge is a function of position, so it does not matter how long a node sits at the
  singularity: the pole is dodged for exactly as long as the node is there and returns to
  home when it leaves. "Settle back" is just what `localPole` evaluates to once no offset
  is near `+y` — it is not a temporal process and nothing has to unwind.

Because `pole = f(geometry)` and node positions are the authoritative persisted state
(`scenePolar`), the pole is **recomputed on demand and never persisted, never carried in
a message, never stored on the holder.** On reload it is recomputed from loaded
positions and yields the identical pole the runtime used (both evaluate the same `f` on
the same geometry) — which also closes the old load-vs-runtime pole divergence.

### The function

```go
// localPole returns the deterministic measurement pole for a node whose neighbour offset
// directions are `dirs`. Home is world +y; when the offset nearest +y is inside the
// singular zone it tilts a little away from that offset so the offset clears the zone.
// Pure: no state, no I/O, no iteration. Deterministic tie-break (Theta then Phi) so it
// never depends on map order.
func localPole(dirs []dir) dir {
    home := dir{Theta: 0, Phi: 0}          // world +y
    // colatitude of an offset about +y is just its Theta.
    minC := math.Pi
    var closest dir
    found := false
    for _, d := range dirs {
        if !found || d.Theta < minC || (d.Theta == minC && d.Phi < closest.Phi) {
            minC, closest, found = d.Theta, d, true
        }
    }
    if !found || minC >= poleKickTheta {
        return home
    }
    // Tilt home away from `closest` along the geodesic through them, by just enough to put
    // `closest` at colatitude poleKickTheta about the new pole. arcBetween/rotateDir are
    // pole-safe (atan2 of unnormalised terms), so minC≈0 resolves finite: the dodge
    // direction is arbitrary there but the RESULT (closest at poleKickTheta) is well-
    // conditioned regardless, because it is a single step, not an iterated one.
    return rotateDir(home, arcBetween(closest, home).Axis, poleKickTheta-minC)
}
```

## What this deletes vs keeps

**Delete (the moving-pole apparatus):**
- `rotating_pole.go`: `resolveLocalPole`, `kickPoleAwayFrom`, `initLocalPole`, `meanDir`,
  `maxPoleKicks`. Keep `dirFromOffset` and `poleKickTheta`. Add `localPole`.
- `layout_holder.go`: the `localPole` / `hasLocalPole` fields and `SetLocalPole` /
  `LocalPole` methods — the pole is no longer held.
- `quant_offset_persist.go`: `WriteLocalPolarsAndPole` stops writing `localPoleTheta` /
  `localPolePhi` (drop the `pole`/`hasPole` params; rename if natural).
- `loader.go` / `loader_tree.go`: `LocalPoleTheta` / `LocalPolePhi` spec fields and their
  parsing; `computeLocalPolars`' persisted-pole seed + `resolveLocalPole` call.
- `topology/nodes/*/meta.json`: `localPoleTheta` / `localPolePhi` keys (now unread —
  remove them so the on-disk fixtures match the schema).

**Keep, unchanged in behaviour:**
- The **radius** quant (`QuantIR`) and its placement consumers (`equalizeEdgeCLocal`) —
  untouched; radius is measured from distance, independent of the pole.
- The requantize **message cascade** (`moveMsgKindRequantize` / `RequantizeSetC`) and its
  non-blocking `sendMoveLossy` delivery — those messages never carried pole data, they
  tell a neighbour "X moved, refresh your offset to X". Still needed for the radius.
- The bearing (`QuantITheta` / `QuantIPhi`) storage slots — still computed and stored as a
  cache, now about the deterministic pole; recomputed from live geometry on load.

## The one real refactor: source the pole from live geometry

`requantizeLocalPolarsAboutPole` (and `computeLocalPolars`) currently reconstruct each
*unchanged* neighbour's direction from its stored quant about the *old stored pole*. With
`pole = f(geometry)` there is no stored pole to reconstruct against. Change both to gather
**all** of the node's neighbours' **live** offset directions (via `centerOfNode`), compute
`pole := localPole(dirs)`, then quantise every neighbour's live offset about it. This also
removes the lossy quantize→dequantize→requantize round-trip the audit flagged.

Consequence for the decentralised path: when neighbour M refreshes its offset to a moved X
(`requantizeNeighborSelf` / `...SetC`), M must gather M's **whole** neighbour set's live
centres (not just X's) to evaluate `localPole`, since the pole depends on the closest of
all M's offsets. M's own goroutine already has `centerOfNode` + the edge adjacency; expand
the handler to collect them.

## Verify

- `go build ./... && go test -race -count=1 ./nodes/Wiring/`.
- `bash scripts/stop-checks.sh` clean (read stdout).
- Determinism: `localPole` is a pure function; add a unit test that the same geometry
  yields the same pole across runs, that an offset just outside the zone gives exactly
  home, and that one just inside gives a pole placing it at `poleKickTheta` (continuity
  across the threshold). The old flaky `TestRotatingPoleKicksAwayFromOffset` should be
  replaced/retargeted at the new function (no async, no drag needed to assert the pole).
- Reload identity: a drag-then-reload lands the same bearings the runtime produced (both
  evaluate `localPole` on the same geometry).

## Out of scope (still open, do not do here)

Whether the quantised **bearing** has any real output consumer. This spec keeps the
bearing (well-conditioned, cheaply) but does not add or assume a consumer. If it is later
confirmed dead, the bearing quant + `localPole` can both be deleted down to the radius —
a separate decision.
