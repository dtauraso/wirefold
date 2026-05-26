# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-26, editor-r3f — pulses wired, rf-retirement plan, blank-reload bug open)

**Active branch:** `editor-r3f` (long-lived R3F source line; NOT merged to main).

### Why 3D

Not cosmetics. The topology genuinely has depth — the wire structures
(inhibitor chain, rings, lateral-inhibition lattices) have real
geometry that the current 2D React Flow canvas flattens into
misleading edge crossings and false adjacencies. The move is about
**representational honesty**: make the rendered structure match the
actual structure.

### Direction (locked)

**ONE 3D view, ONE store.** R3F is THE editor. RF is being retired, not maintained as a peer or fallback. RF removal from `main` = the merge event, not an in-branch deletion debate. This branch carries only R3F things.

Drift guard still applies: interaction CONTROL is substance (no OrbitControls); rendering is medium (R3F yes). zustand is a medium choice (already a dep) — fine to adopt as the store.

### Governing principle (most important — drift guard)

**Interaction CONTROL is substance, not medium.** This is a
**classification clause** of CLAUDE.md's medium-vs-substance rule, NOT
a competing rule. One rulebook, correctly applied.

Decision procedure:

1. Is this rendering/plumbing, or control over the system?
2. Rendering → industry default (react-three-fiber: yes).
3. Control → substance → design from need → apply the
   **recoverable-by-device test**: if a better input device does NOT
   restore a lost capability without changing the design, the loss is
   baked into the design → wrong industry pattern-match → REJECT.

`drei`'s `OrbitControls` FAILS this test (a SpaceMouse still leaves a
fixed pivot and locked roll — the loss is in the design) and must
**NOT** be adopted. Adopting R3F (medium, yes) does not imply adopting
OrbitControls (substance, no).

This principle is also saved in
[`memory/project_interaction_control_is_substance.md`](../../../memory/project_interaction_control_is_substance.md)
(rides to main on merge). Full design lives in
[3d-editor.md](3d-editor.md) (branch-local — does **not** ride the
merge).

### Done this session (committed + pushed to editor-r3f, build green)

