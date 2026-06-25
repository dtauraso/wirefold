---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## Active branch: `task/theta-lock-diag` (in flight) — else `main` is current

This branch now carries BOTH keepers and a throwaway diagnostic. **Before merging, split
the keepers out and drop the diagnostic logging** (it has served its purpose — see below).

**KEEPERS (NavGuides.tsx + a Go-owned toggle):**
- **Polar frame markers + labels.** Three camera-independent colored axes at the
  content-sphere center showing the layout's WORLD frame: **+y pole (green)**, **+x =
  refX/φ0 (red)**, **+z = φ90 (blue)** (three.js X=red/Y=green/Z=blue), each with a
  billboard sprite label ("+Y pole" / "+X φ0" / "+Z φ90", canvas-texture via `AxisLabel`,
  always faces camera). Decorative (raycast off). They exist because "up" in the layout is
  world +y, NOT the screen's up — see the finding below.
- **Go-owned scene-tori show/hide toggle.** Toolbar **"rings"** button. Go owns the bool
  (`MoveDispatch.sceneToriVisible`, default true); a `tori-vis` edit op toggles it
  (stdin_reader.go → `ToggleSceneTori`) and Go streams it via a new `scene-tori` trace
  event (Trace.go). TS only ASKS Go to toggle and reflects Go's streamed value (in the
  EXISTING camera-store — see "no store" rule). Hides ONLY the two tori; handholds and the
  +y/+x/+z markers stay visible.

**THROWAWAY DIAGNOSTIC (lock.go + node_move.go — REMOVE before merge):**
- `theta_lock` breadcrumb in `applyLocks` (tautological — re-reads what it just set).
- `pair_theta` breadcrumb after EVERY `RootMove` (`logPairTheta`): `moved=<n> th3=… th7=…
  d=…`. NOTE: keep the `tr *T.Trace` field on `MoveDispatch` (the toggle keeper needs it);
  remove only the breadcrumb calls + `logPairTheta`.

**FINDING — the reported "persistent θ mismatch between 3 and 7" is NOT a θ bug.**
Measured three independent ways on fresh logs — the θ-lock roots (`pair_theta` d=0.0000
over all moves), the emitted node-geometry (final θ3=θ7), and node 2's port directions
(`ToNext0`/`ToNext1` both θ=67.8°). **Nodes 3 & 7 (and edges 2→3 / 2→7) share θ exactly;
they differ only in φ (azimuth, ~171° apart).** The apparent "different θ" in the editor
is the φ separation seen through a TILTED camera (screen-up ≠ world +y) — hence the pole
markers. Also: every captured drag was `moved=3` only — **node 7 (the follower) was never
dragged**, so "does dragging the follower fire the lock?" is still UNTESTED (any real bug
would live there). The θ-lock holds when the leader is dragged. Open question for David:
do 3/7 want a φ relationship too (mirror φ for symmetry vs current free φ)?

All other work this session is **merged to `main`**. Start unrelated new work from `main`.

## What shipped (this session, all on `main`)

**Viewpoint nav math moved TS → Go.** The camera is now Go-owned and POLAR. Go holds the
state `(pivot, r, pos, up)` (`nodes/Wiring/viewpoint.go`) and does angle-only spherical
trig (`spherical.go`: `dir{θ,φ}`, `rot{axis,angle}`, `rotateDir`/`arcBetween`/
`angleAboutAxis`). The trig is **epsilon-free** — it uses the great-circle bearing form
(`atan2` of two unnormalized terms), so there is NO `/sinθ` and no pole special-case in
the camera math. All four gestures route through Go: an `edit` op `viewpoint`
(`kind` = set/orbit/orbit-locked/zoom/pan, stdin_reader.go) in, a `camera` trace event
(Trace.go) out. TS does only the two edge conversions (pointer px→angles in;
polar→three.js out, where three.js builds the quaternion at draw) — `viewpoint-bridge.ts`,
`CameraFromStore.tsx`. Camera persists as `cameraPolar` in scene.json. The ONLY Cartesian
in Go is the `pivot` world point (translated, never rotated). Zoom-to-cursor is a dolly =
a pan (`pan` op), not a radius change. Empty-space rotation and handhold (locked-disk)
rotation both go through Go; the handhold disk is anchored at the grab point.

**Pick resolution** resolves nodes by `userData.nodeId`/`body` tags across all pick paths
(default/nodesOnly/portOnly); the z-blind x/y-proximity fallback is gone; handholds are
excluded from node picks.

**Port-move** projects the pointer onto the node's own ring plane (`z = nodeCenter.z`),
not a fixed `z=0` (fixes node 8 / any node off z=0).

**Dynamic port auto-aim** (`AimedPortRegistry`, `aimed_ports.go` + loader.go): for edges
1→2, 1→6, 1→8, 2→3, 2→7 the source port aims at its child and the child's input aims back,
so those edges are radial spokes — applied from LOAD (loader.go initial edge geometry uses
`segmentBetweenPortsAimed`), not just after a move. Node 8's `FeedbackOut` (8→1) stays
ring-anchored and manually movable.

