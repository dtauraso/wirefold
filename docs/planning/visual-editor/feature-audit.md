---
branch: main
---

# Feature Audit: Wirefold 3D Visual Editor (Planned vs. Implemented)

## Revision history

- **2026-05-27** Cross-cut analysis and reduction proposals added; undo/redo removed from audit (fade animation replaced its one real responsibility); old view-only fold feature (Fold type, `ops/fold.ts`, `folds-state.ts`, `buildFoldNodes`, `collapsedFoldFor`, `foldId`, fold tests/e2e) fully removed (commit `9d4091c5`). New file-dive fold proposal captured under Folds & containment.
- **2026-05-26** Re-verified against post-architecture-audit code. Undo/redo moved from half-wired to not-started. Folds filename corrected. Sublabel edit and edge midpoint drag annotations updated.
- **2026-05-27** (restructure) Reorganized into consistent per-feature template; summary table regenerated; categories grouped by `##` heading; features sorted high→low within each category.
- **2026-05-28** Pulse substrate transport (Phases 1–6, commits `0572704a`–`2662baa4`) — RESOLVED. Summary: `ArcLength`/`SimLatencyMs` added to `PacedWire`; emitted on `send` trace events; `pump.ts` consumes `simLatencyMs` directly; `PULSE_SPEED_WU_PER_MS` constant and Bezier arcLength recompute deleted from TS render layer; latency-live on drag via `NodeMoveRegistry` IPC + same-frame TS-local recompute in `moveNode` (preserves `t_curr` for in-flight beads); curve derived as non-React store state (`setCurve`/`getCurve` in `pulse-state.ts`), updated synchronously in `moveNode`; `latency-changed` trace event removed end-to-end (was ~120 events/sec echo on drag); `nodes/Wiring/curve_params.go` added as single source of truth for curve constants + `BezierArcLength` helper; `gen-node-defs` extended to emit `curve-params.ts`; `rfArcLength` reimplemented as Bezier integration driven by shared constants; Bezier-vs-chord discrepancy closed; dual-constant risk eliminated via codegen. TS→Go relationship is strictly one-way. Cross-cut candidate #1 (Pulse/pump schema) fully resolved.

---

## Summary

The plan was to replace the React Flow 2D editor with a Three.js/R3F 3D canvas (`ThreeView`) backed by a Go substrate (`paced_wire`) that enforces backpressure and slot-phase discipline. The cutover spec (`rf-to-r3f-cutover.md`, `3d-editor.md`) named a full editor: arcball navigation, select/pick, two-click edge creation, inline label edit, multi-select, delete, palette add, undo/redo, persistent view-saves, and a Fold node in both Go and 3D mesh form.

**Scorecard by status:** 15 working · 4 half-wired · 8 restore-parity · 3 accepted-for-build · 1 proposal

### Master feature table

Sorted by cross-cut weight (high → low) within each category. Proposal type shorthand: **codegen** = generate adapter/parser from schema; **store-subscribe** = eliminate prop-drilling, consumers read store directly; **unify** = collapse two separately maintained files into one authoritative source; **observe-not-assert** = derive dirty state by comparison rather than push-notification at gesture sites; **stub-removal** = delete schema stub until feature is actively built; **complete-stub** = wire the field or remove it; **already-minimal** = no reduction available with a one-line reason.

