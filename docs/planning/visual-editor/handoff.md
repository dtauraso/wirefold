# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-01 — no task branch in flight; all work merged to main)

- **Active branch:** none. `main` is current; last work merged was per-edge travel-time + 3D port-curve.
- Build/test gate GREEN on `main`: `go build ./...`, `go test -count=1 ./nodes/...`, `-race` on `nodes/Wiring`, and `tools/check-generated.sh` all pass.

### What is on main (recent substrate work, newest first)

1. **Per-edge travel-time (fan-in fix).** Travel-time (`ArcLength`/`SimLatencyMs`) is now carried per-edge on the source `Out`, not on the shared per-port `PacedWire`. A fan-in input port (two edges → one input) times each feeder by its own curve. The port keeps the slot + backpressure and a `MaxIncomingSimLatencyMs` aggregate used only by the coincidence window `W`. Only fan-in today: `readGate1.FromChainInhibitor` ← `i1.ToNext1` + `bootstrap_rg.ToReadGate` (one-shot seed). In-flight state is still shared per port (NOT split) — fine while the only fan-in feeder is a one-shot seed; per-edge in-flight lanes are the follow-up if simultaneous fan-in beads ever appear.
2. **3D port-to-port arc length.** Go computes edge arc length over the SAME 3D port-to-port curve the renderer draws (`port_geometry.go` mirrors TS `portDir`/`nodeRadius`/`portWorldPos`; `PortCurveArcLength` integrates the 3D Bézier incl. dz). All magic numbers are shared `CurveParam*` constants codegen'd into `curve-params.ts`; per-kind node dims come from `node_dims_gen.go` (codegen from each kind's SPEC.md `## View`). Result: every edge's pulse animates at the uniform `0.08` wu/ms (was per-edge-variable because Go measured center-to-center 2D while the bead rode port-to-port 3D).
3. **ChainInhibitor poll-and-hold backpressure.** `ChainInhibitor` holds its input until every output wire is fully empty (`Out.Occupied()` = in-flight OR parked-unconsumed slot), instead of silently dropping output pulses when a wire is busy. One pulse per wire, lossless, no deadlock, survives mid-pulse edge delete + re-add. `PacedWire.Occupied()` / `Out.Occupied()` added.
4. **Timing window (coincidence rule).** Nodes with ≥2 input edges run a window: `t0` = first input; all inputs within `W` → fire; else clear all. `W = 1.5 × max(input edge SimLatencyMs)`. On `InhibitRightGate` and `ReadGate`. Uses non-blocking `PollRecv`.

### OPEN ITEMS / NEXT

1. **No task in flight.** Next work is friction-driven from live editor use — drive the editor, narrate, log to session-log.md, and branch when a concrete change is identified.
2. **InhibitRightGate window logic not independently verified.** The fan-in/feeder fixes resolved `inhibitRight0`'s starvation, but the gate's own coincidence fire/clear decisions were never validated against actual input alignment (only that it's no longer starved and the ring is live). Open thread if its timing behavior is ever in question.
3. **Per-node dimensions seam.** Width/height are kind-uniform (codegen from SPEC.md), not serialized per-node. Go and TS both trace to SPEC.md but via different runtime tables. If per-node resizing ever lands, that's the seam to revisit (Go would need per-node dims, not kind dims).

### Substrate model contract (stable)

See [MODEL.md](../../../MODEL.md#slot-phase-lifecycle). One `PacedWire` per destination input port (slot + backpressure). Send rules are node-owned (`consumeGated` / `fireAndForget`). Travel-time is per-edge (on `Out`); the wire holds `MaxIncomingSimLatencyMs` for `W`. `pump.ts` stays render-only.

## Dev-loop

After TS edit: `npm run build` from `tools/topology-vscode/`.
After Go change: `go build ./...` from repo root, `go test ./nodes/...`. After any change to shared `CurveParam*` constants or SPEC.md `## View`, regenerate and run `tools/check-generated.sh`.
To repro / inspect: clear `.probe/*.jsonl`, reload window in VS Code, Run once, inspect `go.jsonl` / `ts.jsonl` breadcrumbs.

Check: `go test ./...`. All guard scripts run via the Stop hook (`scripts/stop-checks.sh`). Bash approval guard runs via PreToolUse.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt
tailored to the state you're leaving the branch in, and commit on the
active branch (main if no task is in flight). Do not rely on chat
history; the next AI may be a fresh model with no transcript. The
rendered handoff must itself contain this same ALWAYS clause so the
loop is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as
the structural source of truth; update the template when an invariant
changes.
