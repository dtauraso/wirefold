# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-10 — task/go-backend-ts-frontend, pushed; Go-authoritative rewrite DONE; halted-start lifecycle + handshake WIRING shipped; handshake FIRING pending one decision)

- Active branch: `task/go-backend-ts-frontend`, pushed. Latest commit `49afda02`. Tree clean except pre-existing untracked `north-seattle-parks.csv` and an untracked `.vscode/settings.json` that appeared during subagent runs (uncommitted — remove if unwanted).
- **`topology.json` has the git skip-worktree bit SET.** Deliberate edits: `git update-index --no-skip-worktree topology.json` → stage → commit → re-set `--skip-worktree`. Bit is currently SET.
- The 5-phase Go-authoritative rewrite remains DONE/verified (prior handoff). This session built on top of it.

### What shipped THIS session (2 commits)

| Commit | What |
|---|---|
| `6e0248ab` | **Halted-start lifecycle.** Go starts with the clock `Halt()`'d; `LoadTopology` still emits geometry, so edges render at editor-open (static diagram) with no Run. `play`/`pause` stdin control messages route to the clock's global gate (`Clock.Halt/Resume`), retiring external SIGSTOP/SIGCONT. Extension spawns Go on the `ready` handshake; the Run button resumes the clock instead of spawning. New `TestHaltedStartGeometryOnlyNoPositions`. |
| `49afda02` | **Handshake wiring.** Added ports Input `FeedbackIn` (in) + ChainInhibitor `FeedbackOut` (out), both `chain` kind. Wired the 2 missing spec'd edges into `topology.json`: `in08→i0` (forward Init value, existing ports) + `i0→in08` (1/0 feedback, new ports). All 3 spec'd edges now render (Go emits 3 geometry events at load). Ports are geometry-only — NO firing yet; unwired-safe (dead-end channels), no ring deadlock. |

### Why (David's correction this session)

"Edges need to be present in the same way the nodes are present" + "I need all the edges for the nodes as specified in the specs." Two root causes, both fixed:
1. Edges absent at load → Go spawned only on Run-click. Fixed by halted-start (spawn on open, geometry at load).
2. Only 1 edge in `topology.json` vs 3 spec'd → wired the 2 missing handshake edges.

### The settled split (the model — unchanged)

