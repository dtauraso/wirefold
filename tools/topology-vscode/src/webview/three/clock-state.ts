// clock-state.ts — a row-keyed READ resource over the buffer's Clock column.
//
// Running-vs-paused is Go-owned: RealClock's Halt()/Resume() transition guards
// (nodes/Wiring/clock.go) are the ONLY emit point for KindHalted, streamed into the
// buffer's single-row Clock block (Buffer/snapshot.go). This module REFLECTS that
// Go-owned column for the RunButton — it is NOT a domain store; it authors nothing
// and predicts nothing. It replaces the old runCommand.ts play()/pause() PREDICTIONS
// (posting {state:"running"}/{state:"paused"} right after a fire-and-forget stdin
// write) with the one true bit Go actually reports.

import { useSyncExternalStore } from "react";
import { getLatestSnapshot, subscribeSnapshot } from "../snapshot-buffer";
import { decodeSnapshot } from "./buffer-decode";
import { readClockHalted } from "../../schema/buffer-layout";

/** Decode the latest snapshot's Clock row: true = halted (paused), false = running.
 *  Returns null when no snapshot has arrived yet (no process/no clock — see RunButton's
 *  reconciliation with process liveness). */
export function readClockHaltedFlag(): boolean | null {
  const snap = getLatestSnapshot();
  if (!snap) return null;
  const decoded = decodeSnapshot(snap);
  if (!decoded) return null;
  return readClockHalted(decoded.clockView, 0) !== 0;
}

/** React hook: re-renders the caller when the clock's halted state flips (Go-owned).
 *  Returns null until the first snapshot lands (no live buffer to reflect). */
export function useClockHalted(): boolean | null {
  return useSyncExternalStore(subscribeSnapshot, readClockHaltedFlag, readClockHaltedFlag);
}
