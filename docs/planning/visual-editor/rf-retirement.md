---
branch: editor-r3f
---

# Retiring rf/ for R3F

## Context

The `rf/` folder conflates three things under one name — genuinely-dead RF code, R3F-shared code that is merely misfiled, and the soon-removable `reactflow` npm dependency. R3F never renders via reactflow at runtime: the only couplings are one CSS import and `RFNode`/`RFEdge` type aliases. Pulse animation was wired in commit 3154816e (`handleTraceEvent` → `setPulse` → `PulseBead`). This plan separates the three concerns into reversible phases with sign-off gates.

---

## Phase 0 — Verification gate (no sign-off; do first)

Confirm pulses animate on Run; reproduce-or-clear the blank-after-drag-reload bug (orthogonal, still open) using `.probe` logs. No destructive step lands until editor confirmed working.

---

## Phase 1 — Delete pure-dead code (sign-off: destructive)

- `rf/rf-imperative.ts` — zero importers; delete.

---

## Phase 2 — Slim animation cluster to pulses-only (repurpose)

Strip `fire-flash`/`slot`/`held` branches out of `pump.ts` (keep only pulse-relevant trace-kind dispatch); then delete:

- `rf/fire-flash-state.ts`
- `rf/slots-state.ts`
- `rf/held-values.ts`

KEEP `rf/trace-kinds.ts` (pump dispatches on it) and `rf/pulse-state.ts` (ThreeView reads it).

---

## Phase 3 — Re-home RFNode/RFEdge type shapes (prep, non-destructive)

Define own `Node<T>`/`Edge<T>` (fields: `id`, `position{x,y}`, `data`, `type` / `source`, `target`, `handles`) in a neutral types home; repoint the 7 `import type ... from "reactflow"` sites. Pure type change — no behavior change.

---

## Phase 4 — Relocate misfiled live modules out of rf/ (refactor, no behavior change)

| File | New home |
|---|---|
| `rf/types.ts` (~37 importers) | `webview/types.ts` |
| `rf/viewer-state.ts` | `webview/state/` |
| `rf/history.ts`, `rf/dimmed.ts`, `rf/folds-state.ts`, `rf/run-status.ts` | `webview/state/` |
| `rf/pulse-state.ts`, `rf/pump.ts`, `rf/trace-kinds.ts` | `webview/three/` |
| `rf/adapter/*` (`spec-to-flow`, `flow-to-spec`, `-helpers`, `_bounds`) | `webview/state/adapter/` |
| `rf/panels/RunButton.tsx` | `webview/three/` or `webview/components/` |
| `rf/nodes/node-defs.ts`, `rf/nodes/registry.ts` | `webview/schema/` |

**Coupled doc edit:** CLAUDE.md's substrate-landing rule names `rf/nodes/registry.ts` as THE node-kind registry — move that rule text to the new path in the SAME commit or the audit guard drifts.

---

## Phase 5 — Drop reactflow dependency (sign-off: dependency removal)

- Delete dead `import "reactflow/dist/style.css"` in `main.tsx`.
- `npm uninstall reactflow`; remove from `package.json`.

---

## Phase 6 — Delete empty rf/ directory

---

## Sign-off gates

Phases 1, 5, and the deletions in 2 and 6 require user go-ahead (destructive/dep-removal rule). Phases 3 and 4 are non-destructive and land freely.

---

## Suggested batching

1. **Phase 0 alone** — gates everything downstream.
2. **Phases 1 + 2** — one cleanup sign-off.
3. **Phases 3 + 4** — refactor run, no sign-off needed.
4. **Phases 5 + 6** — dependency-gone finale, one sign-off.
