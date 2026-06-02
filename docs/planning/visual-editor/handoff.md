# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-01 — no task branch in flight; all work merged to main)

- **Active branch:** none. `main` is current. This session: removed the inert `midpointOffset` wire prop, removed the entire feature audit, and removed the on-screen dolly (▲/▼) buttons.
- Build/test gate GREEN on `main`: `go build ./...`, `go test -count=1 ./nodes/...`, `-race` on `nodes/Wiring`, and `tools/check-generated.sh` all pass. Webview `npx tsc --noEmit` + `npm run build` clean.
- **Uncommitted:** `topology.json` has stood modified (editor scratch graph) across this whole session, deliberately untouched. It still carries dead `midpointOffset` field values (lines ~216/230) that will drop out next time the graph is saved (field no longer in schema).

### What is on main (recent work, newest first)

1. **Dolly buttons removed (task/remove-dolly-buttons, merged + deleted).** The on-screen ▲/▼ dolly-in/out buttons are gone: deleted `DollyButtons` from `camera-ui.tsx` (~80 lines + its hold-to-dolly tick loop), removed the mount/import in `ThreeView.tsx`, and removed the now-unused `sceneCenter` helper from `geometry-helpers.ts`. Wheel/pinch zoom (Ctrl+scroll / trackpad pinch in `interaction-controls.ts`), `⌂ fit` (`HomeButton`), and the labels toggle are all intact.
2. **Feature audit removed entirely.** The whole `docs/planning/visual-editor/feature-audit/` site (`data.js`, `index.html`, `styles.css`, all `features/*.html`) and the `feature-audit.md` redirect stub are deleted; dangling references in `handoff.md` and `session-log.md` were excised. Before deletion, individual entries were removed one-by-one after confirming code reality (`midpointOffset` stub, `edge-kind-context-menu`, `edge-reconnect`, `fit-view-hotkey`, `z-coordinate-node-depth`, `bend-points-waypoints`, `pseudo-panel`). Lesson captured: the audit had TWO layers — `data.js` (card grid) and hand-authored `features/<slug>.html` pages with no generator — so removals needed both; saved as memory `feedback_feature_audit_two_layers`.
3. **`midpointOffset` wire prop removed (task/remove-midpoint-offset, merged + deleted).** Was a fully inert stub. Removed `MidpointOffset` from `nodes/Wiring/loader.go`; `wire-defs.ts` is CODEGEN'd from the Go struct (`go run ./tools/gen-node-defs`), so both TS sites dropped automatically (5 → 4 wire props). The old memory describing it as a separate TS stub was deleted as stale.
4. **Per-edge travel-time (fan-in fix).** Travel-time (`ArcLength`/`SimLatencyMs`) is carried per-edge on the source `Out`, not on the shared per-port `PacedWire`. A fan-in input port times each feeder by its own curve. The port keeps the slot + backpressure and a `MaxIncomingSimLatencyMs` aggregate used only by the coincidence window `W`. Only fan-in today: `readGate1.FromChainInhibitor` ← `i1.ToNext1` + `bootstrap_rg.ToReadGate` (one-shot seed). In-flight state is still shared per port (NOT split) — fine while the only fan-in feeder is a one-shot seed.
5. **3D port-to-port arc length.** Go computes edge arc length over the SAME 3D port-to-port curve the renderer draws (`port_geometry.go` mirrors TS `portDir`/`nodeRadius`/`portWorldPos`; `PortCurveArcLength` integrates the 3D Bézier incl. dz). Magic numbers are shared `CurveParam*` constants codegen'd into `curve-params.ts`; per-kind node dims come from `node_dims_gen.go` (codegen from each kind's SPEC.md `## View`). Every edge's pulse animates at the uniform `0.08` wu/ms.
6. **ChainInhibitor poll-and-hold backpressure.** `ChainInhibitor` holds its input until every output wire is fully empty (`Out.Occupied()` = in-flight OR parked-unconsumed slot). One pulse per wire, lossless, no deadlock, survives mid-pulse edge delete + re-add.
7. **Timing window (coincidence rule).** Nodes with ≥2 input edges run a window: `t0` = first input; all inputs within `W` → fire; else clear all. `W = 1.5 × max(input edge SimLatencyMs)`. On `InhibitRightGate` and `ReadGate`. Uses non-blocking `PollRecv`.

### OPEN ITEMS / NEXT

1. **No task in flight.** Next work is friction-driven from live editor use — drive the editor, narrate, log to session-log.md, and branch when a concrete change is identified.
2. **Feature audit is gone.** If parity/feature tracking is wanted again, it will be a fresh artifact, not the deleted one. The old 2D-RF-view casualties (multi-select, node-delete, node-palette, pseudo-panel, edge-reconnect, edge-kind context menu) and never-built items (multi-node alignment guides) are no longer tracked anywhere except git history.
3. **Per-node dimensions seam.** Width/height are kind-uniform (codegen from SPEC.md), not serialized per-node. If per-node resizing ever lands, that's the seam to revisit (Go would need per-node dims, not kind dims).

### Substrate model contract (stable)

See [MODEL.md](../../../MODEL.md#slot-phase-lifecycle). One `PacedWire` per destination input port (slot + backpressure). Send rules are node-owned (`consumeGated` / `fireAndForget`). Travel-time is per-edge (on `Out`); the wire holds `MaxIncomingSimLatencyMs` for `W`. `pump.ts` stays render-only. Note (re-derived this session): a wire's identity IS its destination port and its slot state lives in the destination node — so edge "reconnect" is not a small feature but a substrate redesign (target-end move = delete+create; source-end move would need a net-new `rewireSource` IPC verb and reworked load-time `SimLatencyMs`). Rejected as not worth the risk to the pulse animation.

## Dev-loop

After TS edit: `npm run build` from `tools/topology-vscode/`.
After Go change: `go build ./...` from repo root, `go test ./nodes/...`. After any change to shared `CurveParam*` constants or SPEC.md `## View`, regenerate and run `tools/check-generated.sh`.
To repro / inspect: clear `.probe/*.jsonl`, reload window in VS Code, Run once, inspect `go.jsonl` / `ts.jsonl` breadcrumbs.
Note: the ring has no headless run — `go run .` builds but deadlocks after the first hop. Delivery is paced by the visual layer (webview pulse-completion → stdin reader → NotifyDelivered); use the live editor to exercise it.
**TS removal/refactor verification:** when removing or refactoring webview TS, run `npx tsc --noEmit` (from `tools/topology-vscode/`) in addition to `npm run build`. esbuild bundles without type-checking, so dangling refs to deleted symbols pass the build and crash at runtime. Captured in memory `feedback_tsc_verify_after_removal`.

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
