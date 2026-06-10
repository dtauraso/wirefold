# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-09 — task/go-backend-ts-frontend, pushed; Go-authoritative rewrite COMPLETE — 5 phases verified green)

- Active branch: `task/go-backend-ts-frontend`, pushed, tree clean (pre-existing untracked `north-seattle-parks.csv` only — not part of this work). Latest pushed commit: `d729ad62`.
- **NOTE:** `topology.json` has the git skip-worktree bit SET — editor churn stays out of git. Deliberate changes require `git update-index --no-skip-worktree topology.json` first, then re-set after.
- **The code now matches MODEL.md.** All 5 spec phases shipped and verified green on this branch. Prior handoffs said "code implements the OLD model" — that is no longer true.

### What shipped (5 commits)

| Commit | Phase | Key change |
|---|---|---|
| `b87eb731` | 1 — Clock into Go | Injectable clock (real+fake, pause-aware); each wire times its own delivery at `inFlightTime`; TS `NotifyDelivered` delivery-trigger removed |
| `e365f030` | 2 — Position stream | Go emits per-frame bead positions (bézier eval factored out of `PortCurveArcLength`); TS plots only; TS position math deleted |
| `8e4b6532` | 3 — Geometry into Go | Node/edge geometry authoritative in Go; streams curves; in-flight re-derive on edit; delete-mid-flight cancel + `pulse-cancelled` echo; TS renders Go's curve |
| `47d7c54e` | 4 — Shading into Go | 33 material/glass/env params moved to Go (codegen, like curve-params); TS applies Go params; GPU machinery stays TS; appearance preserved bit-for-bit |
| `d729ad62` | 5 — Bridge→CRUD + docs | One fire-and-forget `edit` op (create/update/delete/fade) + pause/resume; `notifyDelivered` removed entirely; MODEL.md + CLAUDE.md finalized to the shipped model |

### How it was verified (every phase, confirmed in main session — not just subagent claims)

- `go build` + `go test -race ./...`
- All **8 guard scripts**: `check-trace-kind-parity`, `check-message-kind-parity`, `check-no-ts-timers`, `check-slot-phase-boundary`, `check-ts-computes-no-geometry` (new), `check-ts-shading-from-go` (new), `check-no-await-on-bridge` (new), `check-generated`
- `npx tsc --noEmit`; `vitest` 56/56
- Per-phase deterministic verifiers: headless cascade, golden position parity, geometry re-derive, shading params, save/load round-trip

Note: IDE/gopls diagnostics went stale after each phase — `go test` is authoritative.

### The settled split (the model — now in code)

- **Go owns the diagram** — nodes/edges/beads, 3D math (curve control points, arc length, node/port world positions, per-frame bead positions), animations, clock + pulseSpeed, shading params (33 codegen'd), per-wire fade flag.
- **TS owns the viewpoint** — camera, scene navigation (orbit/pan/zoom), projection, raycast-picking, GPU render. TS keeps r3f/three.js for 3D scene-navigation machinery; control feel bespoke (substance — not a controls library). "TS computes none" means none of the *diagram*; viewpoint math is TS's.
- **Input split:** navigation input (pointer/wheel for camera) stays TS; only **action input** — CRUD carrying the picked Go id — goes to Go.
- **Store:** zustand store is a **no-op holder** — Go is the sole writer of its data.
- **Bridge:** JSONL over stdio; one `edit` op (create/update/delete/fade) + pause/resume; one Go id space; extension host is a dumb pipe.

### Active experimental network — UNCHANGED

`topology.json` holds only `in08` (Input, init `[0,1]`), `i0` (ChainInhibitor), `i1` (ChainInhibitor) + edge `i0ToI1`. `topology.inactive.json` (editor never reads it) holds inactive nodes/edges.

### ⚠️ WHAT DAVID NEEDS TO CHECK (manual end-checks — deterministic halves already green)

1. **Live-editor litmus:** open `topology.json` in the editor, Run, confirm pulses animate from Go's position stream. Detach the editor → Go keeps running the net (headless half proven by `TestHeadlessCascadeCompletes`); reattach → it draws what Go is doing.
2. **Phase-4 pixel fidelity:** confirm nodes / wires / beads / environment look unchanged (shading values moved bit-for-bit, but eyes are the judge).
3. **Save/load through the VS Code UI** end to end (the file round-trip test is green; the UI path is the manual part).

### OPEN ITEMS / NEXT

1. **in08↔i0 handshake — still UNBUILT** (separate from this rewrite). `topology.json` still has only the `i0→i1` edge. Needs: new ports (Input feedback-in, ChainInhibitor feedback-out), two wires (`in08→i0`, `i0→in08`), `i0` held init `-1`. Loop specs:
   - `in08`: read signal on `i0→in08` (1/0); `i = (i + signal) % len(Init)`; send `Init[i]` on `in08→i0`.
   - `i0`: held init `-1`; if arriving value DIFFERS from held → set held, send `1` to `in08`; else send `0`; forward held to `i1`.
   - `i1`: ordinary ChainInhibitor; sink (`ToNext` unwired); sends nothing.
   - Spec'd in spec doc Goroutines tab; none exists in code yet.
2. **Minor follow-up:** in-flight delivery goroutines wait on a background context (cancelled by Reset/Delete), not the run-ctx — bounded/harmless; tie to run-ctx if you want it tidier.
3. **Merge:** run `tools/strip-branch-local-docs.sh task/go-backend-ts-frontend` before merging to main; needs explicit sign-off.

### Carry-forward facts

- Spec doc `docs/go-authoritative-clock/index.html` is the settled planning record (zero open items, deterministic verifiers); MODEL.md + CLAUDE.md are now the authoritative model.
- The node-contract principle is deliberately UNPINNED (totality in / no guarantees out; reliability lives in the node, not the channel). Do NOT re-pitch pinning it to MODEL.md/memory.

## Dev-loop

- After Go changes: `go build ./...` + `go test -race ./...`; after TS: `npm run build` from `tools/topology-vscode/` (refreshes `out/webview.js`) + `npx tsc --noEmit` + `npx vitest run`; run all 8 guard scripts; `tools/check-generated.sh` after shared `CurveParam*` / `ShadingParam*` / SPEC.md changes.
- **Concision rule:** keep prose blocks under ~40 words — bullets, tables, or one-line claims only.
- **Active topology** is `in08`/`i0`/`i1`. `topology.json` is skip-worktree — deliberate edits require clearing the bit first.
- Branch hygiene: no merge to main without explicit sign-off. Delete merged branches without re-asking.
- If user surfaces unrelated friction, log to `docs/planning/visual-editor/session-log.md` and open a fresh `task/<short-kebab>`.

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
