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
