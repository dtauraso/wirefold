---
branch: task/code-smell-cleanup
---

# Code-smell cleanup — remaining phases

Tracks the unfinished phases of the full project code-smell audit. Phases A–D
(Go correctness, dead code, duplication, structural) are DONE and pushed on
`task/code-smell-cleanup`. Phases E–I remain.

Per-phase rule: each item is its own commit; behavior must not change unless
noted. Go phases verify with `go build ./... && go test -race ./...` then
`bash scripts/stop-checks.sh` (exit 0). TS phases additionally need
`cd tools/topology-vscode && npx tsc --noEmit && rm -f out/webview.js && npm run build`.
Serialize committing subagents on this branch (no parallel committers — they
trample the working tree). Do NOT push without sign-off for a merge to main;
pushing the task branch itself is fine.

---

## Phase E — Go topology de-hardcode  (DECISION REQUIRED)

- **loader.go:240–294 — hardcoded topology node IDs in the aimed-port registry.**
  The block hardcodes node IDs `"1".."10"` + port names and uses confusing
  aliases (`has2 = centers["5"]`). For any topology other than the one loaded,
  aimed ports silently fall back to unaimed. This is a MODEL.md drift violation
  (topology-shape knowledge embedded in generic loader infrastructure).
- **Open question (resolve before coding):** what should "aimed port" derive
  from? Candidates: (a) every edge-connected port aims at the node on the other
  end (fully derived from the edge list, generalizes to any topology); or
  (b) an opt-in spec flag on the port/node (requires a schema change per
  CLAUDE.md primitive-landing rule — wire-defs/node-defs generated from SPEC.md).
- **Next step:** read-only investigation of the aimed-port mechanism
  (AimedPortRegistry, port_geometry.go, aimed_ports.go) to recover design
  intent, THEN decide / confirm the rule with David before editing. Verify the
  current demo topology's resulting registry is unchanged by the new derivation.

---

## Phase F — TS dead code

- `webview/three/polar.ts` — dead rotation/zoom half (~70 lines): `arcAxisAngle`,
  `angleAboutAxis`, `rotateAboutAxis`, `scaleRadius` (Go now owns orbit/zoom; zero
  call sites). (#5)
- `webview/three/interaction-handlers.ts:43–67` — dead AND drift-shaped Go-formula
  mirror `computeLargeSphereRadius` / `constrainInsideLargeSphere` (zero call sites). (#6)
- `webview/three/viewpoint-bridge.ts:87–94` — unused `sendViewpointZoom`.
- `webview/three/store.ts:185–202` — no-op `moveNode` and `saveSpec` (`/* no-op */`,
  no external callers).
- `webview/three/interaction-controls.ts:43` — `ARCBALL_FILL` referenced only in comments.

## Phase G — TS duplication

- Three world↔polar-angle conventions coexist: `polar.ts fromWorld`,
  `viewpoint-bridge.ts:17–21 worldDirToAngles`, `NavGuides.tsx` inline → one helper.
- Camera-fit math duplicated: `scene-camera.tsx CameraFitter:51–69` and
  `camera-ui.tsx HomeButton:352–379` (comment admits "matches HomeButton's math").
- Scene center/radius computed four ways: `computeContentSphere`
  (interaction-controls), `sceneCenter`/`regionFocus`/`ensureTarget`
  (interaction-handlers), `boundingBox` (geometry-helpers), HomeButton inline AABB.
- `webview/three/geometry-helpers.ts:96–134` — `portDirLocal`/`nodeRadiusLocal`
  reimplement Go ring-anchor math as a "pre-emit fallback" (drift; second source of
  truth). Prefer a "no port until emit" placeholder over a divergent mirror.
- Segment-key + null-memo block duplicated between `SingleEdgeTube`
  (scene-graph.tsx:332–359) and `DoubleEdgeOverlay` (419–447).

## Phase H — TS structural

- `webview/three/NavGuides.tsx:401/408/409` — hardcoded node ids `"5"/"7"/"8"`
  for θ/φ-lock arcs (`TODO(fix5)` admits hand-sync, breaks on rename). De-hardcode. (#4)
- `webview/three/scene-content.tsx:57–203` — `RaycasterHelper` 140-line god pick
  function (5 option-mode branches, full `scene.traverse` per pick).
- `webview/three/interaction-controls.ts:260–265` — `schedulePortAnchor` wraps
  `makeRafThrottle` in `useCallback` with `flushPortAnchor` dep, so it's recreated
  and can drop an in-flight pending frame (breaks latest-wins; other three throttles
  use stable `useRef`).
- `runCommand.ts` (~line 220) — unconditional `console.log` per trace event
  (~60 Hz/wire), no dev guard → floods the console.
- Magic numbers in `interaction-handlers.ts` (`ZOOM_BASE`, `MIN_DIST`,
  `TARGET_MIN`/`FOCUS_MIN`, `CLICK_MAX_MS`, `MOVE_SLOP_PX`) → hoist to named consts.
- Repeated `eslint-disable react-hooks/exhaustive-deps`
  (scene-content.tsx:200, scene-camera.tsx:73, scene-graph.tsx:358) — audit each
  for genuine missing-dep bugs.
- (Lower) `store.ts load` swallows parse failure into a blank diagram (logged only
  to `.probe`); `ThreeView.tsx:98–118` keydown re-subscribes on every edges change.

## Phase I — Schema / bridge parity

- `schema/node-data-types.ts:34–60` — `parseNodeData` covers only 4 of the 8
  `RUNTIME_IMPLEMENTED_KINDS`; missing `HoldFlip`, `Pulse`,
  `WindowAndInhibitLeftGate`, `WindowAndInhibitRightGate`. Parity gap
  (`feedback_schema_parser_parity`). Note this file is generated by `gen-node-defs`
  — fix likely belongs in the generator, not the output. (#8)
- **Bridge doctrine #9 (DECISION):** `messages.ts` `EditMsg` has ~16 ops (was 19;
  set-origin removed in Phase B), far beyond the "single edit op = create/update/
  delete/fade" promised by MODEL.md/CLAUDE.md. Either formalize the real surface in
  MODEL.md + the messages.ts header comment, or refactor. Confirm direction with David.
- `extension/handle-message.ts:109–147` — `edit` dispatch is an if/else-if chain
  with no fallback; an op valid in messages.ts + Go but missing here silently
  no-ops. Add an exhaustive fallback that logs, and a guard so the dispatch layer
  stays in op-parity (the existing `check-edit-op-parity.sh` only covers TS↔Go).
- `handle-message.ts:27–30` — `NO_PAYLOAD_TOGGLE_OPS` runtime `Set` is a partial
  duplicate of the `parseEdit` switch; divergence silently drops messages.
- `NodeDef` (node-defs.ts) vs `NodeTypeDef` (types-graph.ts) — two parallel type
  defs bridged by manual `defToTypeDef` (node-types.ts) with no coverage guard.
  Add a guard or unify.
- (Done in Phase B: stale `set-origin`/stdin_reader bridge-shape comments.)
