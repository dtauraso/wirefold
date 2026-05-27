---
branch: task/architecture-audit
---

# Architecture Audit (2026-05-26)

Architecture is fundamentally sound — codegen-as-sync works, spec/view split is clean, PacedWire is the lone wire primitive, stdin dispatch is one switch. Findings cluster in layer-boundary discipline and a few single-source seams. Discovery was done by three haiku Explore agents; synthesis by Opus.

## Findings (ranked)

1. **requiredInputDiagnostics re-derives substrate deadness in TS — HIGH severity label, but NOT a violation. ✅ RESOLVED (commit `004cb0b1`, doc-comment).** `src/schema/parse-spec.ts:46-101` runs a fixpoint over `REQUIRED_INPUTS` to propagate dead-node status — structurally the Go readiness check. BUT memory feedback_enforce_required_inputs (2026-05-25, commit 0e8d843) records this as a deliberate reversal: editor flags missing required inputs (diagnostic + red node + precondition-gating); substrate does NOT reject the graph. REQUIRED_INPUTS is generated from Go AST so it can't go stale; only the fixpoint propagation lives TS-side. Verdict: intended division, not drift. Action: add a comment pinning it as diagnostic-only.

2. **pseudoTable hardcodes 3 of 4 kinds — MEDIUM (silent runtime failure). ✅ RESOLVED (commit `0a0fec77`).** Investigation found `pseudoTable` is already typed `Record<PseudoKind, …>`, so a missing kind is already a compile error; added a comment at `PSEUDO_KIND_PREFIX` documenting it as the curated pseudo-editable subset (InhibitRightGate intentionally excluded) and the source of `PseudoKind`. Original finding: `src/extension/handle-message.ts:158-162` is a hand-maintained dispatch table; `src/messages.ts:12` PSEUDO_KIND_PREFIX is its sibling. Adding a new pseudo-editable kind compiles but throws at render/save. The one place that genuinely violates the single-registry principle. InhibitRightGate omission is by-design (not editable) but nothing enforces the table covers exactly the pseudo-editable subset.

3. **computeFade fixpoint pattern — MEDIUM (foothold, not a violation). ✅ RESOLVED (commit `dea08726`, render-mask comment).** `src/webview/three/fade.ts` is render-only and correct, but its fixpoint+cascade shape mimics substrate logic — a template someone could copy into firing logic. Action: add explicit "render-mask only, never reference slot/phase/firing state" comment.

4. **edgeSeeds declared/parsed/validated/serialized but never consumed by Go — MEDIUM, CONTRADICTS MEMORY. ⚠️ INVESTIGATED — needs a decision.** Verified against Go: `grep` for `edgeSeeds`/`EdgeSeeds` across the entire Go tree = ZERO hits; `loader.go` `NodeData` has no such field; `reflectBuild` never reads one. Go's real startup seeding is `data.init` on Input nodes (`loader.go:12`). So **Go does not consume `edgeSeeds`** — the memory `feedback_edge_seed_required_for_rings` is inaccurate (names the wrong mechanism). HOWEVER, `edgeSeeds` is NOT dead: `requiredInputDiagnostics` (`parse-spec.ts:67,84`) reads it so a seeded port suppresses the "dead node" flag — i.e. it's an editor-diagnostic-only concept, same pattern as #1. The only no-op is `flow-to-spec.ts:59` serializing `node.data.edgeSeeds` that Go ignores. **Open decision:** (a) document it as diagnostic-only + drop the no-op serialization (recommended, low-risk, mirrors #1); (b) implement Go consumption (changes substrate behavior — not done without intent); (c) remove the concept entirely (would change ring dead-node diagnostics). Also: correct/replace the inaccurate memory. Deferred to user.

5. **Port-name constants in tools/pseudo/readgate.go:14-19 — LOW. ✅ RESOLVED (commit `f56a2463`).** Added `TestReadGate_PortConstantsMatchStructFields` using reflection over the readgate node struct; a rename drift now fails `go test` instead of silently at pseudo-save.

6. **Generated files rely on manual gen:node-defs — LOW. ✅ RESOLVED (commit `d79bcdbf`).** Added `tools/check-generated.sh` (runs the generator, fails if any of the 4 generated files differ from committed); wired into `scripts/stop-checks.sh`.

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