**θ-lock** (`thetaLock`, lock.go): nodes 2 & 6 on node 1, and nodes 3 & 7 on node 2, share
θ (angle from the center's +y up-pole) while each keeps its own φ — same latitude ring.
Registered by id in loader.go (stopgap, like the chord lock).

**Node kind `Excitatory` → `Pulse`** (pure rename; package `nodes/pulse`, regenerated
NODE_DEFS / kinds_generated / node_dims; topology nodes 6,7 are type `Pulse`).

**HoldFlip (node 4)** mirrors Pulse: a main loop drains input to the LATEST value and
updates the interior bead immediately; a drive goroutine continuously pulses the flip
(`1-held`). So node 4 tracks upstream's current value with no display lag. *Behavior
change:* output is continuous-drive (not one-per-input), and before any input it drives
the `-1` empty placeholder.

**WindowAndGate (node 5)** discards `-1` "no value" placeholders (only a real value fills
a slot) and re-samples each side to the most-recent real bead, so each slot shows the
current bead.

**Trace** serializes stdout writes: `drain()` holds the mutex across the sink write so
event and breadcrumb writes can't interleave into garbled lines (those garbled lines were
leaking into the VS Code "topology run" Output channel).

## Carry-forward (hard-won — READ THIS)

- **`-1` is the "no value / empty" sentinel.** It displays as nothing (correct) and should
  ideally never be a bead on a wire. But Pulse and HoldFlip currently DRIVE `-1`
  continuously before they hold a real value, polluting downstream (node 5 had to learn to
  ignore `-1`). Open: stop emitting `-1` at the source so wires carry only real beads.
- **The (θ,φ) chart is singular at the +y pole in the LAYOUT.** `surfaceCoord`→`cart2polar`
  snaps φ to 0 on the axis; the θ-lock keeps each node's φ, so near the pole positions get
  unstable (φ blows up). The CAMERA math (spherical.go) already avoids this via the
  bearing form; the layout/lock does NOT yet. If the θ-lock needs a pole fix, mirror the
  bearing-form approach.
- **`.probe/*.jsonl` are written by the LIVE editor run and can be minutes stale.** Check
  freshness (last `ts_ms` vs now) before concluding anything — several diagnoses this
  session were derailed by reading a stale log that didn't contain the live failure.
- **Subagents have committed/pushed despite "do not commit."** Spot-check `git log` and
  amend; force-with-lease on your own just-pushed branch is fine. A subagent also DELETED
  unrelated code (the +y/+x/+z markers) while doing the tori-toggle edit — diff the result.
- **"Don't use a store" (David) = don't create a NEW store/conduit for a streamed bit.**
  Reusing an EXISTING store to REFLECT a Go-streamed value is fine. Go owns the variable;
  TS only asks Go to change it and renders Go's last-streamed value (the scene-tori toggle
  is the pattern). Do not make TS the authority.
- **θ vs φ through a tilted camera.** What looks like a θ (height) difference in the editor
  is often a φ (longitude) difference projected by a rotated camera — screen-up ≠ world +y.
  The polar frame markers exist to make the world frame visible. Measure θ from world +y,
  not from the screen, before claiming a θ bug.
- **NEVER run the sim in the foreground** (it can fail to exit and hang). Background it or
  use `tools/run-bounded.sh`.

## Open / next (friction-driven)

1. **Land `task/theta-lock-diag`** — the θ "mismatch" was diagnosed as φ-through-tilted-
   camera, NOT a θ bug (see Active branch FINDING). Next: split the keepers (markers/labels
   + scene-tori toggle) into a clean branch, DROP the diagnostic logging, merge. Still
   untested: does dragging the FOLLOWER (node 7) fire the lock? And does David want a φ
   relationship on 3/7 (mirror vs free)?
2. **Pre-existing `MoveDispatch` data race** (under `-race`): `heldCenters`/`applyLocks`
   (node_move.go:~455, lock.go) reads `centers` concurrently with a `nodeMover.handle`
   write (node_move.go:~129). Not caught by stop-checks (no `-race`). Its own fix branch.
3. **Stop emitting `-1` placeholders at the source** — Pulse + HoldFlip drive only when
   `held != -1`, so wires carry only real beads (cleans the whole chain).
4. **node 4 continuous-pulse → event-driven output** (optional; the flood to node 5 is
   why node 5 needed the `-1`/latest fixes).
5. **Layout pole singularity** (φ blow-up near +y) — apply the epsilon-free bearing form to
   the θ-lock if the pole behavior needs to be stable.

### Verify (NEVER run the sim)
`go build ./... && go test ./...`, then
`cd tools/topology-vscode && npx tsc --noEmit && rm -f out/webview.js && npm run build`,
then from the repo root `bash scripts/stop-checks.sh` (must exit 0 — it runs the guard
suite incl. message-kind-parity, polar-only-nav, no-camera-roundtrip). Editor opens via
the VS Code command; an extension-host change needs Developer: Reload Window (reopening a
file only reloads the webview). Runtime logs are `.probe/*.jsonl` (check freshness). Git:
run from repo root (`/Users/David/Documents/github/wirefold`).
