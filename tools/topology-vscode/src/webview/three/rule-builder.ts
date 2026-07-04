// rule-builder.ts — a row-keyed READ resource over the buffer's RuleBuilder column.
//
// The in-progress polar rule-builder session (gesture.go trySelectSphereRule) is
// Go-owned: a handhold click latches a pending half-term, a node click completes it, and
// every state change streams into the buffer's singleton RuleBuilder row
// (Buffer/snapshot.go). This module REFLECTS that row for the equation panel — it
// authors nothing; it only decodes the latest snapshot's RuleBuilder row + resolves node
// labels via the existing Node-block label section, and subscribes to snapshot arrivals
// so the panel updates live as the user clicks handholds/nodes.

import { useSyncExternalStore } from "react";
import { getLatestSnapshot, subscribeSnapshot } from "../snapshot-buffer";
import { decodeSnapshot, nodeLabel, portName } from "./buffer-decode";
import { readPortNodeRow } from "../../schema/buffer-layout";
import { readNodeSelected } from "../../schema/buffer-layout";
import {
  readRuleBuilderCenterRow,
  readRuleBuilderPendingCode,
  readRuleBuilderTermCount,
  readRuleBuilderT0Row,
  readRuleBuilderT0Code,
  readRuleBuilderT1Row,
  readRuleBuilderT1Code,
  readRuleBuilderPendingPortRow,
  readRuleBuilderPendingPortIsInput,
  readRuleBuilderPendingTorusRow,
  readPolarLockCenterRow,
  readPolarLockARow,
  readPolarLockACode,
  readPolarLockBRow,
  readPolarLockBCode,
  readPolarLockActive,
  readPolarLockKind,
  readPolarLockPortRow,
  readPolarLockPortIsInput,
  readPolarLockTorusRow,
  readPolarLockSelected,
} from "../../schema/buffer-layout";

/** POLAR_LOCK_KIND_NODE_NODE / POLAR_LOCK_KIND_PORT_TORUS mirror the Go eqKind ordering
 *  (locks.go): 0 = node/node equation (Center/A/B), 1 = `port ∈ torus` membership lock
 *  (portNode/portName/portIsInput/torusRow). */
export const POLAR_LOCK_KIND_NODE_NODE = 0;
export const POLAR_LOCK_KIND_PORT_TORUS = 1;

/** RULE_CODE_NONE mirrors the Go PendingCode/T{0,1}Code "absent" sentinel (255). */
const RULE_CODE_NONE = 255;
/** Row sentinel: no node resolved for this slot (mirrors every other buffer row-index column). */
const ROW_NONE = -1;

export interface RuleBuilderTerm {
  row: number;
  label: string;
  code: number;
}

export interface RuleBuilderState {
  centerRow: number;
  centerLabel: string;
  pending: { code: number } | null;
  terms: RuleBuilderTerm[];
  // In-progress `port ∈ torus` authoring capture (gesture.go hasPendingPort/
  // hasPendingTorus), independent of pending/terms above — either or both may be set at
  // once. null = that side not yet picked.
  pendingPort: { row: number; label: string; nodeRow: number; nodeLabel: string; isInput: boolean } | null;
  pendingTorus: { row: number; label: string } | null;
}

/** One COMMITTED polar-equation lock (locks.go md.polarEqs, streamed via the PolarLock
 *  block): index IS the md.polarEqs index — the same value toggle/select/delete send back. */
export interface PolarLockEntry {
  index: number;
  kind: number; // POLAR_LOCK_KIND_NODE_NODE | POLAR_LOCK_KIND_PORT_TORUS
  centerRow: number;
  a: { row: number; label: string; code: number };
  b: { row: number; label: string; code: number };
  active: boolean;
  // selected mirrors md.selectedLocks membership for this equation index — MULTI-select:
  // any number of rows may be selected simultaneously (locks.go SelectLock toggles
  // membership in an ordered list). Authoritative per-row; the panel/overlays no longer
  // read a single scalar "selectedLockIndex".
  selected: boolean;
  // eqPortTorus fields (kind === POLAR_LOCK_KIND_PORT_TORUS). Unused for a node/node row.
  portRow: number;
  portLabel: string;
  portNodeLabel: string;
  portNodeRow: number;
  portIsInput: boolean;
  torusRow: number;
  torusLabel: string;
}