| Feature | Status | Cross-cut weight | Surfaces | Files | Proposal type |
|---|---|---|---|---|---|
| **Substrate** | | | | | |
| Pulse bead + delivered handshake | working | **RESOLVED** (substrate-owned transport timing; 11 files, each with distinct non-overlapping responsibility; dual-constant cross-cut eliminated by codegen via `curve-params.ts`) | Go substrate · Trace · pump · pulse-state · geometry-helpers · store · scene-content · IPC relay | 11 | already-minimal (constants codegen-shared; algorithm drift risk is small, both ~25 lines, same spec) |
| Run/pause/stop controls (runStatus) | working | **High** (structural: threads extension→messages→pulse-state) | store · extension · messages · 3D render · pulse-state | 7 | store-subscribe |
| Paced wire substrate | working | **Low** (local: TS sees only trace events; Go internals opaque) | Go substrate only | 5 Go | already-minimal (hard boundary at pump.ts) |
| Ring-topology deadlock break | working | **Low** (local: startup concern, no TS surface change) | Go substrate · e2e | 2 | already-minimal (irreducible minimum) |
| **Render** | | | | | |
| Spec↔flow adapters + validation flags | working | **High** (structural: every new field threads parse-spec→adapters→store→ThreeNodeMesh) | schema · store · adapters · save/load · 3D render · tests | 6+ | codegen (Strategy B: generate adapters from node-defs.ts) |
| Sublabel inline edit | half-wired | **Medium** (structural: data path load-bearing; missing only 3D gesture trigger) | store · viewer-state · adapters · 3D render · extension/messages | 7 | complete-stub (add 3D gesture in interaction-controls.ts) |
| Node kinds (ChainInhibitor, InhibitRightGate, Input, ReadGate) | working | **Medium** (structural: 3-surface co-commit rule per CLAUDE.md) | schema/codegen · store · adapters · 3D render · Go substrate | 5+ per kind | already-minimal (per CLAUDE.md 3-surface rule; adapter cross-cut addressed by codegen proposal) |
| Two-click edge creation | working | **Medium** (local: creation path self-contained; schema/Go unaware) | 3D render · store · mutation paths · adapters | 5 | already-minimal (irreducible surface for stateful 3D interaction) |
| Three.js/R3F 3D canvas (ThreeView) | working | **Medium** (structural: orchestrator; every new visual feature routes through it) | 3D render · store · schema · save/load · adapters | many | already-minimal (orchestrator cross-cut reflects feature count, not redundancy) |
| Bezier tube edges (SingleEdgeTube) | working | **Low** (local: render-only; no other layer reads WireProps yet) | 3D render · schema · types | 4 | already-minimal (minimum for typed render feature) |
| Billboarded node labels | working | **Low** (local: fully contained in scene-content.tsx) | 3D render | 1 | already-minimal (cross-cut count 1) |
| Nearest-N LOD + occlusion badge | working | **Low** (local: self-contained in 3D render pass) | 3D render | 2 | already-minimal (minimum for any 3D feature) |
| Raycast node pick / single select | working | **Low** (local: single `selectedNodeId` in store; no other layer aware) | 3D render · store | 3 | already-minimal (irreducible for a 3D gesture→store mutation) |
| Z-coordinate (node depth) | half-wired | **Low** (local: parsed but always 0; no UI or render uses it) | schema · adapters | 2 | complete-stub (remove `z` from node-defs.ts + adapter until depth editing is planned) |
| Edge midpoint drag | restore-parity | **Low** (local: schema stub only; no adapter, setter, or drag UI) | schema | 1 | stub-removal (remove `midpointOffset` from wire-defs.ts; reintroduce as `waypoints` when built) |
| Multi-select, Node delete, Node palette, PseudoPanel | restore-parity | — (absent; git-history only) | — | 0 | no action (no stubs; restore as 3–5 file pattern) |
| Port drag, Edge reconnect, Edge delete, Edge-kind context menu | restore-parity | — (absent; git-history only) | — | 0 | no action (no stubs) |
| Multi-node alignment guides | accepted-for-build | — (not present) | — | 0 | no action (gated behind multi-select restore) |
| Bend points / waypoints | accepted-for-build | — (not present; supersedes midpointOffset stub) | — | 0 | no action (spec before code) |
| **Interaction** | | | | | |
| View-save on settle | half-wired | **High** (structural: missing markViewSynced call taxes every new camera gesture) | store · save · viewer-state · 3D render · interaction-controls · camera-ui | 6 | observe-not-assert |
| Arcball / dolly / pan / roll (camera controls) | working | **Medium** (local: camera3d isolated field in viewer-state) | 3D render · store · save/load · schema | 6 | already-minimal (each file has distinct responsibility; view-save closure handled separately) |
| Fit-view hotkey | half-wired | — | ThreeView | 1 | complete-stub (add f/Shift-F handler) |
| **Persistence** | | | | | |
| Load spec / load view | working | **Medium** (structural: new viewer-state field must extend parse.ts + types.ts or loads produce blank state) | store · save/load · viewer-state · schema | 6 | unify (derive ViewerState type from parse.ts inferred return; eliminate separately maintained types.ts) |
| Debounced spec save | working | **Medium** (local: well-isolated in save.ts; callers add one scheduleSave() call) | save/load · store · mutation paths · extension | 6 | already-minimal (natural surface; no parallel structures) |
| Trace/probe logging | working | **Low** (local: one-way log funnel; no feature cross-cuts it) | extension · messages · webview | 5 | already-minimal (each hop distinct; no parallel structure) |
| Undo coalescing at gesture level | accepted-for-build | — (not present) | — | 0 | no action (snapshot-on-pointer-up, 2–3 files when built) |
| **Validation** | | | | | |
| Validation flag colors | working | **Medium** (structural: required-input check in parse-spec; color re-derived in ThreeNodeMesh) | schema · store · 3D render | 5 | observe-not-assert (parse-spec emits `hasError`; ThreeNodeMesh reads passively) |
| **Folds & containment** | | | | | |
| Fold / containment | proposal | — (not present in code) | — | 0 | no action pre-implementation (resolve substrate-layer question first per feedback_specify_substrate_layer_first.md) |

---

## Substrate

### Pulse bead animation + delivered handshake

**Status:** Working — post-refactor steady state. Prior #1 cross-cut candidate resolved. Re-audited 2026-05-28 against code on `task/pulse-substrate-transport`. Phase 6 (commit `2662baa4`) added `curve_params.go` as single source of truth for curve constants + codegen to TS.

**Files (11 files, verified against code):**

