---
branch: task/architecture-audit
---

# Architecture Audit (2026-05-26)

Architecture is fundamentally sound — codegen-as-sync works, spec/view split is clean, PacedWire is the lone wire primitive, stdin dispatch is one switch. Findings cluster in layer-boundary discipline and a few single-source seams. Discovery was done by three haiku Explore agents; synthesis by Opus.

## Findings (ranked)

1. **requiredInputDiagnostics re-derives substrate deadness in TS — HIGH severity label, but NOT a violation.** `src/schema/parse-spec.ts:46-101` runs a fixpoint over `REQUIRED_INPUTS` to propagate dead-node status — structurally the Go readiness check. BUT memory feedback_enforce_required_inputs (2026-05-25, commit 0e8d843) records this as a deliberate reversal: editor flags missing required inputs (diagnostic + red node + precondition-gating); substrate does NOT reject the graph. REQUIRED_INPUTS is generated from Go AST so it can't go stale; only the fixpoint propagation lives TS-side. Verdict: intended division, not drift. Action: add a comment pinning it as diagnostic-only.

2. **pseudoTable hardcodes 3 of 4 kinds — MEDIUM (silent runtime failure).** `src/extension/handle-message.ts:158-162` is a hand-maintained dispatch table; `src/messages.ts:12` PSEUDO_KIND_PREFIX is its sibling. Adding a new pseudo-editable kind compiles but throws at render/save. The one place that genuinely violates the single-registry principle. InhibitRightGate omission is by-design (not editable) but nothing enforces the table covers exactly the pseudo-editable subset.

3. **computeFade fixpoint pattern — MEDIUM (foothold, not a violation).** `src/webview/three/fade.ts` is render-only and correct, but its fixpoint+cascade shape mimics substrate logic — a template someone could copy into firing logic. Action: add explicit "render-mask only, never reference slot/phase/firing state" comment.

4. **edgeSeeds declared/parsed/validated/serialized but never consumed by Go — MEDIUM, CONTRADICTS MEMORY.** TS pipeline exists (`src/schema/types-graph.ts:39` → `src/webview/state/adapter/flow-to-spec.ts:59`), but probe reports `nodes/Wiring/loader.go` NodeData has no field and reflectBuild never reads it. Yet memory feedback_edge_seed_required_for_rings says ring topologies need edgeSeeds and the Go loader pre-sends it before goroutines start. Direct contradiction between recorded memory and probe finding — must be verified against Go code before trusting either. Flagged, not resolved.

5. **Port-name constants in tools/pseudo/readgate.go:14-19 — LOW.** Must hand-match struct fields in nodes/readgate/node.go; rename drift fails at pseudo-save, not build.

6. **Generated files rely on manual gen:node-defs — LOW.** `build` runs it, but no CI/pre-commit check catches a committed-stale generated file.

## Clean (no action)

Spec/view partition, parseSpec/parseViewerState disjointness, PacedWire primacy, stdin dispatch, trace-kinds/wire-defs/node-defs codegen, pump.ts (pure trace-event translation, zero substrate decisions).

## Next-step candidates

- Verify finding #4 (edgeSeeds) against Go — highest priority, resolves a memory/code contradiction.
- Harden #2 (pseudoTable) — derive the pseudo-editable set or add a type-level exhaustiveness check.
- Add render-only comments for #1 and #3.

---

## Organization-Health Pass (2026-05-26)

Second pass, aimed at organization rather than substrate discipline: file-size hotspots, directory structure, dead code, import cycles, module cohesion. Discovery by three haiku Explore agents; synthesis by Opus. Headline: substrate discipline is excellent; the debt lives in the editor/webview TS layer. Two findings were independently confirmed by separate probes (marked CONVERGENT).

### Findings (ranked)

1. **ThreeView.tsx is a 1,489-line god-object — CRITICAL cohesion debt.** Bundles six unrelated domains: geometry helpers, scene/render components, a 326-line `useInteractionControls` state machine, camera UI widgets (RollSlider/DollyButtons/PanPad), occlusion raycasting, and the main orchestrator. Splits cleanly along existing section boundaries into ~5 modules (geometry-helpers, scene-content, interaction-controls, camera-ui, orchestrator); main component would shrink 239 → ~60 lines.

2. **Backwards layer dependency: spec layer imports from webview layer — HIGH (CONVERGENT).** `src/schema/` is meant to be authoritative spec-side, knowing nothing of the view, but: `schema/node-types.ts:13` imports `NODE_DEFS` from `webview/schema/registry`; `schema/parse-spec.ts:11` imports `REQUIRED_INPUTS` from `webview/schema/node-defs`; `extension/handle-message.ts:23` also reaches into `webview/schema/node-defs`. So `parseSpec()` can't run without view-layer code, and the extension host crosses the postMessage boundary by direct import. Root cause: codegen emits `node-defs.ts` into `webview/schema/` but three layers need it. Fix (one move): relocate generated `node-defs.ts` to `src/schema/`, update codegen output path + imports. Resolves the backwards dep AND the extension violation together. Highest-value correction.

3. **Double-`schema` directory — symptom of #2.** `src/webview/schema/` holds only generated `node-defs.ts` + a `registry.ts` shim. Once #2 moves `node-defs.ts` out, this directory nearly disappears.

4. **Dead code — confirmed orphans.** `webview/state/dimmed.ts`: `DimmedCtx`, `useDimmedCtx`, `registerDimmedSetter` exported with zero consumers (context never provided). `webview/three/pulse-state.ts`: `PulseCtx`, `usePulseCtx` unused (`setPulse`/`clearPulse` do the real work).

5. **Vestigial `webview/rf/` directory — MEDIUM.** Named for React Flow (retired). Only `adapter.ts` (a re-export) and `animation-fields.ts` remain; name misleads.

6. **store.ts (381 LOC) has two extractable subsystems — MEDIUM.** `toggleFade` (102 lines: reverse-playback walk + fixpoint + pulse cleanup) and `createEdge` (78 lines: port resolution + kind inference + collision). Justified as a state hub, but the fade machinery is a hidden module.

7. **Minor.** `isObj()` duplicated in `schema/parse-primitives.ts` and `webview/state/viewer/parse.ts`. `store.ts ↔ save.ts` import cycle is a safe Zustand `getState()` pattern — leave it.

### Still clean (no action)
Go substrate (no file >1,200 LOC, tight single-responsibility), pump.ts, runCommand.ts, the parsers, PacedWire primacy, stdin dispatch.

### Next-step candidates (organization)
- **Highest value:** finding #2 — relocate generated `node-defs.ts` to `src/schema/`; fixes the inverted layer dependency and the extension boundary crossing in one move.
- Split `ThreeView.tsx` (#1) along the documented section boundaries.
- Remove dead code (#4) and rename/retire `webview/rf/` (#5) — cheap, low-risk.
