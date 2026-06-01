---
branch: task/timing-window
---

# Timing-Window (Coincidence-Detection) Rule — Spec

> **Prerequisite:** the receive-robustness / deleted-wire fix lands first on branch `task/wire-delete-pulse` (see [wire-delete-pulse.md](wire-delete-pulse.md)). This branch builds on it; the non-blocking poll receive here is needed only for the window timer.

## Motivation
Neuron-like coincidence detection: a multi-input node should fire only when its required inputs arrive close together in time. Inputs that don't complete a valid combination within a window are flushed (cleared) — preventing stray/partial signals from accumulating and from blocking upstream sources. This handles the **permanent-delete** case: an input that genuinely never arrives (an edge removed for good) so the gate flushes a partial combination after `W` instead of piling up beads. (The delete+re-add freeze is a *separate* receive bug fixed on the prerequisite branch `task/wire-delete-pulse`, not here.)

## Model
- **Per-node rule, different per node**, stored in `node.data` (same ownership pattern as the send rule). A node may have a timing window `W`. **Absent = no window = wait indefinitely** (today's behavior).
- The window **opens when the node's first required input arrives**.
- If **all** required inputs arrive within `W` of the opening → the node **fires** (consumes/Done all inputs), as today.
- If `W` elapses with any required input still missing → the node **clears**: `Done` (drain) every *held* input **without firing**, reset its has-input flags and the window, and wait for the next first-arrival.
- Consequence: a windowed node **always releases its inputs** (fire or timeout), so upstream sources never block indefinitely and beads never pile up at a dead/slow gate.

## Clock — KEY DECISION (confirm before implementing)
Proposed: **real elapsed wall-clock time measured at the node**, since that is what the node directly observes (the gap between `NotifyDelivered`-driven arrivals). `W` is chosen per node relative to current pulse pacing.
Alternatives: **sim-time ms** (consistent with `simLatencyMs`, but requires plumbing the playback scale into the node); **discrete rounds/ticks** (but the substrate has no global tick — only local clocks). Recommendation: real-time.

## Substrate mechanics
- **Receive becomes non-blocking poll.** A windowed node must check each input without parking, so it can run its window timer. Add a poll-receive primitive to PacedWire/Out, e.g. `PollRecv() (value, ok)` returning `ok=false` immediately when no value is present (`hasSend` false), without blocking.
  - This removes the long-parked blocking `Recv` for windowed nodes. Note: the delete+re-add freeze (orphaned parked `Recv` after a `slotReadyCh` swap) is **NOT** fixed here — that is a standalone receive bug fixed on the prerequisite branch `task/wire-delete-pulse`. The non-blocking poll in this spec is needed only to drive the window timer; the window/clear logic here addresses only the *permanent-delete* case (an input that genuinely never arrives, so the gate flushes a partial combination after `W`).
- **Windowed node loop:** poll each input each iteration; on first input present record `t0`; if all present → fire (`Done` all); else if `now - t0 > W` → clear (`Done` all held, reset flags + window). Use a short sleep / timer between polls (or a `select` with a timeout) to avoid busy-spin.
- **"Clear" =** call `Done` on each held input's `In` port (drains the upstream wire so a `consumeGated` source's `WaitConsumed` returns), and reset the node's `HasX` flags. No `Fire()`.
- **Holding semantics:** a polled-but-not-yet-fired input is *held* (received; slot stays full until `Done`). On clear, `Done` drains it.

## Scope / first step
- Implement on **`inhibitRight0`** first (the AND-gate that motivated this). Window value in `inhibitRight0`'s `node.data`.
- `readGate` is also an AND-gate but likely wants **no window** (ring lockstep) — leave it windowless initially.
- Non-windowed nodes: unchanged (blocking `Recv`).

## Open questions
- Clock units (see above).
- Default `W` and a sensible value for `inhibitRight0` given current `simLatencyMs` (~2500–3300 ms per wire) — `W` likely a small multiple of that.
- Should clearing emit a trace breadcrumb (`window-clear`) for observability? Recommended: yes.
- Interaction with `fireAndForget`: once gates self-clear on timeout, the `fireAndForget` side-gate sends (i0.ToNext1, i1.ToNext0) may be revertible to `consumeGated` (the timeout `Done` releases the source). Revisit after the window lands.

## Verification
- Unit: a windowed node receives one input, no second within `W` → clears (`Done` emitted, no fire). Two within `W` → fires.
- Live: permanently delete an `inhibitRight0` input edge → gate times out after `W`, clears its held input, ring keeps running (no pile-up). (Delete+re-add freeze-resume is verified on `task/wire-delete-pulse`, not here.)
