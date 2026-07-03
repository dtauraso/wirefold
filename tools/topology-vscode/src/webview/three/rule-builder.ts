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
import { decodeSnapshot, nodeLabel } from "./buffer-decode";
import {
  readRuleBuilderCenterRow,
  readRuleBuilderPendingCode,
  readRuleBuilderTermCount,
  readRuleBuilderT0Row,
  readRuleBuilderT0Code,
  readRuleBuilderT1Row,
  readRuleBuilderT1Code,
} from "../../schema/buffer-layout";

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

  const fingerprint = `${centerRow}|${pendingCode}|${termCount}|${t0Row}|${t0Code}|${t1Row}|${t1Code}`;
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

  if (!hasCenter && !hasPending && terms.length === 0) {
    cachedVal = null;
    return null;
  }

  cachedVal = {
    centerRow,
    centerLabel: hasCenter ? nodeLabel(decoded, centerRow) : "",
    pending: hasPending ? { code: pendingCode } : null,
    terms,
  };
  return cachedVal;
}

/** React hook: re-renders the caller when the rule-builder session changes (Go-owned).
 *  Returns null until a session exists (nothing latched/pending/accumulated). */
export function useRuleBuilder(): RuleBuilderState | null {
  return useSyncExternalStore(subscribeSnapshot, readRuleBuilder, readRuleBuilder);
}
