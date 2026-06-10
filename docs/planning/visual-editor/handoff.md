---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-10 — task/go-backend-ts-frontend, pushed; per-goroutine node-geometry DONE (both Go + TS halves, verified); edge-curve per-goroutine emit is next)

- Active branch: `task/go-backend-ts-frontend`, pushed. Latest commit `440a7daa`. Tree clean (topology.json skip-worktree; north-seattle-parks.csv moved to ~/Desktop; .vscode/ gitignored).
- **`topology.json` has git skip-worktree SET.** Deliberate edits: `git update-index --no-skip-worktree topology.json` → stage → commit → re-set. No topology.json change this session.
- Prior 5-phase Go-authoritative rewrite + halted-start + handshake WIRING + handshake FIRING remain DONE. This session shipped per-goroutine node-geometry (both halves).

### Commits this session (`git log --oneline e41f3bcc..HEAD`)

```
440a7daa chore: gitignore .vscode/ editor settings
15adf9ff feat(webview): consume node-geometry — 4 geometry helpers read Go's emitted node/port positions (fallback to local compute pre-emit)
38a278d0 feat(net): per-goroutine node-geometry emit — each node emits its node+port world positions/dirs on startup (TS consume next)
7ca50c02 docs(redesign): add Whats-left tab — remaining geometry + cleanup work
d782f3e0 docs(handoff): demote redesign doc to branch-local scratch; geometry verified by file confirmations
```

### What that covers

- **Per-goroutine node-geometry emission — DONE (both halves).**
  - **Go (`38a278d0`):** new `KindNodeGeometry`/`node-geometry` trace event. Each node's goroutine emits its node center + per-port world positions/dirs on startup via an `EmitGeometry func()` closure injected by `reflectBuild` (mirrors the `Fire` pattern), called once at top of each node kind's `Update` (`input`, `chaininhibitor`, `inhibitrightgate`, `readgate`). Math reuses `port_geometry.go` (`nodeWorldPos`/`portWorldPos`/`portDir`) — no duplication. MODEL.md got a one-line contract addition (node owns its geometry emission; wires still own bead-position emission). New Go test asserts emit.
  - **TS (`15adf9ff`):** new `useNodeGeometryStore` (`node-geometry.ts`, mirrors `edge-geometry.ts`); `pump.ts` consumes `node-geometry` as a pure store-write (drift guard clean); all 4 geometry helpers (`nodeWorldPos`, `portDir`, `nodeTopWorldPos`, `boundingBox`) flipped to READ Go's emitted positions, with local-compute fallback during the pre-emit startup race. Frame verified: no coordinate flip (Go mirrors TS y-down→y-up).
  - **Correction:** an earlier explore wrongly thought `nodeTopWorldPos`/`boundingBox` were dead. They are LIVE — callers in `scene-content.tsx` L194 + L579. All 4 helpers were flipped.
  - All green: `go build`, `go test -race`, `tsc --noEmit`, 56/56 vitest, `check-trace-kind-parity`, `check-no-await-on-bridge`, `check-ts-computes-no-geometry`.
- **Redesign doc demoted (`d782f3e0`)** to branch-local scratch. `docs/go-authoritative-clock/index.html` is NOT main-bound; `strip-branch-local-docs.sh` strips it at merge, no exceptions. A "What's left" tab was added to it for reference only.
- **Housekeeping:** `.vscode/` added to `.gitignore`; north-seattle-parks.csv moved out of repo.

### The settled split (model — unchanged)

Go owns the diagram (nodes/edges/beads, 3D math, clock+pulseSpeed, shading, fade). TS owns the viewpoint (camera, projection, raycast-picking, GPU render). Store is a no-op holder; Go is sole writer. Bridge = JSONL over stdio, fire-and-forget TS→Go.

### Active experimental network — 3 EDGES, all firing

`topology.json`: `in08` (Input init `[0,1]`), `i0` (ChainInhibitor), `i1` (ChainInhibitor). Edges: `in08→i0` (ToReadGate→FromPrev), `i0→i1` (ToNext0→FromPrev), `i0→in08` (FeedbackOut→FeedbackIn). All three animate.

### OPEN ITEMS / NEXT

1. **NEXT SINGLE STEP — move central edge-curve emitter to per-goroutine.** `loader.go:212` emits edge curves centrally at load time. Per David's model, each goroutine should emit its own geometry. Move the edge-curve `node-curve` trace event emission into the wire's goroutine (mirrors the node-geometry pattern).
2. **After that — route node-move to owning goroutine.** `stdin_reader.go` `applyNodeMove` / `NodeMoveRegistry ~L190` handles node-move centrally. Route to the owning node goroutine ("TS sends things the goroutine picks up").
3. **`requiredInputs` cosmetic (open):** gen-node-defs auto-lists `FeedbackIn` in `requiredInputs`/`REQUIRED_INPUTS` — dead metadata, no consumer. Fix with an optional-port SPEC annotation.
4. **Merge:** run `tools/strip-branch-local-docs.sh task/go-backend-ts-frontend` (strips redesign doc + any other branch-local docs); needs explicit sign-off.

**Geometry verified by FILE CONFIRMATIONS against code** (grep-backed: caller counts, emit site file:line). The HTML doc (`docs/go-authoritative-clock/index.html`) is branch-local scratch — drifts, stripped at merge, not hand-maintained.

### Carry-forward facts

- **Paced `TryRecv` BLOCKS** (not a poll) despite the `if v, ok := …TryRecv(); ok` idiom — `ok=false` only on ctx-cancel. Judge recv semantics from `paced_wire.go` impl, not call-site idiom.
- **Per-goroutine bridge:** each goroutine sends to TS / picks up TS input; avoid central emitters/handlers.
- **Two-process editor:** reopen-file reloads only the webview; **Developer: Reload Window** reloads the extension host (Go spawn). gopls goes stale — `go test`/`go run` authoritative.
- **Node contract:** nodes do local work + drive outputs; no TCP-handshake/ack-nack/send-gating.
- MODEL.md + CLAUDE.md are the authoritative model. `docs/go-authoritative-clock/index.html` is branch-local scratch — stripped at merge.

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
