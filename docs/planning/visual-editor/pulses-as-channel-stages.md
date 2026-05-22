---
branch: task/pulses-as-instances
---

# Pulses as Channel Stages

This file tracks the rebuild plan from `pulses-as-channel-plan.html`, breaking the migration from the old pulse-animation model (with `clearRunState` and `run-start` plumbing) into four concrete implementation stages. Each stage is a standalone, testable step with a clear entry and exit contract.

## Stage 1 — Wire type alongside existing wiring

Introduce visual-paced wire type with two-gate semantics; no node conversion.

**Status:** Done.

**Commit:** `2531bc9`.

## Stage 2 — Convert Input node

Switch one node (Input) to the new wire type and run end-to-end.

**Status:** Not started.

## Stage 3 — Convert remaining nodes + webview round-trip

Convert ReadGate, ChainInhibitor, InhibitRightGate. Add the `delivered` message from webview→host that opens the delivery gate.

**Status:** Not started.

## Stage 4 — Remove old animation state

Delete `clearRunState`, `run-start` plumbing, `pulseValueRef` in SubstrateEdge, `prev` in use-fire-flash.

**Status:** Not started.

---

*This file is branch-local and will be stripped on merge.*
