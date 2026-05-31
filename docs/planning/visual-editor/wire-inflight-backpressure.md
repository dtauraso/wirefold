---
branch: task/delete-edge
---

# Spec: wire-in-flight backpressure (source polls wire, not destination slot)

## Decision (confirmed by David 2026-05-31)

Source send-readiness is gated on the WIRE's in-flight occupancy, observed locally on the wire the source holds — not on a delivery acknowledgement that round-trips through the destination slot. The wire owns a single in-flight bit (one bead at a time). "Wire is clear" is the precondition for the source to put the next bead on it.

## Why

The delivery ack keyed to slot/handle/edge identity was fragile across this branch: edge `id != label`, `targetHandle` lost on save/reload, live re-add producing new identities, deleting an edge mid-pulse stranding state. Each fix (slot-keyed delivery `a6f5d04e`, Go-authoritative slot stamping `f3e179f6`, targetHandle guard `17aa67a3`) removed a layer but an intermittent stall remained. Gating the source on the wire's own in-flight bit removes the identity-lookup ambiguity entirely: the source holds the wire reference directly; bead-arrival flips the wire's bit; no lookup to get wrong.

## MODEL.md amendment (required, in the same change)

Reverses two current statements — apply deliberately and update MODEL.md:
- "Source nodes observe destination slot phase directly, not through wire phase" -> **Source observes the wire's in-flight phase.**
- "The wire owns no parked state, no ack, no take" -> **The wire owns a single in-flight occupancy bit** (not a parked value / ack / take — just one-bead occupancy).
- Driver / firing-rule sections: the source's send precondition becomes "wire not in-flight," polled each frame (consistent with the self-scheduling poll driver). `in-flight` is already allowed vocabulary.

## Concrete changes

1. `nodes/Wiring/paced_wire.go`: add `inFlight bool` to PacedWire with `InFlight()` accessor. Set true when the source puts a bead on the wire; clear to false on bead delivery. Replace the Send-blocks-until-slot-empty + blocks-until-Done handshake with: source checks `wire.InFlight()`; if false, put the bead on (set inFlight=true, emit the send trace), else do nothing this poll.
2. Source send sites (`nodes/Wiring/ports.go` and node run loops): change the precondition from a blocking Send to a polled `!wire.InFlight()`. `run()` stays idempotent — only sends when the wire is clear.
3. Bead delivery clears the wire: the TS animation-complete -> `delivered` ack (already slot-keyed + Go-authoritative, `f3e179f6`) maps to the wire via the slot registry and clears `inFlight`.
4. OPEN DESIGN POINT to resolve during implementation: when exactly does `inFlight` clear — on visual arrival, or when the bead enters the destination slot (slot empty)? Recommended: clear when the bead is delivered INTO the slot, so destination slot backpressure is preserved end-to-end while the source still sees a single wire-local signal. (`inFlight` then covers "bead traversing OR waiting for a full slot to empty.") Confirm against desired pacing: the source must not overrun a destination whose slot is still filled.
5. `nodes/Wiring/stdin_reader.go`: the `delivered` handler (currently `slotReg[target+"."+targetHandle].NotifyDelivered()`) clears the wire's `inFlight`. Keep the slot-keyed lookup.
6. `nodes/Wiring/loader.go`: wire setup unchanged (already sets Target/TargetHandle on the PacedWire).
7. MODEL.md: apply the amendment in section above.
8. TS cleanup: remove the `pulse-deliver` debug breadcrumb (commit `89334b4d`) and any remaining `wire-*` debug postLogs before merge.

## Verification

- `bash tools/check-substrate-vocabulary.sh`
- `bash tools/check-trace-kind-parity.sh`
- `bash tools/check-message-kind-parity.sh`
- `go build ./... && go test ./...`
- from tools/topology-vscode/: `npx tsc --noEmit` and `npm run build`
- Live repro: run the animation; delete an edge while a pulse is on it, then re-add it; confirm the animation keeps cycling continuously (the bug that motivated this).

## Branch state at spec time (task/delete-edge)

In order:
- delete-edge feature: select edge + Delete/Backspace removes it (`dfa37bf5`) — works.
- slot-keyed delivery ack (`a6f5d04e`).
- Go-authoritative slot stamped into the send trace (`f3e179f6`).
- targetHandle round-trip guard (`17aa67a3`).
- pulse-deliver debug breadcrumb (`89334b4d`) — REMOVE before merge.
The intermittent stall is superseded by this model change; the breadcrumb need not be analyzed.