- **Go owns the diagram** — nodes/edges/beads, 3D math (curve control points, arc length, node/port world positions, per-frame bead positions), animations, clock + pulseSpeed, shading params (33 codegen'd), per-wire fade flag.
- **TS owns the viewpoint** — camera, scene navigation, projection, raycast-picking, GPU render. "TS computes none" means none of the *diagram*; viewpoint math is TS's.
- **Input split:** navigation input (pointer/wheel) stays TS; only action input (CRUD carrying the picked Go id) goes to Go.
- **Store:** zustand store is a no-op holder — Go is the sole writer.
- **Bridge:** JSONL over stdio. Go→TS: trace stream (positions, node events, edge curves, shading params). TS→Go: spec save/load + one `edit` op (create/update/delete/fade) + `play`/`pause` control. Fire-and-forget (`check-no-await-on-bridge.sh`).

### Active experimental network — NOW 3 EDGES

`topology.json`: nodes `in08` (Input, init `[0,1]`), `i0` (ChainInhibitor), `i1` (ChainInhibitor). Edges (3, all `chain`):
- `in08ToI0`: in08.ToReadGate → i0.FromPrevChainInhibitorNode
- `i0ToI1`: i0.ToNext0 → i1.FromPrevChainInhibitorNode
- `i0FeedbackToIn08`: i0.FeedbackOut → in08.FeedbackIn

New ports `in08.FeedbackIn` / `i0.FeedbackOut` exist but are GEOMETRY-ONLY (no firing). `topology.inactive.json` is a DIFFERENT/fuller exploration (ReadGate + InhibitRightGate) — NOT this net's spec.

### ⚠️ WHAT DAVID NEEDS TO CHECK

1. **Reload the WINDOW** (Developer: Reload Window — NOT reopen-file) → confirm all 3 edges render at load, static, no Run. (Reopen-file reloads only the webview; the extension host — Go-spawn-on-open — reloads only on window reload.)

### OPEN ITEMS / NEXT

1. **Handshake FIRING — pending; rules pinned; ONE decision open (awaiting David).** Both kinds gain behavior ONLY when the new port is wired (i1 / plain Inputs unchanged):
   - **i0**: held value, init **−1**; on recv `v`: `v≠held` → set held, send **1** on `FeedbackOut`; else send **0**; always forward held to i1.
   - **in08**: read `FeedbackIn` `s` → `i=(i+s)%len(Init)` → send `Init[i]` on `ToReadGate`.
   - **OPEN DECISION — ring startup seed:** in08 reads feedback before sending; i0 sends only after receiving → deadlock at t=0 (handoff loop spec omits the seed). **(B) in08 self-seeds** — emit `Init[0]` once, then read-loop; no extra node; in08 is a source — *assistant's lean*. **(A) dedicated bootstrap Input node** → in08.FeedbackIn (the established ring-seed convention; adds a 4th node). David picks; also confirm i0 held-init −1. Then build firing (conditional-on-wiring for both kinds) + a firing test.
2. **`requiredInputs` cosmetic:** gen-node-defs auto-lists `FeedbackIn` in `requiredInputs`/`REQUIRED_INPUTS` (dead metadata — required-input enforcement removed 2026-06-01; no consumer). Fix with an optional-port SPEC annotation when firing lands.
3. **Redesign doc → MAIN (David's request, 2026-06-10):** the branch spec `docs/go-authoritative-clock/index.html` is to ride to MAIN as a permanent "major redesign" doc — a *before* section with diagrams of the previous (TS-driven) clock system, then the new Go-authoritative model. EXCEPTION to the branch-local-docs strip rule. Do AFTER the code fixes.
4. **Merge:** run `tools/strip-branch-local-docs.sh task/go-backend-ts-frontend` before merge (but KEEP the redesign doc per #3); needs explicit sign-off.

### Carry-forward facts

- **Two-process editor model (cost hours this session):** VS Code webview and extension host are SEPARATE processes. Reopen-file reloads only the WEBVIEW (`out/webview.js`). The extension host (`out/extension.js` — trace parsing, Go spawn) reloads only on **Developer: Reload Window**. `npm run build` refreshes on-disk bundles but NOT the running host. gopls also goes stale — `go test`/`go run` authoritative. (Also saved to memory `feedback_two_process_editor_reload`.)
- **Debug doctrine that worked:** probe logs (`.probe/*.jsonl`, unfiltered) over static theory; trace the event hop-by-hop. Three static theories died before runtime evidence settled the "no edges" cause: Go was simply never spawned (editor spawned only on Run-click; now fixed).
- The node-contract principle stays UNPINNED (totality in / no guarantees out; reliability in the node, not the channel). Do NOT re-pitch pinning it.
- Spec doc `docs/go-authoritative-clock/index.html` is the settled planning record; MODEL.md + CLAUDE.md are the authoritative model.

## Dev-loop

- After Go changes: `go build ./...` + `go test -race ./...`; after TS: `npm run build` from `tools/topology-vscode/` (refreshes `out/webview.js` AND `out/extension.js`) + `npx tsc --noEmit` + `npx vitest run`; run all 8 guard scripts; `tools/check-generated.sh` after shared `CurveParam*`/`ShadingParam*`/SPEC.md/port changes.
- **To exercise editor changes live:** Developer: Reload Window (reloads the extension host, not just the webview).
- **Concision rule:** prose blocks under ~40 words — bullets/tables/one-liners.
- **Active topology** is `in08`/`i0`/`i1` (3 edges). `topology.json` is skip-worktree — clear the bit before deliberate edits, re-set after.
- Branch hygiene: no merge to main without explicit sign-off. Delete merged branches without re-asking.
- Unrelated friction → log to `docs/planning/visual-editor/session-log.md`, open a fresh `task/<short-kebab>`.

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
