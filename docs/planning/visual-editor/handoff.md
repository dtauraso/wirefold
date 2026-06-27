---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## Active branch: `main` is current

The `task/theta-lock-diag` keepers landed on `main` (polar frame markers + labels in
NavGuides.tsx; Go-owned scene-tori "rings" toggle) and the throwaway diagnostic was
dropped. **FINDING (kept for context):** the reported "persistent θ mismatch between 3
and 7" was NOT a θ bug — nodes 3 & 7 share θ exactly and differ only in φ; the apparent
mismatch is the φ separation seen through a TILTED camera (screen-up ≠ world +y), hence
the pole markers. Still untested: dragging the FOLLOWER (node 7) — every captured drag
was the leader only. Open question for David: do 3/7 want a φ relationship (mirror φ for
symmetry vs current free φ)?

Start new work from `main`.

## What shipped (recent, all on `main`)

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

1. **Pre-existing `MoveDispatch` data race** (under `-race`): `heldCenters`/`applyLocks`
   (node_move.go:~455, lock.go) reads `centers` concurrently with a `nodeMover.handle`
   write (node_move.go:~129). Not caught by stop-checks (no `-race`). Its own fix branch.
2. **Stop emitting `-1` placeholders at the source** — Pulse + HoldFlip drive only when
   `held != -1`, so wires carry only real beads (cleans the whole chain).
3. **node 4 continuous-pulse → event-driven output** (optional; the flood to node 5 is
   why node 5 needed the `-1`/latest fixes).
4. **Layout pole singularity** (φ blow-up near +y) — apply the epsilon-free bearing form to
   the θ-lock if the pole behavior needs to be stable.
5. **Test θ-lock follower drag** — does dragging node 7 (the follower) fire the lock?
   Untested; any real θ-lock bug would live there.

### Verify (NEVER run the sim)
`go build ./... && go test ./...`, then
`cd tools/topology-vscode && npx tsc --noEmit && rm -f out/webview.js && npm run build`,
then from the repo root `bash scripts/stop-checks.sh` (must exit 0 — it runs the guard
suite incl. message-kind-parity, polar-only-nav, no-camera-roundtrip). Editor opens via
the VS Code command; an extension-host change needs Developer: Reload Window (reopening a
file only reloads the webview). Runtime logs are `.probe/*.jsonl` (check freshness). Git:
run from repo root (`/Users/David/Documents/github/wirefold`).
