# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-02 — task/spec-go-backend-ts-frontend, pushed)

- Active branch: `task/spec-go-backend-ts-frontend`, pushed to origin (tracking set). 6 commits, DOCS/SPEC ONLY — no Go, TS, or substrate code changed. Branch off `main` (was at a814ba60 / 7f223989).
- Work this session: authored two self-contained HTML spec pages under `docs/` — `docs/clock-dialog/index.html` and `docs/go-authoritative-clock/index.html` (8-tab spec: Overview, Division, The Bridge, Clock Change, Contract, Play/Pause, TS → Go, Doc Updates). Dark theme, vanilla-JS tabs, inline SVG diagrams.
- The spec defines a **Go-authoritative model** (NOT yet implemented — it is a target/proposal): Go backend holds ALL geometry, shading, and animation data — nodes, ports, edges (endpoints+curves), pulses, materials/glass/environment params — runs the clock, computes every bead position and emits a position stream. Node and wire loops sleep (~16ms) to set the report rate ONLY; bead speed stays `pulseSpeed` (uniform), arrival stays at `inFlightTime`. TS frontend runs the GPU to display Go's data and forwards editor geometry + animation updates (and pause/resume) back to Go, which updates its data accordingly. Bridge: geometry+shading+position data out; editor geometry+animation updates in.
- This is a **substrate-model amendment**: it deletes `NotifyDelivered` as the delivery trigger (MODEL.md currently pins it as the only cross-boundary signal). NOT implemented yet — MODEL.md remains current for existing code; the spec is the target. The spec's "Doc Updates" tab lists the exact MODEL.md (Driver, Cross-boundary, Geometry/time, Editor-surface) and CLAUDE.md (pump description, bridge surface, drift rule) sections to change in the same commit as any future code.
- Key finding (grounded in a webview inventory): the geometry/animation math the spec moves to Go ALREADY exists in TS, partly duplicated — `geometry-helpers.ts` `rfArcLength` mirrors Go's `BezierArcLength`; `store.ts` `moveNode` re-derives arc length + `simLatencyMs` in TS; `scene-content.tsx` `PulseBead` computes `t` + `curve.getPointAt(t)` per frame. So implementation is largely DELETING TS duplicates and consolidating into Go, not net-new math. This duplication is itself a current drift risk (two arc-length implementations that must agree).
- Build/test gate: unchanged from main (no code touched this session).
- Uncommitted: only `topology.json` (editor scratch), deliberately untouched. Note: the two HTML specs live under `docs/` (not `docs/planning/`), so they are NOT branch-local-stripped and will ride a merge.

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

1. **Decide the spec branch's fate.** Either merge `task/spec-go-backend-ts-frontend` to main (specs ride along), or keep it as a pushed planning reference. No code depends on it.
2. **If implementing the Go-authoritative model:** first concrete step is to make Go compute arc length and elapse `inFlightTime` itself, calling its own delivery — replacing the `NotifyDelivered`-from-TS trigger. Then move bead-position computation + position-stream emit into Go (node/wire loops sleep ~16ms for report rate), and reduce TS to plotting the stream + capturing input + forwarding editor geometry/animation updates. Update MODEL.md + CLAUDE.md (per the spec's Doc Updates tab) in the SAME commit as the code.
3. Carry-over: `task/partial-feature-audit` (inventory partially-done features, findings-only) was noted as starting in the prior handoff — status unknown / not this session.
4. `session-log.md` still has dated React-Flow line refs — left intentionally as historical record.

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
