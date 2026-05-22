# Audit registry

Index of audit categories. CI-backed audits live in [`audits/`](audits/).

## CI-backed audits

- [1. Visual regression — SVG / canvas baselines](audits/01-visual-regression-svg-canvas-baselines.md)

## AI-driven audits

### 6 — Security (AI-driven)
Check webview↔extension postMessage boundaries (payload validation, no eval), topogen spawn uses argv array not shell concat, all file writes inside workspace root, no secrets in committed files, `npm audit` / `go list -m -u all` clean.

### 7 — Code smells (AI-driven)
Scan for near-identical blocks across node-kind handling, functions >~100 lines, god objects (Zustand store), unreachable dead code, magic numbers/strings, comments that explain what not why.

### 8 — Code quality (AI-driven)
Check identifier naming (channel names encode endpoints), error propagation (no silent `_` discards, no catch-all panics), consistency across node-kind adapters, tests actually exercise the claimed property.

### 9 — Time and space complexity (AI-driven)
Audit hot paths: topogen run (≈linear in spec size), animation tick (O(n) in visible nodes/edges, memoized selectors), trace decode (O(frames)), save/load (O(spec size)), no unbounded history stacks in zundo.

### 10 — Architectural tradeoffs (AI-driven)
Verify: spec/viewer split (topology.json vs topology.view.json), codegen authority (no hand-edits to generated Go), React Flow vs custom boundary, undo stack completeness, animation model integrity, plugin/topogen responsibility boundary.

### 11 — Documentation drift (AI-driven)
Check CLAUDE.md paths and node-type table are current, planning doc status claims match code, memory/ files not stale, generated-code "do not edit" headers present.

### 12 — Goroutine and channel leak (AI-driven)
For each `go func()` in `Wiring/` and node packages: what closes it? For each `make(chan ...)`: who closes it? Test teardown stops topology cleanly with no goroutine accumulation.

### 13 — Backpressure invariant (AI-driven)
Verify latch+AND-gate+ack discipline holds: each readLatch has a readGate controlling release (inputs include downstream ack), each detectorLatch has a syncGate (inputs include all detectors), no bypass paths skip a latch.

### 14 — Channel naming convention (AI-driven)
All channel names in `Wiring/` and node packages encode two endpoint nodes (e.g. `in0Ready`, `detectorLatchAck`). Generic names (`ch1`, `done`) without endpoint info are violations. Quick pass; bundle with audit 8.

### 15 — Spec / viewer state hygiene (AI-driven)
Every field in `topology.json` is read by topogen or Go runtime. Every field in `topology.view.json` is ignored by topogen. Ambiguous fields (e.g. keyframes) have a documented decision. High-priority: drift here is the most damaging rot.

### 16 — Error-surface coverage (AI-driven)
topogen errors surface inline near offending node/edge, plugin runtime errors visible to user (not console-only), build/run errors surface in editor, no `catch (e) {}` with no logging in user-affecting paths.

### 17 — Test quality (AI-driven)
Each test fails when code under test breaks, no timing/ordering brittleness, integration tests hit real thing where cost/value allows, no known-flaky tests being re-run to pass.

### 18 — Dependency freshness and supply chain (AI-driven)
`npm audit` in `tools/topology-vscode/`, `go list -m -u all`, lockfiles committed and consistent, surprising new transitive deps, maintainer/ownership changes since last audit.

### 19 — Reading-trip economy (AI-driven)
Load-bearing facts reachable within 1-2 hops from CLAUDE.md/audits.md/session-log. MEMORY.md index lines convey the rule not just topic. No same fact in two drifting locations. Stale planning docs clearly marked closed. Session-log old entries summarized or pruned.

### 20 — AI usage leak (AI-driven)
Opus doing executor work (grep/read/edit) instead of delegating to haiku/sonnet. Fixed per-turn taxes in context window (stale CLAUDE.md lines). Stop-hook wallclock. Bash output noise (known false positives). Wide tool outputs. Recurring rejected proposals after memory exists. Duplicate sources of truth requiring parallel reads.

## Adding new audit categories

To add a new category, append a new numbered section following the existing AI-driven audit pattern.