// Separate cache (identity-stable like cachedVal above) for the committed-equations list,
// decoded from the PolarLock block. Selection is MULTI (per-row PolarLockEntry.selected) —
// the RuleBuilder's scalar SelectedLockIndex column is no longer read here.
export interface PolarLocksState {
  equations: PolarLockEntry[];
}

let cachedLocksFingerprint: string | null = null;
let cachedLocksVal: PolarLocksState = { equations: [] };

/** Decode the latest snapshot's committed polar-equation locks (PolarLock block), including
 *  each row's Selected column (MULTI-select — any number of rows may be selected). Pure
 *  reflect — no TS state authority; Go owns md.polarEqs/selectedLocks (locks.go). Returns a
 *  STABLE object identity while unchanged (useSyncExternalStore requirement). */
export function readPolarLocks(): PolarLocksState {
  const snap = getLatestSnapshot();
  if (!snap) return cachedLocksVal;
  const decoded = decodeSnapshot(snap);
  if (!decoded) return cachedLocksVal;

  const v = decoded.polarLockView;
  const n = decoded.polarLockCount;

  let fp = `${n}`;
  for (let i = 0; i < n; i++) {
    fp += `|${readPolarLockKind(v, i)},${readPolarLockCenterRow(v, i)},${readPolarLockARow(v, i)},${readPolarLockACode(v, i)},${readPolarLockBRow(v, i)},${readPolarLockBCode(v, i)},${readPolarLockActive(v, i)},${readPolarLockPortRow(v, i)},${readPolarLockPortIsInput(v, i)},${readPolarLockTorusRow(v, i)},${readPolarLockSelected(v, i)}`;
  }
  if (fp === cachedLocksFingerprint) {
    return cachedLocksVal;
  }
  cachedLocksFingerprint = fp;

  const equations: PolarLockEntry[] = [];
  for (let i = 0; i < n; i++) {
    const centerRow = readPolarLockCenterRow(v, i);
    const aRow = readPolarLockARow(v, i);
    const bRow = readPolarLockBRow(v, i);
    const portRow = readPolarLockPortRow(v, i);
    const torusRow = readPolarLockTorusRow(v, i);
    const portNodeRow = portRow === ROW_NONE ? ROW_NONE : readPortNodeRow(decoded.portView, portRow);
    equations.push({
      index: i,
      kind: readPolarLockKind(v, i),
      centerRow,
      a: { row: aRow, label: aRow === ROW_NONE ? "" : nodeLabel(decoded, aRow), code: readPolarLockACode(v, i) },
      b: { row: bRow, label: bRow === ROW_NONE ? "" : nodeLabel(decoded, bRow), code: readPolarLockBCode(v, i) },
      active: readPolarLockActive(v, i) === 1,
      selected: readPolarLockSelected(v, i) === 1,
      portRow,
      portLabel: portRow === ROW_NONE ? "" : portName(decoded, portRow),
      portNodeLabel: portRow === ROW_NONE ? "" : nodeLabel(decoded, portNodeRow),
      portNodeRow,
      portIsInput: readPolarLockPortIsInput(v, i) === 1,
      torusRow,
      torusLabel: torusRow === ROW_NONE ? "" : nodeLabel(decoded, torusRow),
    });
  }
  cachedLocksVal = { equations };
  return cachedLocksVal;
}

/** React hook: re-renders the caller when the committed polar-equation lock list or the
 *  focused row changes (Go-owned). */
export function usePolarLocks(): PolarLocksState {
  return useSyncExternalStore(subscribeSnapshot, readPolarLocks, readPolarLocks);
}

/** Buffer node-row index of the click-selected node (Node block's Selected column), or -1
 *  if nothing is selected / no snapshot yet. Not cached to a stable identity (a plain
 *  number is already stable-by-value for useSyncExternalStore's Object.is comparison). */
export function readSelectedNodeRow(): number {
  const snap = getLatestSnapshot();
  if (!snap) return ROW_NONE;
  const decoded = decodeSnapshot(snap);
  if (!decoded) return ROW_NONE;
  for (let i = 0; i < decoded.nodeCount; i++) {
    if (readNodeSelected(decoded.nodeView, i) !== 0) return i;
  }
  return ROW_NONE;
}