Go substrate:
- `nodes/Wiring/curve_params.go` — `CurveParamPulseSpeedWuPerMs = 0.08`, `CurveParamMinArcLength = 1.0`, `CurveParamBulgeFactor = 0.25`, `CurveParamBezierSampleCount = 64`; `BezierArcLength(p0, p2, bulgeFactor, samples)` helper (64-sample Bezier integration). Single source of truth; parsed by `gen-node-defs` and emitted as `curve-params.ts`.
- `nodes/Wiring/paced_wire.go` — `PacedWire` struct with `ArcLength`/`SimLatencyMs` fields; `NewPacedWire(arcLength, pulseSpeed)` derives `SimLatencyMs = arcLength / pulseSpeed`; `NotifyDelivered` unblocks `Recv`; `Done` unblocks `Send`.
- `nodes/Wiring/ports.go` — `TrySend` emits trace event via `trace.SendWire(node, port, v, pw.ArcLength, pw.SimLatencyMs)` BEFORE blocking on `pw.Send` (deadlock prevention comment in code).
- `nodes/Wiring/loader.go` — `arcLengthBetween(a, b specPosition)` calls `BezierArcLength`; `NewPacedWire(arcLength, CurveParamPulseSpeedWuPerMs)` called per destination port. Chord-distance computation removed.
- `nodes/Wiring/stdin_reader.go` — `NodeMoveRegistry`; `node-move` handler calls `BezierArcLength` and updates `pw.SimLatencyMs`/`pw.ArcLength` under mutex; `delivered` handler calls `pw.NotifyDelivered()`. `arcLengthBetween3` removed (consolidated into `BezierArcLength`).
- `Trace/Trace.go` — `Event` struct carries `ArcLength float64` and `SimLatencyMs float64`; `SendWire` method populates both; `marshalEvent` emits them as `omitempty` JSON fields on `send` events.

Codegen:
- `tools/gen-node-defs/main.go` — extended to parse `CurveParam*` constants from Go source and emit `curve-params.ts`.
- `tools/topology-vscode/src/schema/curve-params.ts` — generated; exports `CURVE_PARAM_PULSE_SPEED_WU_PER_MS`, `CURVE_PARAM_MIN_ARC_LENGTH`, `CURVE_PARAM_BULGE_FACTOR`, `CURVE_PARAM_BEZIER_SAMPLE_COUNT`. Covered by `tools/check-generated.sh`.

