# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-23, main)

**Active branch:** none — working on `main`, clean tree. HEAD: `27f625b`.

Local + origin/main in sync. Working tree: clean.

### What landed this session

**Removed `specKindToRfType` casing layer** — RF node type now equals the spec
kind verbatim (PascalCase). Codegen emits PascalCase `NODE_DEFS` keys directly;
the first-char lowercase derivation in the registry was eliminated. CLAUDE.md
substrate-landing rule reconciled to reflect this (RF type = spec kind verbatim).

**BUG 1 fixed: `PseudoErrorOverlay` now covers all pseudo prefixes** — overlay
was previously scoped to a hardcoded prefix subset, silently dropping ReadGate
errors and save-results. Fixed to derive from `_pseudoPrefixes`, so all pseudo
kinds are covered.

**Phase 4 of pseudo-path compression (4a + 4b):**
- (4a) `handle-message.ts` dispatch is now table-driven off `PSEUDO_KIND_PREFIX`;
  `cmd/pseudo/main.go` is a registry map — no per-kind if-chains.
- (4b) `READGATE_OUT_HANDLE` derives from generated `NODE_DEFS.ReadGate`; the
  cmd-arg kind string is folded into `pseudoTable.cmdArg`.
- All hand-synced cross-file string duplication in the pseudo path eliminated.
  The pseudo path is at its irreducible floor: per-kind handler bodies + one-line
  registrations; no string drift hotspots remain.

**Navigation-tax re-audit:** built the audit, drove all findings to shipped,
removed the audit HTML. Retained in git history only.

### Open / next

No open items flagged. Pseudo path compression is complete. Next work should
be friction-driven from real editor use (log to session-log.md and open a
`task/<short-kebab>` branch).

Deferred from prior sessions (still valid if friction surfaces):
1. **InhibitRightGate pseudo projection** — same pattern as Input/ReadGate, has L/R params.
2. **ChainInhibitor pseudo projection** — blocked on unresolved "keep prev send current" spec.
3. **Live-verify ReadGate edit loop in VS Code** — the full edit-in-canvas UX was
   not verified live in the previous session; pick up if ReadGate friction surfaces.

### Key files

- `cmd/pseudo/main.go` — registry-map CLI entry point
- `tools/topology-vscode/src/extension/handle-message.ts` — table-driven dispatch
- `tools/topology-vscode/src/webview/rf/nodes/registry.ts` — NODE_DEFS (PascalCase keys, no casing layer)
- `tools/pseudo/readgate.go` — ReadGate pseudo package
- `tools/pseudo/input.go` — Input pseudo package
- `nodes/readgate/node.go` — ReadGate Go source (written by readgate-save)
- `nodes/Input/node.go` — Input Go source (written by input-save)

### Substrate model contract (stable)

See [MODEL.md](../../MODEL.md#slot-phase-lifecycle).

## Dev-loop

After TS edit: `npm run build` from `tools/topology-vscode/`.
After Go change: `go build ./...` from repo root, `go test ./nodes/Wiring/...`.
After pseudo change: `go test ./tools/pseudo/...`.
To repro / inspect: clear `.probe/*.jsonl`, reload window in VS Code, Run once, inspect logs.

Check: `go test ./...`, `bash tools/check-substrate-vocabulary.sh`.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored
to the state you're leaving the branch in, and commit on the active branch (main
if no task is in flight). Do not rely on chat history; the next AI may be a fresh
model with no transcript. The rendered handoff must itself contain this same ALWAYS
clause so the loop is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the
structural source of truth; update the template when an invariant changes.