/** React hook: re-renders the caller when the click-selected node row changes. */
export function useSelectedNodeRow(): number {
  return useSyncExternalStore(subscribeSnapshot, readSelectedNodeRow, readSelectedNodeRow);
}

// Cache so getSnapshot returns a STABLE object identity while the session is unchanged
// (useSyncExternalStore compares by identity; a fresh object every snapshot would
// re-render every frame). Keyed by a cheap fingerprint of the row's raw columns.
let cachedFingerprint: string | null = null;
let cachedVal: RuleBuilderState | null = null;

/** Decode the latest snapshot's RuleBuilder row into a stable RuleBuilderState, or null
 *  if no snapshot / decode failure / nothing to show (no Center and no pending/terms). */
export function readRuleBuilder(): RuleBuilderState | null {
  const snap = getLatestSnapshot();
  if (!snap) return cachedVal;
  const decoded = decodeSnapshot(snap);
  if (!decoded) return cachedVal;
  const v = decoded.ruleBuilderView;

  const centerRow = readRuleBuilderCenterRow(v);
  const pendingCode = readRuleBuilderPendingCode(v);
  const termCount = readRuleBuilderTermCount(v);
  const t0Row = readRuleBuilderT0Row(v);
  const t0Code = readRuleBuilderT0Code(v);
  const t1Row = readRuleBuilderT1Row(v);
  const t1Code = readRuleBuilderT1Code(v);
  const pendingPortRow = readRuleBuilderPendingPortRow(v);
  const pendingPortIsInput = readRuleBuilderPendingPortIsInput(v);
  const pendingTorusRow = readRuleBuilderPendingTorusRow(v);

  const fingerprint = `${centerRow}|${pendingCode}|${termCount}|${t0Row}|${t0Code}|${t1Row}|${t1Code}|${pendingPortRow}|${pendingPortIsInput}|${pendingTorusRow}`;
  if (fingerprint === cachedFingerprint) return cachedVal;
  cachedFingerprint = fingerprint;

  const hasCenter = centerRow !== ROW_NONE;
  const hasPending = pendingCode !== RULE_CODE_NONE;
  const terms: RuleBuilderTerm[] = [];
  if (termCount >= 1 && t0Row !== ROW_NONE && t0Code !== RULE_CODE_NONE) {
    terms.push({ row: t0Row, label: nodeLabel(decoded, t0Row), code: t0Code });
  }
  if (termCount >= 2 && t1Row !== ROW_NONE && t1Code !== RULE_CODE_NONE) {
    terms.push({ row: t1Row, label: nodeLabel(decoded, t1Row), code: t1Code });
  }

  const hasPendingPort = pendingPortRow !== ROW_NONE;
  const hasPendingTorus = pendingTorusRow !== ROW_NONE;
  let pendingPort: RuleBuilderState["pendingPort"] = null;
  if (hasPendingPort) {
    const portNodeRow = readPortNodeRow(decoded.portView, pendingPortRow);
    pendingPort = {
      row: pendingPortRow,
      label: portName(decoded, pendingPortRow),
      nodeRow: portNodeRow,
      nodeLabel: nodeLabel(decoded, portNodeRow),
      isInput: pendingPortIsInput === 1,
    };
  }
  const pendingTorus: RuleBuilderState["pendingTorus"] = hasPendingTorus
    ? { row: pendingTorusRow, label: nodeLabel(decoded, pendingTorusRow) }
    : null;

  if (!hasCenter && !hasPending && terms.length === 0 && !hasPendingPort && !hasPendingTorus) {
    cachedVal = null;
    return null;
  }

  cachedVal = {
    centerRow,
    centerLabel: hasCenter ? nodeLabel(decoded, centerRow) : "",
    pending: hasPending ? { code: pendingCode } : null,
    terms,
    pendingPort,
    pendingTorus,
  };
  return cachedVal;
}

/** React hook: re-renders the caller when the rule-builder session changes (Go-owned).
 *  Returns null until a session exists (nothing latched/pending/accumulated). */
export function useRuleBuilder(): RuleBuilderState | null {
  return useSyncExternalStore(subscribeSnapshot, readRuleBuilder, readRuleBuilder);
}
