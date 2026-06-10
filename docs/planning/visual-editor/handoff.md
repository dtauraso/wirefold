---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-10 — task/go-backend-ts-frontend, pushed; handshake FIRING shipped + verified live; redesign doc demoted to branch-local scratch; per-goroutine GEOMETRY is next, verified by file confirmations)

- Active branch: `task/go-backend-ts-frontend`, pushed. Latest commit `ad438591`. Tree clean except pre-existing untracked `north-seattle-parks.csv` and `.vscode/settings.json`.
- **`topology.json` has git skip-worktree SET.** Deliberate edits: `git update-index --no-skip-worktree topology.json` → stage → commit → re-set. No topology.json change this session.
- Prior 5-phase Go-authoritative rewrite + halted-start + handshake WIRING remain DONE. This session shipped handshake FIRING and hardened the redesign doc.

### Commits this session (`git log --oneline bc96a022..HEAD`)

```
ad438591 refactor(webview): delete dead geometry/clock helpers (portWorldPos, getPauseAdjustedNow)
0ed7be70 docs(redesign): Table 4 — mark SingleEdgeTube/PulseBead as TS renderers, not moved-to-Go
c8ff7b5e docs(redesign): responsive spec-tables — fit page, wrap cells (drop fixed px + nowrap)
4f4ea3bd docs(redesign): add i0 poll-vs-blocking-recv row to Inconsistencies
b2faf041 docs(redesign): add Inconsistencies tab (code↔spec drift table)
e7888943 docs(redesign): add Source-file column to goroutines + TS-context tables
e41f3bcc feat(net): handshake firing — in08↔i0 feedback ring, SEND→READ ordered
```

### What that covers

- **Handshake firing (send→read).** in08: send `Init[i]` → recv `FeedbackIn s` → `i=(i+s)%len(Init)` (send precedes read; the first emit seeds the ring). i0 (ChainInhibitor): held init −1; on recv v: v≠held → set held + send 1 on `FeedbackOut`, else send 0; forward held to i1. Both gated on the new port being wired (plain Inputs / i1 unchanged). Firing test added. **All 3 edges animate live (David confirmed):** in08 alternates 0,1,0,1; i0 sends 1 on FeedbackOut each cycle.
- **Redesign doc `docs/go-authoritative-clock/index.html` hardened:** Source-file column on the goroutines (.go) + TS-contexts (.ts/.tsx) tables; new **Inconsistencies tab** (2 rows — Input loop read→send vs shipped send→read; i0 poll-vs-blocking-recv); Table 4 overclaim fix (`SingleEdgeTube`/`PulseBead` = TS renderers, not moved-to-Go); responsive table CSS (width:100%, % columns, `overflow-wrap:anywhere` — prose wraps at spaces, long tokens break).
- **Deleted dead TS helpers** `portWorldPos`, `getPauseAdjustedNow` (verified zero callers; tsc/vitest/build green).

### Why / key corrections this session

- **"Deadlock" was challenged and verified real.** Paced `TryRecv` BLOCKS (`paced_wire.go` `Recv` selects on `slotReadyCh`, no default; live ports built via `NewInPaced`). So read-before-send WOULD deadlock at t=0 → send→read is required. The bootstrap-seed-node idea was a false dilemma: in08 is a source, so emitting before its first read seeds the ring.
- **Geometry is the lone central emission.** Go emits ONLY edge curves (`loader.go:212` at load + `stdin_reader.go:190` `NodeMoveRegistry` on node-move). It **never emits node positions or port positions/dirs**. Beads (`paced_wire.go:441` Position) + firing events are already per-goroutine. Node-move is applied **centrally** (`stdin_reader` → `NodeMoveRegistry.applyNodeMove`), not routed to node goroutines.
- **David's model (invariant):** "each goroutine sends things to TS and TS sends things the goroutine picks up." The central geometry emitter violates this; geometry should be per-goroutine.

### The settled split (model — unchanged)

Go owns the diagram (nodes/edges/beads, 3D math, clock+pulseSpeed, shading, fade). TS owns the viewpoint (camera, projection, raycast-picking, GPU render). Store is a no-op holder; Go is sole writer. Bridge = JSONL over stdio, fire-and-forget TS→Go.