TS layer:
- `tools/topology-vscode/src/webview/three/pump.ts` — `handleTraceEvent` for `send` reads `simLatencyMs` from event; calls `setPulse(edge.id, { value, simStep, target, targetHandle, simLatencyMs })`. 500ms fallback guards stale Go binary. No clock fabrication.
- `tools/topology-vscode/src/webview/three/pulse-state.ts` — imperative non-React stores: `PulseMap` (per-edge in-flight pulse data including `simLatencyMs` + `startTime = getPauseAdjustedNow()`) and `_curveMap` (per-edge `QuadraticBezierCurve3`). Exports: `setPulse`, `clearPulse`, `patchPulse`, `getCurve`, `setCurve`, `claimDelivered`.
- `tools/topology-vscode/src/webview/three/geometry-helpers.ts` — imports four constants from `curve-params.ts`; `rfArcLength(ax, ay, bx, by)` reimplemented as 64-sample Bezier integration (same algorithm as Go's `BezierArcLength`, driven by shared constants); `arcLengthToSimLatencyMs(arcLength)`.
- `tools/topology-vscode/src/webview/three/store.ts` — `moveNode(id, x, y)`: updates node positions, then synchronously for every touching edge: calls `setCurve(buildEdgeCurve(...))` and, for in-flight pulses, calls `patchPulse(edge.id, newSimLatencyMs, newStartTime)` preserving fractional progress `t_curr`.
- `tools/topology-vscode/src/webview/three/scene-content.tsx` — `PulseBead` reads `getPulseMap()` + `getCurve()` imperatively in `useFrame`; computes `t = (getPauseAdjustedNow() - pulse.startTime) / pulse.simLatencyMs`; at t≥1 calls `claimDelivered` + posts `{type:"delivered", edge:edgeId}`. Uses `CURVE_PARAM_BULGE_FACTOR` from `curve-params.ts` (was hardcoded `0.25`).
- `tools/topology-vscode/src/webview/three/interaction-controls.ts` — drag handler calls `store.moveNode(nodeId, x, y)` on pointer-move; rAF-throttled `node-move` IPC posts `{type:"node-move", nodeId, x, y, z:0}` to extension host.

IPC relay (host):
- `tools/topology-vscode/src/extension/handle-message.ts` — forwards `delivered` → `runner.writeStdin(JSON.stringify({type:"delivered", edge}))` and `node-move` → `runner.writeStdin(JSON.stringify({type:"node-move", ...}))`.

**Data flow (send → bead → delivered → recv unblocks):**
1. Go `TrySend` calls `trace.SendWire(node, port, v, pw.ArcLength, pw.SimLatencyMs)` then blocks in `pw.Send`.
2. Extension streams `trace-event` to webview; `pump.ts:handleTraceEvent("send")` calls `setPulse(edgeId, { simLatencyMs, ... })` with `startTime = getPauseAdjustedNow()`.
3. `PulseBead.useFrame` reads pulse + curve each frame; computes `t`; at `t ≥ 1` posts `"delivered"`.
4. Extension host forwards `"delivered"` → Go stdin; `stdin_reader` calls `pw.NotifyDelivered()` → `deliveryCh` closed → `pw.Recv` unblocks.
5. `pump.ts:handleTraceEvent("done")` clears pulse via `clearPulse(edgeId)`.

**Drag geometry update (same-frame invariant):**
- `store.ts:moveNode` recomputes curve + `simLatencyMs` synchronously, adjusting `startTime` to preserve `t_curr`. No Go round-trip for in-flight rendering.
- rAF-throttled `node-move` IPC keeps Go `PacedWire` geometry current for future sends.
- Relationship TS→Go is strictly one-way; Go does not echo back.

**Invariant — uniform speed:** `simLatencyMs = max(arcLength, 1.0) / 0.08`. Longer wire = longer time, same speed.

**Cross-cuts:** 11 files across Go substrate + codegen + TS store + TS render + IPC relay. Weight: **low** — each file has a distinct, non-overlapping responsibility. No redundant state. The TS→Go direction is needed for future-send correctness; the dual arc-length computation is required because Go computes at send time and TS recomputes at drag time without a round trip. Constants are codegen-shared; residual drift risk is algorithm-implementation drift only (both ~25 lines, same spec).

**Known limitations:**
- Go and TS implement the Bezier arc-length algorithm in parallel (~25 lines each, same spec, same constants). Both driven by `curve_params.go` constants via codegen. Algorithm-implementation drift is the residual risk; small and bounded.

**Reduction proposal:** The dual-constant cross-cut (speed + min-arc-length + bulge-factor + sample-count) is fully resolved by Phase 6 codegen. Constants are generated from `curve_params.go` into `curve-params.ts`; no manual sync required. The residual is algorithm-implementation drift (Go `BezierArcLength` vs. TS `rfArcLength`): both are ~25 lines implementing the same quadratic-Bezier integration spec, isolated parallel implementations driven by shared constants. Drift risk is small; no further reduction warranted.

**Revision history:**
- 2026-05-28 (re-audit) — Rewrote from scratch against code on `task/pulse-substrate-transport`. Corrected stale file paths, documented actual 10-file shape, added Bezier-vs-chord known limitation, documented dual-function Go anomaly, updated cross-cut verdict.
- 2026-05-28 (Phase 6, `2662baa4`) — `curve_params.go` added; codegen to `curve-params.ts`; `rfArcLength` reimplemented as Bezier integration; `arcLengthBetween3` removed; Bezier-vs-chord discrepancy closed; dual-constant risk eliminated. File count 10→11.

---

### Run/pause/stop controls (runStatus)

**Status:** working

**Files:**
- `tools/topology-vscode/src/webview/state/run-status.ts`
- `tools/topology-vscode/src/extension.ts`
- `tools/topology-vscode/src/messages.ts`
- `tools/topology-vscode/src/webview/main.tsx`
- `tools/topology-vscode/src/webview/three/RunButton.tsx`
- `tools/topology-vscode/src/webview/three/scene-content.tsx`
- `tools/topology-vscode/src/webview/three/pulse-state.ts`

**Cross-cuts:** Surfaces: store · extension · messages · 3D render · pulse-state. Distinct files: 7. Weight: **High** (structural: `runStatus` threads from `extension.ts` through `messages.ts` into `pulse-state` and `scene-content`; a new control state requires editing every relay hop).

**Reduction proposal:**
- Axis: `runStatus` is relayed hand-to-hand through `main.tsx` as a prop into `<ThreeView>`, then into `scene-content.tsx` and `pulse-state.ts`. Each relay hop is a mandatory edit site for any new run-state variant.
- Strategy: **store-subscribe** — `scene-content.tsx`, `pulse-state.ts`, and `RunButton.tsx` read `runStatus` directly from the store via `useEditorStore`. Remove prop-drilling through `main.tsx` and `ThreeView`.
- Expected post-change cross-cut count: **7 → 4** (`state/run-status.ts`, `extension.ts`, `messages.ts`, `RunButton.tsx`). `main.tsx`, `scene-content.tsx`, and `pulse-state.ts` become direct store subscribers.
- Blocker: none structural. `useEditorStore` is already available in every component in the `three/` subtree.

---

### Paced wire substrate

**Status:** working

**Files:**
- `nodes/Wiring/paced_wire.go`
- `nodes/Wiring/builders.go`
- `nodes/Wiring/loader.go`
- `nodes/Wiring/ports.go`
- `main.go`

**Cross-cuts:** Surfaces: Go substrate only. Distinct files: 5 Go. Weight: **Low** (local: TS side sees only trace events through `pump.ts`; Go internals are opaque to all TS surfaces).

**Reduction proposal:** Already minimal. `pump.ts` is the hard boundary. TS sees zero Go internals. The Go-internal structure is governed by MODEL.md, not by cross-cut audit.

---

### Ring-topology deadlock break

**Status:** working

**Files:**
- `nodes/input/` (Go package)
- `e2e/scenario-ring-animates.spec.ts`

**Cross-cuts:** Surfaces: Go substrate · e2e tests. Distinct files: 2. Weight: **Low** (local: startup concern; no TS surface change required).

**Reduction proposal:** Already minimal. 2-file cross-cut is the irreducible minimum for a Go-only startup concern.

---

## Render

### Spec↔flow adapters + validation flag colors

**Status:** working

**Files:**
- `tools/topology-vscode/src/webview/state/adapter/spec-to-flow.ts`
- `tools/topology-vscode/src/webview/state/adapter/flow-to-spec.ts`
- `tools/topology-vscode/src/webview/state/adapter/spec-to-flow-helpers.ts`
- `tools/topology-vscode/src/webview/schema/parse-spec.ts`
- `tools/topology-vscode/src/webview/schema/parse-nodes-edges.ts`
- `tools/topology-vscode/src/webview/schema/index.ts`
- `tools/topology-vscode/src/webview/store.ts`
- `tools/topology-vscode/src/webview/save.ts`
- `tools/topology-vscode/src/webview/three/ThreeNodeMesh.tsx` (in `scene-content.tsx`)

**Cross-cuts:** Surfaces: schema · store · adapters · save/load · 3D render · tests. Distinct files: 6+. Weight: **High** (structural: every new node or edge field passes through both adapter files plus `parse-spec.ts`; a field addition is a 6-file minimum change).

**Reduction proposal:**
- Axis: `spec-to-flow.ts` and `flow-to-spec.ts` must be manually updated for every new field. The schema in `schema/node-defs.ts` is already the authoritative field list and is the natural codegen source.
- Strategy: **codegen (Strategy B)** — generate `spec-to-flow.ts` and `flow-to-spec.ts` from `schema/node-defs.ts` + `schema/wire-defs.ts`. A field addition becomes one schema edit; adapters regenerate. Strategy A (unify spec and flow shapes) is partially blocked by RF-shaped fields in `types.ts` that have no spec equivalent (e.g. `z` half-wiring, RF-internal `id` conventions).
- Expected post-change cross-cut count: **6 → 2** (schema edit + regenerate; `store.ts` and `save.ts` unaffected).
- Blocker: no codegen infrastructure exists today. Defer until ≥3 field additions in one session make the friction acute. Start with `node-defs.ts → spec-to-flow.ts` only.

---

### Sublabel inline edit

**Status:** half-wired — `beginEditSublabel` exists in `inline-edit.ts`; missing is a 3D gesture to trigger it (only `RunButton` calls `flushActiveInlineEdit`).

**Files:**
- `tools/topology-vscode/src/webview/inline-edit.ts`
- `tools/topology-vscode/src/webview/types.ts`
- `tools/topology-vscode/src/webview/state/viewer-state.ts`
- `tools/topology-vscode/src/webview/state/adapter/flow-to-spec.ts`
- `tools/topology-vscode/src/webview/state/adapter/spec-to-flow.ts`
- `tools/topology-vscode/src/webview/state/viewer/parse.ts`
- `tools/topology-vscode/src/webview/three/ThreeView.tsx`
- `tools/topology-vscode/src/webview/three/RunButton.tsx`
- `tools/topology-vscode/src/webview/schema/node-defs.ts`

**Cross-cuts:** Surfaces: store · viewer-state · adapters · 3D render · extension/messages. Distinct files: 7 (9 with RunButton + node-defs). Weight: **Medium** (structural: `inline-edit.ts` is the trigger; the data path through `viewer-state → adapters → spec` is load-bearing; the missing piece is a single 3D gesture).

**Reduction proposal:** The half-wiring IS the cross-cut. Do not restructure the existing path — add one gesture trigger in `interaction-controls.ts` calling `beginEditSublabel`. Once wired, the 7-file cross-cut is structurally justified (each file has a distinct role). No further reduction warranted.

---

### Node kinds (ChainInhibitor, InhibitRightGate, Input, ReadGate)

**Status:** working

**Files (per kind):**
- `tools/topology-vscode/src/webview/schema/node-defs.ts`
- `tools/topology-vscode/src/webview/schema/node-data-types.ts`
- `tools/topology-vscode/src/webview/state/adapter/spec-to-flow.ts`
- `tools/topology-vscode/src/webview/state/adapter/flow-to-spec.ts`
- `tools/topology-vscode/src/webview/three/scene-content.tsx`
- `nodes/<Kind>/` (Go package)

**Cross-cuts:** Surfaces: schema/codegen · store · adapters · 3D render · Go substrate. Distinct files: 5+ per kind. Weight: **Medium** (structural per CLAUDE.md: adding a kind requires 3 co-committed surfaces — `<Kind>Node.tsx`, registry, Go package).

**Reduction proposal:** Already minimal per CLAUDE.md. The 3-surface co-commit rule is the minimum coherent unit for adding a kind. The adapter cross-cut is addressed by the Spec↔flow codegen proposal (Strategy B). No further reduction independent of that proposal.

---

### Two-click edge creation

**Status:** working

**Files:**
- `tools/topology-vscode/src/webview/three/edge-creation.ts`
- `tools/topology-vscode/src/webview/three/interaction-controls.ts`
- `tools/topology-vscode/src/webview/three/ThreeView.tsx`
- `tools/topology-vscode/src/webview/store.ts`
- `tools/topology-vscode/src/webview/state/adapter/spec-to-flow.ts`

**Cross-cuts:** Surfaces: 3D render · store · mutation paths · adapters. Distinct files: 5. Weight: **Medium** (local: creation path is self-contained in the 3D layer; schema and Go are unaware of how an edge is created).

**Reduction proposal:** Already minimal. 5 files is the irreducible surface for a stateful 3D interaction (gesture capture, creation logic, orchestrator, store mutation, adapter). No parallel structure exists.

---

### Three.js/R3F 3D canvas (ThreeView)

**Status:** working — `ThreeView.tsx` (~264-line orchestrator); geometry in `geometry-helpers.ts`; render in `scene-content.tsx`.

**Files:**
- `tools/topology-vscode/src/webview/three/ThreeView.tsx`
- `tools/topology-vscode/src/webview/three/scene-content.tsx`
- `tools/topology-vscode/src/webview/three/geometry-helpers.ts`
- (virtually every other `three/` file)

**Cross-cuts:** Surfaces: 3D render · store · schema · save/load · adapters. Weight: **Medium** (structural: ThreeView is the orchestrator; any new visual feature must route through it).

**Reduction proposal:** Already minimal. ThreeView's cross-cut count reflects the number of 3D features, not redundancy. No reduction available without collapsing distinct features.

---

### Bezier tube edges (SingleEdgeTube)

**Status:** working

**Files:**
- `tools/topology-vscode/src/webview/three/SingleEdgeTube.tsx` (rendered inside `scene-content.tsx`)
- `tools/topology-vscode/src/webview/three/ThreeView.tsx`
- `tools/topology-vscode/src/webview/types.ts`
- `tools/topology-vscode/src/schema/wire-defs.ts`

**Cross-cuts:** Surfaces: 3D render · schema (wire-defs) · types. Distinct files: 4. Weight: **Low** (local: render-only; `WireProps` in `wire-defs.ts` carries props but no other layer reads them yet).

**Reduction proposal:** Already minimal. 4 files is the minimum for a typed render feature (render component, types, schema, orchestrator). `wire-defs.ts` is the single source of truth for wire props.

---

### Billboarded node labels

**Status:** working — `@react-three/drei` `Billboard` + `Text` in `scene-content.tsx`.

**Files:**
- `tools/topology-vscode/src/webview/three/scene-content.tsx`

**Cross-cuts:** Surfaces: 3D render only. Distinct files: 1. Weight: **Low** (local: fully contained in `scene-content.tsx`).

**Reduction proposal:** Already minimal. Cross-cut count is 1. No reduction available.

---

### Nearest-N LOD + occlusion badge

**Status:** working

**Files:**
- `tools/topology-vscode/src/webview/three/scene-content.tsx`
- `tools/topology-vscode/src/webview/three/OcclusionBadge.tsx`

**Cross-cuts:** Surfaces: 3D render only. Distinct files: 2. Weight: **Low** (local: fully self-contained in the 3D render pass).

**Reduction proposal:** Already minimal. Cross-cut count is 2 (scene-content + OcclusionBadge). Minimum for any 3D feature.

---

### Raycast node pick / single select

**Status:** working

**Files:**
- `tools/topology-vscode/src/webview/three/interaction-controls.ts`
- `tools/topology-vscode/src/webview/three/ThreeView.tsx`
- `tools/topology-vscode/src/webview/store.ts`

**Cross-cuts:** Surfaces: 3D render · store. Distinct files: 3. Weight: **Low** (local: selection state is a single `selectedNodeId` in store; no other layer is selection-aware today).

**Reduction proposal:** Already minimal. 3 files is the irreducible surface for a 3D gesture that mutates store state.

---

### Z-coordinate (node depth)

**Status:** half-wired — schema parses `z`; always 0 in practice; no UI to set depth.

**Files:**
- `tools/topology-vscode/src/webview/schema/node-defs.ts`
- `tools/topology-vscode/src/webview/state/adapter/spec-to-flow.ts`

**Cross-cuts:** Surfaces: schema · adapters. Distinct files: 2. Weight: **Low** (local: parsed but always 0; no 3D render or UI surface reads it meaningfully).

**Reduction proposal:** Complete it or remove it. **Preferred: (a) remove `z` from `node-defs.ts` and the adapter** until a depth-editing UI is planned. The stub pays schema-parse and adapter tax with no user benefit. Reintroduce when depth editing is actively built.

---

### Edge midpoint drag

**Status:** restore-parity — `midpointOffset` at `schema/wire-defs.ts:13,24` is a schema stub only; no adapter reads it, no setter, no drag UI.

**Files:**
- `tools/topology-vscode/src/schema/wire-defs.ts` (lines 13, 24 — stub only)

**Cross-cuts:** Surfaces: schema (stub only). Distinct files: 1. Weight: **Low** (local: purely a schema stub; no live cross-cuts).

**Reduction proposal:** Stub-removal. Remove `midpointOffset` from `wire-defs.ts` now to keep the schema honest. The accepted-for-build bend-points feature supersedes it; reintroduce as a generalized `waypoints` field when actively built. Leaving the stub risks a future adapter silently treating a zero-valued field as meaningful.

---

### Port drag / Edge reconnect / Edge delete / Edge-kind context menu

**Status:** restore-parity — all absent from code (deleted; git-history only).

**Files:** none (deleted)

**Cross-cuts:** Distinct files: 0. Weight: —

**Reduction proposal:** No action. No stubs to remove. When restored, each should follow the same 3–5 file pattern as two-click edge creation.

---

### Multi-select / Node delete / Node palette / PseudoPanel

**Status:** restore-parity — all absent from code (deleted; git-history only).

**Files:** none (deleted)

**Cross-cuts:** Distinct files: 0. Weight: —

**Reduction proposal:** No action. No stubs to remove. When restored, scope to the analogous working-feature pattern.

---

### Multi-node alignment guides

**Status:** accepted-for-build — not present in code; generalizes single-node drag guides to multi-selection bounding box; gated behind multi-select restore.

**Files:** none yet

**Cross-cuts:** Distinct files: 0. Weight: —

**Reduction proposal:** No action now. When built, will touch interaction-controls + store (selection) + 3D render. No pre-build reduction possible.

---

### Bend points / waypoints

**Status:** accepted-for-build — generalizes midpoint drag to arbitrary waypoints; supersedes `midpointOffset` stub; per-edge persisted state threaded through schema + adapters.

**Files:** none yet

**Cross-cuts:** Distinct files: 0. Weight: —

**Reduction proposal:** No action now. When built, will be structural (schema → adapters → store → 3D render → save/load all touched).

---

## Interaction

### View-save on settle

**Status:** half-wired — `markViewSynced` called inside `loadView`; NOT called after camera/drag, so positions are lost on reload.

**Files:**
- `tools/topology-vscode/src/webview/store.ts`
- `tools/topology-vscode/src/webview/save.ts` (lines 48, 78)
- `tools/topology-vscode/src/webview/state/viewer-state.ts`
- `tools/topology-vscode/src/webview/three/ThreeView.tsx`
- `tools/topology-vscode/src/webview/three/camera-ui.tsx`
- `tools/topology-vscode/src/webview/three/interaction-controls.ts`

**Cross-cuts:** Surfaces: store · save/load · viewer-state · 3D render · interaction-controls · camera-ui. Distinct files: 6. Weight: **High** (structural: missing `markViewSynced` call after camera/drag means every new camera gesture must remember to also trigger the view-save path — a tax on every future gesture added).

**Reduction proposal:**
- Axis: every camera gesture (arcball drag, dolly, pan, roll) must call `markViewSynced` to close the view-save loop. Today none of them do; only `loadView` calls it.
- Strategy: **observe-not-assert** — derive dirty state by comparison. A single effect compares `currentCamera3d` (read from the camera ref on `pointerup`/`wheel`-end events) against `lastSavedCamera3d` (written on each successful save). When they differ for longer than a debounce interval (e.g. 1.5 s), trigger `scheduleViewSave`. No per-gesture call site needed; new gestures are automatically covered.
- Implementation: add `lastSavedCamera3d` to `state/viewer/types.ts`; update `save.ts` to write it after a successful save; add one debounced `useEffect` in `ThreeView.tsx` (or `CameraRefBridge`); remove or no-op `markViewSynced` in `store.ts`.
- Expected post-change cross-cut count: **6 → 3** (`state/viewer/types.ts`, `save.ts`, `ThreeView.tsx`). `store.ts`, `camera-ui.tsx`, and `interaction-controls.ts` need no changes.
- Blocker: camera ref already exposed via `CameraRefBridge` in `ThreeView.tsx`. No structural blocker.

---

### Arcball / dolly / pan / roll (camera controls)

**Status:** working

**Files:**
- `tools/topology-vscode/src/webview/three/interaction-controls.ts`
- `tools/topology-vscode/src/webview/three/camera-ui.tsx`
- `tools/topology-vscode/src/webview/three/ThreeView.tsx`
- `tools/topology-vscode/src/webview/store.ts`
- `tools/topology-vscode/src/webview/save.ts`
- `tools/topology-vscode/src/webview/state/viewer/types.ts`

**Cross-cuts:** Surfaces: 3D render · store · save/load · schema (viewer/types). Distinct files: 6. Weight: **Medium** (local: `camera3d` is an isolated field in `viewer-state`; only `save.ts` and `store.ts` need to be aware of it).

**Reduction proposal:** Already minimal. Camera3d is a single isolated field in `state/viewer/types.ts`; the 6 files are the natural surface (gesture capture, UI, scene, store, save, schema). Each has a distinct responsibility. View-save closure is handled by the separate View-save proposal above.

---

### Fit-view hotkey

**Status:** half-wired — fit-on-load only; no f/Shift-F for manual re-fit.

**Files:**
- `tools/topology-vscode/src/webview/three/ThreeView.tsx`

**Cross-cuts:** Surfaces: 3D render. Distinct files: 1. Weight: —

**Reduction proposal:** Complete-stub. Add `f`/`Shift-F` keyboard handler in `ThreeView.tsx` triggering the existing `fitCamera` logic.

---

## Persistence

### Load spec / load view

**Status:** working

**Files:**
- `tools/topology-vscode/src/webview/store.ts` (`loadSpec`, `loadView`)
- `tools/topology-vscode/src/webview/save.ts`
- `tools/topology-vscode/src/webview/state/viewer-state.ts`
- `tools/topology-vscode/src/webview/state/viewer/parse.ts`
- `tools/topology-vscode/src/webview/state/viewer/types.ts`
- `tools/topology-vscode/src/webview/three/ThreeView.tsx`

**Cross-cuts:** Surfaces: store · save/load · viewer-state · schema. Distinct files: 6. Weight: **Medium** (structural: any new viewer-state field must extend both `viewer/parse.ts` and `viewer/types.ts` or loads produce blank state).

**Reduction proposal:**
- Axis: `viewer/parse.ts` and `viewer/types.ts` must be kept in sync manually. A mismatch produces a silent blank-load bug.
- Strategy: **unify** — make `viewer/parse.ts` return a typed result whose inferred return type IS the `ViewerState` type, eliminating the separately maintained `types.ts` interface. A type mismatch becomes a compile error rather than a silent bug.
- Expected post-change cross-cut count: **6 → 5** (the two parse files collapse into one authoritative source; `store.ts`, `save.ts`, and `ThreeView.tsx` unaffected).
- Blocker: none structural; a pure TS refactor with no behavior change.

---

### Debounced spec save

**Status:** working

**Files:**
- `tools/topology-vscode/src/webview/save.ts`
- `tools/topology-vscode/src/webview/store.ts`
- `tools/topology-vscode/src/webview/three/interaction-controls.ts`
- `tools/topology-vscode/src/webview/main.tsx`
- `tools/topology-vscode/src/webview/three/SaveLifecycle.tsx`
- e2e specs

**Cross-cuts:** Surfaces: save/load · store · mutation paths · extension. Distinct files: 6. Weight: **Medium** (local: well-isolated in `save.ts`; callers add exactly one `scheduleSave()` call, no awareness of internals required).

**Reduction proposal:** Already minimal. `save.ts` is the single locus of save logic. The 6-file cross-cut is the natural surface (save engine, store, two caller gesture files, lifecycle component, e2e coverage). No parallel structures exist.

---

### Trace/probe logging

**Status:** working — `.probe/` JSONL files; `probe.ts` in webview.

**Files:**
- `tools/topology-vscode/src/webview/log/post.ts`
- `tools/topology-vscode/src/extension/webview-log.ts`
- `tools/topology-vscode/src/messages.ts`
- `tools/topology-vscode/src/runCommand.ts`
- `tools/topology-vscode/src/webview/probe.ts`

**Cross-cuts:** Surfaces: extension · messages · webview log pipeline. Distinct files: 5. Weight: **Low** (local: well-isolated one-way log funnel; no feature cross-cuts it).

**Reduction proposal:** Already minimal. The 5-file pipeline is a one-way log funnel (Go → extension → messages → webview → probe.ts). Each hop is distinct; no parallel structure exists.

---

### Undo coalescing at gesture level

**Status:** accepted-for-build — snapshot-on-pointer-up pattern; from-scratch undo/redo build.

**Files:** none yet

**Cross-cuts:** Distinct files: 0. Weight: —

**Reduction proposal:** No action now. When built, the snapshot-on-pointer-up pattern is scoped to interaction-controls + a history store (2–3 files). No pre-build reduction possible.

---

## Validation

### Validation flag colors

**Status:** working — `ThreeNodeMesh.tsx`; `parseSpec` diagnostics.

**Files:**
- `tools/topology-vscode/src/webview/schema/parse-spec.ts`
- `tools/topology-vscode/src/webview/schema/parse-nodes-edges.ts`
- `tools/topology-vscode/src/webview/schema/index.ts`
- `tools/topology-vscode/src/webview/store.ts`
- `tools/topology-vscode/src/webview/three/ThreeNodeMesh.tsx` (inside `scene-content.tsx`)

**Cross-cuts:** Surfaces: schema (parse-spec) · store · 3D render. Distinct files: 5. Weight: **Medium** (structural: required-input validation is checked in `parse-spec` but the color decision is re-derived in `ThreeNodeMesh`; new required constraints require touching both ends).

**Reduction proposal:**
- Strategy: **observe-not-assert** — `parse-spec` emits `validationColor` or the existing `hasError` boolean directly into node data; `ThreeNodeMesh` reads that field passively rather than re-checking required inputs. New required constraints then require only a `parse-spec` edit.
- Expected post-change cross-cut count: **5 → 3** (`parse-spec.ts`, `store.ts`, `ThreeNodeMesh.tsx`). `parse-nodes-edges.ts` and `schema/index.ts` are already on the parse path; only their output shape changes.
- Blocker: none structural.

---

## Folds & containment

### Fold / containment

**Status:** proposal — not implemented; old view-only fold feature fully removed (commit `9d4091c5`).

**Files:** none yet

**Cross-cuts:** Distinct files: 0. Weight: — (projected: **Very High** — spec layer, viewer-state, adapters, 3D render, Go substrate all affected by design).

**Reduction proposal:** No reduction available pre-implementation. The projected cross-cut (spec, viewer-state, adapters, 3D render, Go substrate) is inherent to the feature's responsibility. Pre-work recommendation: resolve the open substrate question (how a child diagram associates with a node at spec vs. viewer-state layer) before writing any code, per `feedback_specify_substrate_layer_first.md`. That decision determines whether the cross-cut is 5 surfaces or 7.

**Proposal shape (not yet specced at spec/viewer-state layer):**
- Fold is an **attribute**, not a node kind. Any node can be marked as a fold.
- **Visibility and execution are coupled.** Unfolded = visible + running; folded = hidden + not running.
- **Authoring gesture.** Create a node → assign fold attribute → select nodes to place inside → they move into the fold node's child diagram. The fold node adopts boundary edges.
- **Reveal gesture (expand-in-place).** Each fold node has a show/hide toggle. Toggling "show" reveals one level down inline, anchored to the fold node. No full-screen canvas swap, no breadcrumb navigation.
- **Hierarchy.** Folds can nest; each child diagram may contain fold nodes with their own toggles.
- **Open substrate question.** How a child diagram associates with a node at spec vs. viewer-state layer must be resolved first.
