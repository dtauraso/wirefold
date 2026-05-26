# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-26, task/editor-3d-r3f-canvas — post-audit)

**Active branch:** `task/editor-3d-r3f-canvas`. Branched from
`task/editor-3d-plan` (carries the branch-local `3d-editor.md` design
spec forward). NOT merged to main.

### Why 3D

Not cosmetics. The topology genuinely has depth — the wire structures
(inhibitor chain, rings, lateral-inhibition lattices) have real
geometry that the current 2D React Flow canvas flattens into
misleading edge crossings and false adjacencies. The move is about
**representational honesty**: make the rendered structure match the
actual structure.

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

### Design pass: complete

The 3D design pass (#1–#10) is fully resolved and documented in
[3d-editor.md](3d-editor.md). All problems resolved; the deferred
minor conventions (#1 empty-space pivot, #3 badge placement/large-count
format, #10 trackpad multitouch richer gestures) are not blockers.

### Implementation: complete (in-scope spec)

The full in-scope spec has been implemented on this branch across the
following commits:

- `69184ea0` — deps: three + @react-three/fiber@9 + @types/three.
- `d0efc0f9` — ThreeView R3F canvas (2D replica, z=0) + 2D/3D toggle
  in main.tsx + subscribeRFState in rf-imperative.ts.
- `d7451397` — schema: `z` coordinate + 3D port-anchor vector offset
  (parse-spec/types/parse + spec-to-flow), validator parity confirmed;
  positions live in topology.view.json.
- `732aa4ff` — interaction grammar: perspective camera, hand-rolled
  arcball drag-rotate (incremental quaternion), scroll-dolly,
  click-pick, roll slider, floating pan-pad (200ms dwell), ^/v dolly
  buttons; gesture-discrim consts (CLICK_MAX_MS=150, DWELL_MS=200,
  MOVE_SLOP_PX=6); labels project through live camera. NO
  drei/OrbitControls.
- `b953153f` — #4 node-level wiring: click node A → click node B
  creates a real persisted edge (rfCreateEdge → rfSetEdges →
  scheduleSave, same path as 2D; first available source/target handle,
  multi-port disambiguation TODO-deferred); edges render as 3D
  TubeGeometry bezier paths with sphere-surface exit/entry.
- `437e04e4` — #7 labels: LOD (hover ∪ selected ∪ nearest-8)
  billboarded sign-post overlay; validation flag reuses
  `data.validationError` as color+emissive on sphere/torus body,
  mirrored onto the label div.
- `1a5ff5a3` — #3 occlusion: "+N" count badge on front nodes,
  recomputed on 250ms camera-settle (not per-frame); full occlusion
  allowed, no transparency/halo.
- `68819c80` — #8 pulse: always-lit emissive tubes; pulse bead travels
  along the curve driven by the SAME pulse-state.ts timing as the 2D
  edge (uniform speed 0.08 wu/ms); motion→intensity fallback is
  automatic by viewing angle.

### Post-implementation audit/fix pass (commits after v0)

Two additional commits landed after the initial 8-commit implementation:

- `391cb421` — **Cross-cutting audit + fix pass** of ThreeView.tsx (had been
  assembled by 6 narrow-view agents; several HIGH-severity bugs). Fixed:
  - **Pan & dolly used `cam.position.z` as distance-to-scene** — broke after any
    rotation; now uses true camera-to-plane / distance-to-scene-center.
  - **CameraFitter fired before node state arrived** — framed empty scene; now
    waits for nodes.
  - **Nearest-N throttle used a non-ref local** — now `useRef`.
  - **TubeGeometry + THREE.Color reallocated every render** — now memoized.
  - **Stale closure in connect-mode pointer-up** — now reads a ref.
  - Shared helpers extracted: `nodeRadius()`, `ndcToPixel()`, `pixelToNDC()`,
    `worldPerPixel()` (de-duped 3–4 copies each).
  - LabelProjector throttled to ~30 fps; phantom-hover rAF cancel; dead code
    removed.
  - Known remaining TODOs (left intentionally): `scene.traverse` pick-by-position
    fragility; roll-slider absolute-zero drift; CameraSettleDetector per-frame
    string snapshot.

- `b96d8be3` — **Moved ThreeView out of the React Flow dir.**
  `src/webview/rf/three/ThreeView.tsx` → `src/webview/three/ThreeView.tsx`.
  R3F is not React Flow. The file still imports the borrowed RF state bridge
  (`rf-imperative`, `pulse-state`, types) — that bridge stays until RF is
  actually removed.

### Architecture decision: staged RF removal (IMPORTANT — record prominently)

**RF→R3F is a STAGED REPLACEMENT, not a permanent toggle.**

R3F at z=0 is an exact replica of RF and is designed to subsume it. Full React
Flow removal is the **tracked next milestone** — but it is staged AFTER all of:

1. Live-render verification (confirmed working in real VS Code editor use).
2. R3F gets its **own load/save/state pipeline** (currently borrows RF's
   `rf-imperative` bridge — that bridge must be replaced before RF is removed).
3. Feature parity confirmed.

Only then: delete the 2D/3D toggle, the React Flow node/edge components, and the
`reactflow` dependency, and rename the `rf-*` exports.

**Do NOT remove RF before R3F is proven in real use — RF is the fallback until
then.**

### Verified

`npm run build` clean; `go build ./...` + `go test ./...` pass;
`scripts/stop-checks.sh` (all guards + substrate-vocabulary) pass.
Security: `npm audit` 0 vulns, no unsafe sinks
(no dangerouslySetInnerHTML/eval/innerHTML) in the 3D code, CSP
unchanged (script-src 'nonce-…', no unsafe-eval), publishers
confirmed.

### Deliberately deferred (not built; record so next session doesn't think they're missed)

The spec marks these deferred — they are NOT implementation gaps:

- **Fold-node primitive (#9):** new node kind FoldNode.tsx + registry +
  Go `nodes/Fold/`; design is settled (independent interface, fed-value
  output, simple-subgraph constraint), build deferred.
- **What drives z:** structural rank/ring/lattice z-derivation — z=0
  for all nodes currently; deferred until friction.
- **Trackpad multitouch, SpaceMouse, touch bindings** — deferred per
  #10.
- **User-saved camera snapshots** — deferred per #6.
- **Occlusion badge edge-cases** — edge-on counting, placement when
  front partly occluded, large-count cap (TODO left in code).
- **Pulse end-on direction-at-a-glance** — accepted residue.
- **Multi-port disambiguation (#4)** — first available handle used;
  disambiguation TODO in code.

### Next concrete step

1. **LIVE-RENDER check** — reload the VS Code window, click the top-right
   "3D view" toggle, exercise rotate/dolly/pick/wire/labels/badge/pulse.
   **Exercise pan and dolly-after-rotation specifically** — that was just fixed
   (pan & dolly used `cam.position.z`, broke after any rotation). Building ≠
   running; this is the first thing to confirm.
2. Once live-render is verified, work toward the **staged RF removal milestone**:
   give R3F its own load/save/state pipeline (replace the borrowed `rf-imperative`
   bridge), confirm feature parity, then delete the 2D/3D toggle + RF components +
   `reactflow` dep. Do NOT remove RF before R3F is proven in real use.
3. Deferred: fold-node primitive (#9), merge to main (run
   `tools/strip-branch-local-docs.sh task/editor-3d-r3f-canvas` first).

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

- [3d-editor.md](3d-editor.md) — full 3D design (branch-local; does NOT ride the merge)
- `tools/topology-vscode/src/webview/three/ThreeView.tsx` — the whole 3D view (moved out of `rf/` by `b96d8be3`)
- `tools/topology-vscode/src/webview/rf/rf-imperative.ts` — subscribeRFState, rfCreateEdge
- `tools/topology-vscode/src/webview/rf/pulse-state.ts` — pulse timing consumed by 3D
- [`memory/project_interaction_control_is_substance.md`](../../../memory/project_interaction_control_is_substance.md) — control-is-substance anti-drift principle (rides to main)
- `tools/topology-vscode/src/webview/rf/` — 2D React Flow editor (node registry, `SubstrateEdge.tsx`, RF store) that coexists with the 3D view
- `pump.ts` — pump firing + pulse-animation logic; stays put (only the geometry pulses travel over changes)
- `tools/topology-vscode/src/schema/parse-spec.ts` — node position model (has `z`) + 3D port-anchor model

Pseudo files below are for the **deferred** `task/inhibitright-pseudo` branch only, not this one:

- `tools/pseudo/chaininhibitor.go`, `tools/pseudo/readgate.go` — pseudo pattern references
- `cmd/pseudo/main.go` — pseudo subcommand dispatch
- `nodes/inhibitrightgate/{node.go,SPEC.md}` — target to regenerate / mark `hasPseudo:true`
- `tools/topology-vscode/src/handle-message.ts` — handleChainInhibitor{Render,Save} + pseudoTable
- `tools/topology-vscode/src/webview/rf/PseudoPanel.tsx` — double-click-to-edit panel

### Substrate model contract (stable)

See [MODEL.md](../../MODEL.md#slot-phase-lifecycle). Unchanged by the
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
