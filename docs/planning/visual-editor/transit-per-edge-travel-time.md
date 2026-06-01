---
branch: task/transit-per-edge
---

# Spec: per-edge travel-time (fan-in fix)

## Problem

A pulse's travel-time (`PacedWire.ArcLength` / `SimLatencyMs`) is stored on a `PacedWire`, and there is **one `PacedWire` per destination input port** (`loader.go`: `destWire` keyed by `target+"."+targetHandle`; `edgeWire[label]` points at the same pointer). When two edges fan into one input port, they **share one wire and one travel-time**. The last edge's length wins; the other edge's bead is drawn along its own (different-length) curve in TS but timed with the shared wire's latency, so it animates at the wrong speed.

Only one fan-in exists today: `readGate1.FromChainInhibitor` ← `i1.ToNext1` (193.85) **and** `bootstrap_rg.ToReadGate` (156.74, a one-shot startup seed). The seed bead crawls (apparentSpeed 0.0647 instead of 0.0800).

## Model

Travel-time belongs to the **edge** (it is the length of that specific drawn curve). The slot and backpressure belong to the **input port** (one parked value, one in-flight bead at a time). These coincided in every 1:1 graph, so they were bundled into one per-port object. Fan-in is the first case where a port has >1 edge, which separates them.

## What moves and what stays

- **Travel-time → per-edge.** `ArcLength` / `SimLatencyMs` for a given edge live with that edge's source `Out`, computed from that edge's port-to-port geometry.
- **Slot + backpressure → stay per-port.** `slot` / `hasSend` / `slotReadyCh`, the single in-flight bead, `Done`/`Recv`/`PollRecv`/`NotifyDelivered` are unchanged. Backpressure stays one-pulse-per-input. In-flight state stays shared (NOT split) — acceptable because the only fan-in feeder fires once at startup, so two fan-in beads are never in flight at once.
- **Window `W` → per-port aggregate.** `windowMs` needs `max(SimLatencyMs)` over the edges feeding each input. The per-port wire keeps a `MaxIncomingSimLatencyMs` aggregated over all edges bound to it (computed at load, recomputed on node-move). `In.SimLatencyMs()` returns that aggregate.

## Readers of travel-time, post-split

1. **Trace breadcrumb** (`Out.TrySend`/`TryEmit` → `trace.SendWire`): log the **Out's own** per-edge `ArcLength`/`SimLatencyMs`, not the shared wire's. This is what fixes the TS animation speed (one pulse per edge already; it just receives the correct duration).
2. **Coincidence window `W`** (`readgate`/`inhibitrightgate` `windowMs` via `In.SimLatencyMs()`): reads the per-port `MaxIncomingSimLatencyMs`. For `readGate1.FromChainInhibitor` this is `max(2423, 1959) = 2423` — unchanged from today, so `W` does not regress.
3. **Delivery timing**: unchanged — consumer/renderer-driven via `NotifyDelivered`; does not read `SimLatencyMs`.

## Changes (by file)

- `paced_wire.go`: remove per-bead `ArcLength`/`SimLatencyMs` as the authoritative travel-time source; add per-port `MaxIncomingSimLatencyMs` (set at load, updated on node-move). Slot/transit fields unchanged.
- `ports.go`: `Out` carries its own `ArcLength`/`SimLatencyMs`; `Out.TrySend`/`TryEmit` log those in `SendWire`. `In.SimLatencyMs()` returns the wire's `MaxIncomingSimLatencyMs`.
- `loader.go`: compute per-edge arc length for each edge's `Out` (already computed via `arcLengthBetweenPorts`); when binding edges to a shared dest wire, accumulate `MaxIncomingSimLatencyMs = max(...)` across them. Pass per-edge latency into `NewOutPaced`.
- `stdin_reader.go` node-move: update the affected **edge's** `Out` travel-time, and recompute the dest wire's `MaxIncomingSimLatencyMs` across all its edges.
- `builders.go`: thread per-edge latency into `NewOutPaced`.

## Degenerate case

For every 1:1 input (6 of 7 edges), `MaxIncomingSimLatencyMs == the single edge's SimLatencyMs == the Out's SimLatencyMs`, so behavior is identical to today. Only fan-in changes.

## Verification

- `pulse_speed_probe` (TS): every edge, including `bootstrapRgToReadGate1`, reads `apparentSpeed ≈ 0.0800` and `drawnLen == impliedBudgetMs`.
- Go tests: window `W` for `readGate1` unchanged (still derived from the longer i1 edge); ring runs; fan-in delete/re-add still works.
- `go build ./...`, `go test ./nodes/...`, `-race` on Wiring, `check-generated.sh` green.

## Out of scope / follow-up

In-flight state is NOT split per-edge (still one in-flight bead per input port). If a future graph needs two fan-in feeders with simultaneous in-flight beads, per-edge in-flight lanes are the follow-up; not needed while the only fan-in feeder is a one-shot seed.
