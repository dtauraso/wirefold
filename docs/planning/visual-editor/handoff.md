# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-01 — timing window implemented on both multi-input gates)

- **Active branch:** `task/timing-window` (built on `main` HEAD `c09fcd6a`, which already includes the merged `task/wire-delete-pulse` deleted-wire pulse removal).
- Build/test gate currently GREEN: `go build ./...`, `go test -count=1 ./...`, `-race` on touched packages all pass. `npx tsc --noEmit` not affected (no TS changed).

### What landed on this branch

- **Spec + HTML page** for the timing window: `docs/planning/visual-editor/timing-window.md` and a tabbed, diagram-led `docs/planning/visual-editor/timing-window.html` (Overview / Diagram / Rules / Windows / Tests). Styled to match the feature-audit palette.
- **The rule (final):** a node with **≥2 input edges** runs a timing window. `t0` = first input received. If ALL inputs arrive within `W` → all kept, node fires. If `W` elapses with any missing → all inputs removed (`Done` each held input, drains upstream so `consumeGated` sources release), no fire, reset, wait for next first-arrival. Single-input nodes have NO window (plain blocking `Recv`). `W` is **derived, not stored**: `W = 1.5 × max(simLatencyMs over the node's current input wires)`, `simLatencyMs = bezier arcLength / 0.08 WU-per-ms`, recomputed per check so geometry changes are reflected.
- **Code:**
  - `nodes/Wiring/paced_wire.go` — `PacedWire.PollRecv() (value, ok)`: non-blocking, returns `ok=false` immediately when nothing pending; on a hit returns the value WITHOUT clearing the slot (consumer must `Done`), identical consume/`WaitConsumed` contract to `Recv`.
  - `nodes/Wiring/ports.go` — `In.PollRecv()`, `In.SimLatencyMs()` (surfaces `pw.SimLatencyMs`), `In.Breadcrumb()`.
  - `nodes/inhibitrightgate/node.go` — window loop on the 2-input gate (`FromLeft`/`FromRight`). Reference implementation. `windowMs()`, `clear()`, `window_clear` breadcrumb, `select`+`time.After` park.
  - `nodes/readgate/node.go` — same window loop on ReadGate's 2 inputs (`FromInput`←in08, `FromChainInhibitor`←i1; both required). `windowMs()` mirrored, not shared (distinct named ports).
  - Tests: `paced_wire_test.go` (PollRecv empty + consume contract), `inhibitrightgate/firing_rule_test.go` and `readgate/firing_rule_test.go` (window FIRE + CLEAR).

### Windowed nodes + their derived W (current geometry snapshot)

| Node | Kind | Inputs | W |
|---|---|---|---|
| `readGate1` | ReadGate | 2 (in08, i1) | 2950 ms |
| `inhibitRight0` | InhibitRightGate | 2 (i1, i0) | 2650 ms |

### OPEN ITEMS / NEXT

1. **LIVE verification still pending (primary next step).** Unit tests run in chan-mode where `SimLatencyMs()` returns 0 (→ W=0), so they prove the FIRE/CLEAR logic but not a real timed window. Drive the editor: clear `.probe/*.jsonl`, reload, run, then **permanently delete an `inhibitRight0` (or `readGate1`) input edge** and confirm from `.probe/go.jsonl`: a `window_clear` breadcrumb appears for that node/port and the ring keeps running (no stall, no bead pile-up). That is the real payoff of this branch.
2. **Doc/topology discrepancy to reconcile.** `timing-window.md` / the HTML table earlier said `readGate1` has 3 inputs (in08, bootstrap_rg, i1); the ReadGate code has only 2 input ports (in08, i1) — `bootstrap_rg` is not a wired ReadGate input. The W value (2950, longest input = i1) is unaffected, but the input-count wording should be corrected to 2.
3. **The delete+re-add freeze for SINGLE-input / blocking receivers is still latent.** Windowed gates now use non-blocking `PollRecv` so they no longer park on `slotReadyCh` and cannot be orphaned. But single-input nodes still use blocking `Recv`; if a delete+re-add freeze ever resurfaces there, the durable-cond `Recv` rewrite (deferred on `task/wire-delete-pulse`) is the fix.

### Substrate model contract (stable)

See [MODEL.md](../../../MODEL.md#slot-phase-lifecycle). Send rules are node-owned (`node.data.sendRules`: `consumeGated` / `fireAndForget`); `PacedWire` is pure transport (`PollRecv` / `Recv` / `WaitConsumed` / `Reset` / `Delete` / `Restore`). `pump.ts` stays render-only.

## Dev-loop

After TS edit: `npm run build` from `tools/topology-vscode/`.
After Go change: `go build ./...` from repo root, `go test ./nodes/...`.
To repro / inspect: clear `.probe/*.jsonl`, reload window in VS Code, Run once, inspect `go.jsonl` breadcrumbs (`window_clear`, `wire_delete_drop_pulse`, `wire_recv`).

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
