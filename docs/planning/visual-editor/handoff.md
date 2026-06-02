# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-01 — no task branch in flight; all merged to main)

- Active branch: none. All task branches merged. `main` is current (commit a814ba60). Glass node-body rendering + init-value pulse beads (from `task/glass-node-only`) are on main — see "What is on main" item 1. Since that merge: housekeeping only — superseded `task/glass-node-surface` branch deleted (local + remote); `/gen-node-defs` build artifact added to root `.gitignore` (commit 422cc1f2) so it stops showing as untracked. No code/substrate changes; no task branch in flight; branches clean.
- Build/test gate GREEN on `main`: `go build ./...`, `go test ./...`, webview `npx tsc --noEmit` + `npm run build`, `tools/check-generated.sh` (no diff), and `scripts/check-dead-doc-tokens.sh` all pass (green at last merge; nothing has changed since).
- Uncommitted: only `topology.json` remains modified (editor scratch), deliberately untouched.

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

1. **No task in flight.** Friction-driven from here. (Housekeeping: `/gen-node-defs` build artifact is now gitignored at the repo root — commit 422cc1f2 — so it no longer shows as untracked.)
2. **`task/partial-feature-audit` starting.** New audit branch to inventory other partially-done features across the codebase — findings-only pass, no fixes on that branch.
3. **`session-log.md`** still has dead React-Flow line references (app.tsx, AnimatedEdge.tsx) — left intentionally; it's a dated historical snapshot, rewriting it would falsify the record.

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