1. **ThreeView.tsx is a 1,489-line god-object — CRITICAL cohesion debt. ✅ RESOLVED (commits `17805746`, `3e864c10`, `d0de5144`, `9db3a6a4`).** Split into geometry-helpers.ts (100), camera-ui.tsx (227), interaction-controls.ts (373), scene-content.tsx (581); ThreeView.tsx is now a 264-line orchestrator. Structure-only, no behavior change; external import (`main.tsx` → `{ ThreeView }`) unchanged. Original finding: Bundles six unrelated domains: geometry helpers, scene/render components, a 326-line `useInteractionControls` state machine, camera UI widgets (RollSlider/DollyButtons/PanPad), occlusion raycasting, and the main orchestrator. Splits cleanly along existing section boundaries into ~5 modules (geometry-helpers, scene-content, interaction-controls, camera-ui, orchestrator); main component would shrink 239 → ~60 lines.

2. **Backwards layer dependency: spec layer imports from webview layer — HIGH (CONVERGENT). ✅ RESOLVED (commit `76af8c02`).** Codegen now emits `node-defs.ts` to `src/schema/`; the `registry.ts` shim and the `webview/schema/` directory are deleted; all importers (incl. a contract test) updated; zero `webview/schema` references remain. Original finding: `src/schema/` is meant to be authoritative spec-side, knowing nothing of the view, but: `schema/node-types.ts:13` imports `NODE_DEFS` from `webview/schema/registry`; `schema/parse-spec.ts:11` imports `REQUIRED_INPUTS` from `webview/schema/node-defs`; `extension/handle-message.ts:23` also reaches into `webview/schema/node-defs`. So `parseSpec()` can't run without view-layer code, and the extension host crosses the postMessage boundary by direct import. Root cause: codegen emits `node-defs.ts` into `webview/schema/` but three layers need it. Fix (one move): relocate generated `node-defs.ts` to `src/schema/`, update codegen output path + imports. Resolves the backwards dep AND the extension violation together. Highest-value correction.

3. **Double-`schema` directory — symptom of #2. ✅ RESOLVED (commit `76af8c02`).** `src/webview/schema/` is deleted as a side effect of #2; only one `schema/` directory remains.

4. **Dead code — confirmed orphans. ✅ RESOLVED (commit `73c6ff3a`).** Removed `DimmedCtx`/`useDimmedCtx`/`registerDimmedSetter` from `dimmed.ts` and `PulseCtx`/`usePulseCtx` from `pulse-state.ts` (kept `setDimmedImperative`/`getDimmed`/the live pulse setters). Original finding: `webview/state/dimmed.ts`: `DimmedCtx`, `useDimmedCtx`, `registerDimmedSetter` exported with zero consumers (context never provided). `webview/three/pulse-state.ts`: `PulseCtx`, `usePulseCtx` unused (`setPulse`/`clearPulse` do the real work).

5. **Vestigial `webview/rf/` directory — MEDIUM. ✅ RESOLVED (commit `5e1f95b9`).** `animation-fields.ts` moved to `three/`; the `adapter.ts` re-export shim (zero importers) deleted; `rf/` removed. Original finding: Named for React Flow (retired). Only `adapter.ts` (a re-export) and `animation-fields.ts` remain; name misleads.

6. **store.ts (381 LOC) has two extractable subsystems — MEDIUM. ✅ RESOLVED (commit `aa6f9040`).** Extracted `three/fade-actions.ts` (`applyFade`/`reconcileFadeOrder`/`computeToggleFade`) and `three/edge-creation.ts` (`buildEdge`) as pure functions; store.ts now 187 lines of thin orchestration. Original finding: `toggleFade` (102 lines: reverse-playback walk + fixpoint + pulse cleanup) and `createEdge` (78 lines: port resolution + kind inference + collision). Justified as a state hub, but the fade machinery is a hidden module.

7. **Minor. ✅ RESOLVED (commit `db30725f`).** `isObj()` now has a single definition in `schema/parse-primitives.ts`; `webview/state/viewer/parse.ts` imports + re-exports it (clean direction: webview→schema). Original finding: `isObj()` duplicated in `schema/parse-primitives.ts` and `webview/state/viewer/parse.ts`. `store.ts ↔ save.ts` import cycle is a safe Zustand `getState()` pattern — leave it.

### Still clean (no action)
Go substrate (no file >1,200 LOC, tight single-responsibility), pump.ts, runCommand.ts, the parsers, PacedWire primacy, stdin dispatch.

### Status
All Organization-Health findings (#1–#7) resolved (see ✅ tags above).
