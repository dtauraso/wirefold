# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-02 — task/spec-go-backend-ts-frontend, pushed, docs only)

- Active branch: `task/spec-go-backend-ts-frontend`, pushed to origin. DOCS/SPEC ONLY — no Go/TS/substrate code changed. Many commits this session refining the spec.
- Two HTML specs under `docs/`: `docs/clock-dialog/index.html` and `docs/go-authoritative-clock/index.html`. The latter is now an 11-tab spec: Overview, Division, The Bridge, Clock Change, Contract, Play/Pause, TS → Go, Doc Updates, Plan, Phases, Verify.
- Target model (NOT implemented — proposal): Go backend owns ALL world-space geometry (nodes, ports, edges endpoints+curves, pulses), shading params, and animation data; runs ONE clock; computes every bead position and emits a position stream. Node/wire loops sleep ~16ms (report rate only); bead speed = pulseSpeed (uniform); arrival = inFlightTime. TS frontend runs the GPU, applies Go's data to the scene, and sends CRUD edits back. Zustand authoritative store removed; the per-frame path already bypasses React via imperative buffers (pulse-state.ts, edge-curve map).
- Bridge = plain CRUD, no timing handshake: position+geometry+shading data OUT; editor geometry+animation edits + pause/resume IN. TS→Go is FIRE-AND-FORGET — no await/Promises, no request/response; optimistic local apply covers responsiveness.
- Human-viewing speed belongs to Go: Go runs the animation at a human pace; TS CRUD edits update the animation at that same pace; TS never sets timing. This works because tempo (human speed) ≫ bridge latency, so loose/eventually-consistent coupling is correct and ~20ms round-trips are sub-perceptual.
- Coding plan (Phases tab): Phase 1 clock into Go (scoped to the in08→readGate1 slice; park other topology.json items under an ignored `_parked` key since JSON has no comments; readGate1 won't fire yet — goal is delivery into its slot); Phase 2 position stream + delete TS position/timing math; Phase 3 geometry data into Go; Phase 4 shading data into Go; Phase 5 bridge→CRUD + finalize MODEL.md/CLAUDE.md.
- Substrate-model amendment, not yet implemented: deletes the TS-triggered delivery (`notifyDelivered`) trigger. NOTE: `NotifyDelivered` (the Go function) may be reused, rewritten, OR removed — TBD in Phase 1; the fixed thing is the delivery CONTRACT (bead enters slot on traversal-complete if empty, else holds = backpressure; deleted wire never delivers), not the function. MODEL.md remains current for existing code until Phase 1 lands; the spec's Doc Updates tab lists the MODEL.md + CLAUDE.md sections to change.
- deleteEdge must be rewritten (Reset-only is insufficient: must atomically cancel Go's clock-delivery AND emit a pulse-cancelled echo) and folded into the generic CRUD path (Phase 5). Go's inbound reader stays live during pause (pause freezes the clock, not message intake).
- Key finding: the geometry/animation math the plan moves to Go ALREADY exists in TS, partly duplicated (geometry-helpers.ts rfArcLength mirrors Go BezierArcLength; store.ts moveNode re-derives arc length+simLatencyMs; scene-content.tsx PulseBead computes getPointAt per frame). Implementation is largely DELETING TS duplicates. Go already computes arc length at load (loader.go:191, BezierArcLength in curve_params.go) — it does NOT depend on TS for it.
- Honest risk assessment (from a devil's-advocate pass): strongest value is Phase 1–2 (clock + position stream). Phase 3–4 (all geometry/shading into Go) are higher-cost/lower-payoff and deserve a "do we need this?" gate — shading-data-in-Go especially couples Go to Three.js's material model. Structural risks: the Phase-1 dual-clock intermediate (Go self-delivers while TS still animates) may look broken mid-migration; Phase 1 touches the most-rewritten delivery/backpressure code. Camera/screen-space: camera CAN be a Go object, but render the pose LOCALLY always (a Go round-trip can't drive a smooth 60Hz orbit reliably under CPU load); screen-space derivatives (labels/picking) can live in Go and fall back to TS if they lag — judged an easy, isolated, reversible fix, intentionally NOT documented in the spec.
- Uncommitted: only `topology.json` (editor scratch), deliberately untouched. The two HTML specs live under `docs/` (not `docs/planning/`), so they are NOT branch-local-stripped and ride a merge.

### What is on main (recent work, newest first)

1. **Glass node bodies + init-value pulse beads.** Node body now renders as a thin-shell glass material (`meshPhysicalMaterial` with `transmission`), lit by a procedural offline PMREM environment built once via `EnvTexContext` — applied per-node-material only, with NO global `scene.environment` set. Input nodes additionally render their `data.init` values as small pulse beads on the node center plane: each value maps to a component via `INIT_PULSE_COMPONENTS` (0 = white sphere + black torus ring, 1 = black sphere + black ring, any other value = `DefaultPulseBead`); all beads share a single `StyledPulseBead`. Data path: init lives at `node.data.nodeData.init` (populated in `spec-to-flow.ts`). All in `src/webview/three/scene-content.tsx`. Merged from `task/glass-node-only` (merge commit 251ffb5a).
2. **Dead slot trace event removed.** `KindSlot` removed from `Trace/Trace.go` (`TraceEventKinds`, `Slot()` emitter, marshal case). `SlotEvent` union member + `pump.ts` `case "slot": return;` no-op removed from TS. Marshal contract test entries and `test/fixtures/trace-events.jsonl` slot fixture removed. Stray `runCommand.ts` slot guard removed. Merged from `task/remove-slot-trace-event` (4195742a), merge commit 03a75e5b. **NOTE: this CORRECTS the prior handoff's stale claim that `slot` was a "LIVE Go trace kind that still flows"** — verification showed `Trace.Slot()` was never called anywhere (dead emitter, no-op consumer). The runtime slot/backpressure concept in `paced_wire.go` is entirely separate and was untouched.
3. **DEFAULT_EDGE_KIND constant.** `flow-to-spec.ts:74` unguarded `"signal"` edge-kind literal is now `DEFAULT_EDGE_KIND` in `src/schema/types.ts` (typed as `EdgeKind`, validated against the `EDGE_KINDS` union at compile time); imported into `flow-to-spec.ts`. Merged from `task/default-edge-kind-const` (e46ebd17), merge commit b40fff69.
4. **Level-4 audit site.** `docs/level4-audit/index.html` — self-contained offline HTML report (horizontal tabs, inline SVG diagrams, 3 findings each with evidence + a proposed-solution block, a "what's healthy" page, leverage-ranked recs). Leverage axis = AI re-derivation cost.
5. **F1 — stale docs re-anchored.** CLAUDE.md + MODEL.md described the RETIRED React Flow architecture as current. Re-anchored to the live `three/` reality: node rendering is generic via `GraphNode` in `scene-content.tsx` (reads `node.data.fill`/`stroke` from `NODE_DEFS`); there are NO per-kind `<Kind>Node.tsx` files and no `rf/` dir; `NODE_DEFS` in `src/schema/node-defs.ts` is the single registry (no `registry.ts`); pump is at `webview/three/pump.ts`. Added `scripts/check-dead-doc-tokens.sh` (tokens: `rf/nodes`, `GenericNode`, `PUMP_SLOT_HANDLER`, `webview/schema/`, `webview/rf/`) wired into `scripts/stop-checks.sh` so these docs cannot silently rot again.
6. **F2 — silent-failure duplication closed.** Extracted `NODE_DIM_FALLBACK = {width:110,height:60}` to `src/schema/node-dims.ts` (neutral layer; `src/webview/state/node-dims.ts` is now a re-export shim) and replaced all 110/60 literal fallbacks across spec-to-flow, geometry-helpers, interaction-controls, node-types. `node-override-text.ts` handled kind names ("Input","ChainInhibitor") are now a compile-checked subset of `NODE_DEFS` keys (renaming a kind breaks tsc instead of silently returning ""). Trace-kind exhaustiveness was already enforced via assertNever in pump.ts.
7. **F3 — SendRule made structural.** Added `ParseSendRule(string) (SendRule, error)` in `nodes/Wiring/ports.go` and a parse-time rejection in `nodes/Wiring/validate.go` (Check 4): an invalid `data.sendRules` value is now REJECTED at load instead of silently degrading to consumeGated. loader.go uses ParseSendRule too. Tests in ports_test.go.
8. **Dead slot badge removed.** Deleted unused `SlotEntry`/`SlotMap` types from `messages.ts` (the old badge-render state, consumed by nothing). This was the precursor cleanup; the full slot trace removal landed in item 2 above.

### Process note (worth keeping)

The re-audit (re-running the SAME audit after the fix) caught that the F1 fix was itself incomplete: `MODEL.md:5` still said `webview/rf/pump.ts`, and the first version of the dead-token guard only checked `rf/nodes`, so it would NOT have caught it. Adding `webview/rf/` to the guard + re-auditing closed it. Lesson: re-run the audit post-fix; a guard must cover the token that actually appeared, not a near-miss.

### OPEN ITEMS / NEXT

1. **Decide the spec branch's fate** — merge `task/spec-go-backend-ts-frontend` to main (specs ride along) or keep as a pushed planning reference. No code depends on it.
2. **Scope gate on Phases 3–4** before any implementation — confirm geometry-into-Go and especially shading-into-Go are worth the cost/coupling, or trim the plan to Phase 1–2.
3. **If implementing:** Phase 1 first concrete step = Go computes arc length (it already can) and elapses inFlightTime itself, delivering on its own clock (replacing the notifyDelivered trigger), proven by a HEADLESS Go test on the in08→readGate1 slice (today `go run .` deadlocks after the first hop). Park other topology items. Whether `NotifyDelivered` the function survives is decided here. Update MODEL.md + CLAUDE.md per the spec's Doc Updates tab in the SAME commit as code.
4. Carry-over: `task/partial-feature-audit` (findings-only) noted as starting in a prior handoff — status unknown, not this session.
5. `session-log.md` still has dated React-Flow refs — left intentionally as historical record.

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