- **Wire-pulse animation WIRED (commit 3154816e):** main.tsx message handler gained a `trace-event` case calling `handleTraceEvent(msg.event)` (imported from ./rf/pump). This connects the existing producer (pump.ts → setPulse, keyed by edge.id) to the existing consumer (ThreeView PulseBead reading getPulseMap().get(edgeId)). Message shape `{type:"trace-event", event}` matches what extension.ts posts; edge.id keys line up. `npm run build` clean, out/webview.js refreshed. NOT yet live-verified — beads-animate-on-Run is UNCONFIRMED.
- **rf-retirement plan written (commit 3c819011):** docs/planning/visual-editor/rf-retirement.md (branch-tagged). 6-phase plan to retire the rf/ folder. Key facts it records: R3F NEVER renders via reactflow at runtime — only couplings are one dead CSS import in main.tsx + RFNode/RFEdge type aliases in 7 files (all `import type`). Three-way bucket: (A) dead-now = rf-imperative.ts; (B) live-but-misfiled-under-rf = types.ts, viewer-state.ts, history.ts, dimmed.ts, folds-state.ts, run-status.ts, pulse-state.ts, pump.ts, trace-kinds.ts, adapter/*, panels/RunButton.tsx, nodes/node-defs.ts+registry.ts (relocatable, no runtime reactflow dep); (C) genuine reactflow coupling = the RFNode/RFEdge type sites only. DECISION LOCKED: keep pulses (pulse-state.ts + pump.ts + trace-kinds.ts are live R3F animation infra); fire-flash-state.ts/slots-state.ts/held-values.ts have NO R3F consumer and are deletable.

### BLOCKING open bug — blank diagram on reload (intermittent)

- Symptom: on window reload the 3D diagram sometimes comes up blank.
- CRITICAL clue captured this session: it came back WITHOUT a reload (user confirmed "I have not reloaded") — i.e. the view populated asynchronously after an initial empty window. So it is NOT a load-order race that re-running `load` fixes; it is an async-arrival / late-render window.
- RULED OUT this session (do not re-investigate): viewer-state module duplication (single holder at rf/viewer-state.ts — store reads/writes the same instance); parseSpec throwing on the embedded top-level `view` key (parseSpec TOLERATES extra keys, loadSpec does not throw); module-load throw in the pump import chain (chain is clean, no top-level side effects); stale bundle (out/webview.js is fresh, contains the trace-event handler).
- NOT yet captured: the actual webview DevTools console error/log ordering during a blank instance — probe bridge wrote ZERO .jsonl on the blank reload. This is the missing evidence.
- Design smell noted (agent finding, not yet the confirmed cause): extension posts the FULL topology.json (incl. embedded `view` key) as the `load` message, and the `view` separately as `view-load` — two order-dependent messages. extension `send()` = document.getText() (unstripped); `sendView()` = extractViewText(). loadView() NO-OPS if `_lastSpec` not yet cached. The clean fix if confirmed: make store render order-independent (render when both spec+view present), or strip `view` from the load text and fold it into one render path.
- Two unanswered diagnostic questions for next session to ask the user (or capture via console): (1) how long was it blank — sub-second startup latency vs several seconds; (2) did it return on its own or only after an interaction (resize/click/mouseover). Sub-second + self-return = benign startup latency (render-as-soon-as-spec-lands fix); interaction-triggered = scene not re-rendering until forced.
- Process note for next session: 5 static-analysis subagents ran without catching a real error (cost-overrun pattern — speculating on an unverified diagnosis). NEXT STEP IS EVIDENCE, NOT MORE INFERENCE: open webview DevTools (Developer: Open Webview Developer Tools; switch to inner active-frame), reload until blank repros, capture the console error + log ordering, THEN fix.

### Working-tree state

- topology.json shows as modified (M) — node-drag positions written to its embedded `view` key (~4 lines: readGate1, inhibitRight0 x/y). Confirm intended before any commit that would sweep it up.

### Next concrete steps (in order)

1. **Phase 0 verification (BLOCKING all rf cleanup):** (a) confirm wire-pulses animate on Run; (b) capture + fix the intermittent blank-on-reload bug per the evidence-first note above. Do NOT start rf deletions until Phase 0 passes — debugging a flaky load path gets harder once modules are moving.
2. Then execute rf-retirement.md: Phase 1+2 (deletions — needs sign-off), Phase 3+4 (refactor — lands freely), Phase 5+6 (reactflow dep removal — needs sign-off).

### Separate deferred task (paused — NOT this branch)

Branch `task/inhibitright-pseudo` exists on origin:
**InhibitRightGate pseudo-text projection** (same
Input/ReadGate/ChainInhibitor pattern). Params L/R; semantic "L pass /
R inhibit" → result = `Left==1 && Right==0`. Steps: `cmd/pseudo`
subcommand (render/save) + `nodes/inhibitrightgate/SPEC.md`
`hasPseudo:true` + `handle-message.ts` handler + Go template regen of
`node.go`. **Watch:** apply the ChainInhibitor OutMulti handle-matching
lesson (suffix-strip ToNext0/ToNext1 → base ToNext) if InhibitRightGate
has multiple outputs. Paused while 3D work is in flight.

### Key files

- `tools/topology-vscode/src/webview/three/ThreeView.tsx` — the whole (sole) 3D view: node drag, edge tubes, pointer state machine.
- `tools/topology-vscode/src/webview/three/store.ts` — the single zustand source of truth (nodes/edges/selection, load/save actions).
- `tools/topology-vscode/src/webview/main.tsx` — renders only ThreeView; feeds store on load; hoisted run/save toolbar; posts { type: "ready" } to unblock host load sequence.
- `tools/topology-vscode/src/webview/save.ts`, `tools/topology-vscode/src/webview/rf/pump.ts` — read from the store now. pump.ts is LIVE in the R3F path (handleTraceEvent wired via main.tsx trace-event case; pulse-state.ts is the R3F pulse read-store; ThreeView PulseBead is the pulse renderer). NOT yet live-verified.
- `tools/topology-vscode/src/webview/rf/pulse-state.ts` — R3F pulse read-store (getPulseMap, setPulse); live R3F animation infra.
- `tools/topology-vscode/src/webview/rf/rf-imperative.ts` — FULLY ORPHANED (zero importers); pending deletion sign-off.
- `tools/topology-vscode/src/webview/rf/adapter/{spec-to-flow,flow-to-spec}.ts`, `tools/topology-vscode/src/webview/state/viewer/*` — pure adapters/state, shared and RF-free.
- `docs/planning/visual-editor/rf-retirement.md` — 6-phase rf/ retirement plan (branch-tagged); Phase 0 verification must pass before Phase 1+2 deletions.
- `docs/planning/visual-editor/rf-to-r3f-cutover.md` — note it is now partially superseded (toggle/staged-removal framing is gone; the cut happened).

Pseudo files below are for the **deferred** `task/inhibitright-pseudo` branch only, not this one:

- `tools/pseudo/chaininhibitor.go`, `tools/pseudo/readgate.go` — pseudo pattern references
- `cmd/pseudo/main.go` — pseudo subcommand dispatch
- `nodes/inhibitrightgate/{node.go,SPEC.md}` — target to regenerate / mark `hasPseudo:true`
- `tools/topology-vscode/src/extension/handle-message.ts` — handleChainInhibitor{Render,Save} + pseudoTable
- `tools/topology-vscode/src/webview/rf/PseudoPanel.tsx` — double-click-to-edit panel (deleted in Slice 3; must be re-created for this task)

### Substrate model contract (stable)

See [MODEL.md](../../../MODEL.md#slot-phase-lifecycle). Unchanged by the
3D move — going 3D is a medium change; the Go substrate,
slot-phase/AND-gate/backpressure model, and `pump.ts` firing logic stay
untouched.

## Dev-loop

After TS edit: `npm run build` from `tools/topology-vscode/`.
After Go change: `go build ./...` from repo root, `go test ./nodes/Wiring/...`.
After pseudo change (deferred branch): `go test ./tools/pseudo/...`.
To repro / inspect: clear `.probe/*.jsonl`, reload window in VS Code, Run once, inspect logs.

Check: `go test ./...`. All five guard scripts — the four boundary guards plus
`check-substrate-vocabulary` — run automatically via the Stop hook (`scripts/stop-checks.sh`).

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
