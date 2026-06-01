# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-01 — task/delete-edge merged to main)

- **Active branch:** `task/timing-window` (branched from `main` after the
  `task/delete-edge` merge).
- `task/delete-edge` is **merged to main** and deleted (local + remote).
- Build/test gate: green at merge (`go build ./... && go test ./...`, `npx tsc --noEmit` clean).

### What landed on task/delete-edge (merged)

- **Per-edge, node-owned send rules.** Send rules now live on the node
  (`node.data.sendRules`), not the wire. Two rules:
  - `consumeGated` — source blocks until the bead is consumed (default backpressure).
  - `fireAndForget` — non-blocking; drops the bead if the destination is busy.
  - `i0.ToNext1` uses `fireAndForget` so deleting that side-gate input keeps the ring alive.
- **PacedWire is pure transport.** Ops: `WaitConsumed` / `Reset` /
  `Delete` / `Restore`. `Delete` sets a deleted flag so the source stops
  placing beads (no pile-up at a dead gate); `Restore` re-enables it.
- **Substrate messages `deleteEdge` + `addEdge`** wired through
  `messages.ts` → `handle-message.ts` → `store.ts`; delete persistently
  silences the wire, add restores it.
- **Delete-path trace breadcrumbs** added.
- **MODEL.md amended** to node-owned send rules (rules are a property of
  the node, not the wire).

### KNOWN ISSUE (prominent — drives next branch)

**Delete + re-add freezes the destination AND-gate.** A receiver parked
in `PacedWire.Recv` is orphaned when `Reset` swaps its `slotReadyCh`:
the parked goroutine holds a reference to the old channel and never
wakes. This is a blocking-receive structural problem, not a knob to
tune. It is intended to be subsumed by the timing-window receive
rewrite below (non-blocking polling has no parked receiver to orphan).

### Next task — task/timing-window (SPEC FIRST)

Implement a per-node **timing-window (coincidence-detection) rule**:

- `node.data` carries a `window` (duration).
- The node **polls its inputs non-blockingly** instead of parking in a
  blocking `Recv`.
- On the **first** input arrival, the node **opens the window**.
- If **all** required inputs land within the window → **fire**.
- If the window expires with inputs missing → **clear** the held inputs
  (Done-without-fire) and reset; no fire.
- This rewrites the receive path from blocking `Recv` to non-blocking
  polling and **subsumes the orphaned-Recv freeze** (no parked receiver
  exists to be orphaned by a `slotReadyCh` swap).

**Write the SPEC FIRST** (amends MODEL.md) and get David's confirmation
before any implementation. Do not write code ahead of the spec.

### Key files

- `nodes/Wiring/paced_wire.go` — pure transport: `WaitConsumed` / `Reset` / `Delete` / `Restore`. The blocking `Recv` here is what the timing-window rewrite replaces.
- `nodes/Wiring/paced_wire_test.go` — transport tests; extend for window polling.
- `nodes/Wiring/stdin_reader.go` — node-move IPC; also touched by the i0 fireAndForget wiring.
- `nodes/Wiring/loader.go` — threads node positions + send rules into wire/node construction.
- `tools/topology-vscode/src/messages.ts` — `deleteEdge` / `addEdge` message types.
- `tools/topology-vscode/src/extension/handle-message.ts` — dispatch for the new messages.
- `tools/topology-vscode/src/webview/three/store.ts` — store wiring for delete/add.
- `tools/topology-vscode/src/webview/three/ThreeView.tsx` — orchestrator (render in `scene-content.tsx`, interaction in `interaction-controls.ts`).
- `docs/planning/visual-editor/feature-audit/index.html` — friction-driven feature board.

### Substrate model contract (stable)

See [MODEL.md](../../../MODEL.md#slot-phase-lifecycle). As of
task/delete-edge, **send rules are node-owned** (`node.data.sendRules`:
`consumeGated` / `fireAndForget`), and `PacedWire` is pure transport
(`WaitConsumed` / `Reset` / `Delete` / `Restore`). `pump.ts` stays
render-only. The timing-window rule (next task) amends MODEL.md again:
non-blocking input polling + coincidence-detection window.

## Dev-loop

After TS edit: `npm run build` from `tools/topology-vscode/`.
After Go change: `go build ./...` from repo root, `go test ./nodes/Wiring/...`.
Fade unit tests: `cd tools/topology-vscode && npx vitest run test/fade.test.ts`.
To repro / inspect: clear `.probe/*.jsonl`, reload window in VS Code, Run once, inspect logs.

Check: `go test ./...`. All guard scripts run via the Stop hook (`scripts/stop-checks.sh`). Bash approval guard runs via PreToolUse.

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