### Active experimental network — 3 EDGES, all firing

`topology.json`: `in08` (Input init `[0,1]`), `i0` (ChainInhibitor), `i1` (ChainInhibitor). Edges: `in08→i0` (ToReadGate→FromPrev), `i0→i1` (ToNext0→FromPrev), `i0→in08` (FeedbackOut→FeedbackIn). All three animate.

### ⚠️ WHAT DAVID NEEDS TO CHECK / DECIDE

1. **Redesign doc render:** open `docs/go-authoritative-clock/index.html` — confirm the Inconsistencies tab switches, Source-file columns read well, no table overflows the page.
2. **GO/NO-GO on the next substrate step (below).** Stated and waiting per the model rule.

### OPEN ITEMS / NEXT

1. **Per-goroutine GEOMETRY emission — next substrate step (stated, awaiting David's go).** Problem: Go never emits node/port world positions, so TS recomputes them for viewpoint work — LIVE helpers `nodeWorldPos` (camera pivot), `portDir` (port spheres), `boundingBox` (camera-fit), `nodeTopWorldPos` (labels). Per David's model + the doc's "delete TS duplicates" intent:
   - **NEXT SINGLE STEP:** each node's goroutine emits its node + port world positions/dirs as a NEW trace event (Go→TS), wired with trace-kind parity (`check-trace-kind-parity`; Go trace-kinds ↔ TS `trace-kinds.ts`). Then TS camera/label/port code reads Go's positions; the 4 helpers collapse to thin readers or go away.
   - **After that (state each as a single step, do NOT dump a multi-step plan):** move the central edge-curve emitter (`loader.go`) into per-goroutine emission; route node-move (`stdin_reader` → `NodeMoveRegistry`) to the owning node goroutine ("TS sends things the goroutine picks up").
   - **Geometry progress verified by FILE CONFIRMATIONS against code** (grep-backed one-liners: caller counts, emit site file:line). Authoritative sources = MODEL.md + CLAUDE.md + the code. The HTML doc (`docs/go-authoritative-clock/index.html`) is branch-local scratch — not kept in sync, stripped at merge.
2. **`requiredInputs` cosmetic** (open): gen-node-defs auto-lists `FeedbackIn` in requiredInputs/REQUIRED_INPUTS (dead metadata, no consumer). Fix with an optional-port SPEC annotation.
3. **Merge:** run `tools/strip-branch-local-docs.sh task/go-backend-ts-frontend` (redesign doc is branch-local, no exceptions); needs explicit sign-off.

### Carry-forward facts

- **Paced `TryRecv` BLOCKS** (not a poll) despite the `if v, ok := …TryRecv(); ok` idiom — `ok=false` only on ctx-cancel. Judge recv semantics from the `paced_wire.go` impl, not the call-site idiom (it misled a subagent this session). Saved to memory `feedback_paced_tryrecv_blocks`.
- **Per-goroutine bridge:** each goroutine sends to TS / picks up TS input; avoid central emitters/handlers. Saved to memory `feedback_per_goroutine_bridge`.
- **Two-process editor:** reopen-file reloads only the webview; **Developer: Reload Window** reloads the extension host (Go spawn). gopls goes stale — `go test`/`go run` authoritative.
- **Node contract:** nodes do local work + drive outputs; no TCP-handshake/ack-nack/send-gating.
- MODEL.md + CLAUDE.md are the authoritative model. `docs/go-authoritative-clock/index.html` is branch-local scratch — it drifts (e.g. listed deleted `portWorldPos` as live), stripped at merge, not hand-maintained.

### Dev-loop

- After Go: `go build ./...` + `go test -race ./...`; after TS (from `tools/topology-vscode/`): `npm run build` + `npx tsc --noEmit` + `npx vitest run`; run guard scripts; `tools/check-generated.sh` + `check-trace-kind-parity` after trace-kind/SPEC/port changes.
- Exercise editor changes: Developer: Reload Window.
- Concision: prose blocks under ~40 words — bullets/tables/one-liners.
- Active topology in08/i0/i1 (3 edges, all firing). topology.json is skip-worktree.
- No merge to main without explicit sign-off. Delete merged branches without re-asking.

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
