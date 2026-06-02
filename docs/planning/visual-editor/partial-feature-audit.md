---
branch: task/partial-feature-audit
---

# Partial-feature / half-removed-feature audit

Hunt signature (per the slot-badge instance we already removed): orphaned plumbing
left behind by a half-built or half-removed feature — dead emitters, no-op consumers,
orphaned fixtures, half-removed render/UI, codegen entries with no producer/consumer,
unused exported symbols, stubs, and one-sided IPC verbs.

Method: grep-first. Each finding shows the definition `file:line` and a grep proof of
zero live producers/consumers (excluding the definition itself and tests).

## Summary table

| # | Finding | Layer | Tell-type | Confidence | Est. removal scope |
|---|---------|-------|-----------|------------|--------------------|
| 1 | Canonical/edge-keyed trace resolver (`Resolve`, `EdgeMap`, `EdgeLite`, `BuildEdgeMap`, `WriteCanonicalJSONL`, `marshalCanonicalEvent`, `Event.Edge`) | Go (`Trace/`) | Dead emitter + dead serializer; no producer/consumer | dead | Delete `Trace/Resolve.go` + `WriteCanonicalJSONL`/`marshalCanonicalEvent` + `Edge` field + `edge?` in TS `TraceEvent`. ~120 LOC. |
| 2 | `Event.hasValue` field | Go (`Trace/`) | Set-but-never-read field; feature ("distinguish value 0 from no value") never implemented | dead | Remove field + 3 `hasValue: true` set sites + 2 test set sites. ~5 lines. |
| 3 | Wire props `arrowStyle` and `concurrent` | TS (`schema/` + `webview/`) | Round-tripped but never rendered; render-path consumer missing | likely-dead | Either wire into `SingleEdgeTube` (intended) or drop from `WIRE_PROPS`/`WireProps`/parser. Cross-layer (Go `wire:` tags). |
| 4 | NODE_DEFS fields never read by any consumer: `accent`, `defaultLabel`, `targets`, `sources`, `displays`/`DisplayKind`, `requiredInputs`, `defaultData`, top-level `isMulti` on ports | TS (`schema/`) | Generated codegen entries with no consumer | likely-dead (mixed; some in-progress) | Trim generator + `NodeDef` type fields. Confirm against `gen-node-defs` intent first. |
| 5 | pump.ts `case "recv"`/`case "fire"` → `return;` | TS (`webview/three/pump.ts`) | No-op consumers (exhaustiveness only) | in-progress / by-design | Keep — see analysis. Not a removal candidate. |

Findings ordered dead → likely-dead → in-progress.

---

## Finding 1 — Canonical/edge-keyed trace resolver is fully orphaned (DEAD)

**What it is.** A whole "raw → canonical" trace pathway: Go nodes emit `send` events
keyed by `(node, port)`; this code was meant to rewrite them to be keyed by spec edge
ID for parity against a TS-simulator trace and the "chunk-1 fixture".

**Definitions.**
- `Trace/Resolve.go:23` `type EdgeMap`, `:29` `type EdgeLite`, `:35` `func BuildEdgeMap`, `:51` `func Resolve`.
- `Trace/Trace.go:245` `func WriteCanonicalJSONL`, `:344` `func marshalCanonicalEvent`.
- `Trace/Trace.go:49` `Event.Edge` field (`json:"edge,omitempty"` — comment: "canonical send only; set by Resolve").
- TS mirror: `messages.ts:33` `TraceEvent` send variant carries optional `edge?: string`.

**Grep proof of zero live producer/consumer.**
- `Resolve(` / `WriteCanonicalJSONL(` / `BuildEdgeMap` / `EdgeMap` / `Canonical` across the
  entire repo (`grep -rn ... --include="*.go"`) match ONLY `Trace/Resolve.go`,
  `Trace/Trace.go`, and a coincidental word "Resolve" in a `tools/gen-node-defs/main.go`
  comment. Zero callers in `main.go`, `nodes/`, or any `_test.go`.
- `main.go:55` uses `tr.WriteJSONL(f)` (the RAW writer) — the canonical writer is never invoked.
- `Event.Edge` is set ONLY inside `Resolve` (`Resolve.go:61 ce.Edge = edge`). `grep -rn "\.Edge =\|Edge:"`
  shows no other assignment.
- TS side: `grep -rn "event.edge\|\.edge"` in `tools/topology-vscode/src` → zero reads of `TraceEvent.edge`.
  pump.ts matches edges by `source`+`sourceHandle`, never by `edge`.
- `Resolve.go:13` comment references "the TS simulator's historyToTrace" and `Trace.go:27`
  references `src/sim/trace.ts`; neither exists in `tools/topology-vscode/src` (`grep historyToTrace`,
  `ls src/sim` → no such dir). The simulator this pathway was meant to reconcile against is gone.

**Removing it — what to check.** Nothing live consumes it; Go compiles unused exported
funcs without complaint (verified `go build ./Trace/` is clean today). Safe to delete
`Trace/Resolve.go` wholesale, plus `WriteCanonicalJSONL` + `marshalCanonicalEvent` +
`Event.Edge` in `Trace.go`, plus `edge?` from the `TraceEvent` send variant in
`messages.ts`. Before removing: confirm no out-of-repo replay tooling reads canonical JSONL
(none found in repo). The raw-form `marshal_contract_test.go` and TS `trace-event-fields.test.ts`
do NOT touch the canonical path, so they are unaffected.

## Finding 2 — `Event.hasValue` is set but never read (DEAD field)

**What it is.** `Trace/Trace.go:52` declares unexported `hasValue bool` with the comment
"distinguishes 'value 0' from 'no value' for send/recv events." The feature it documents
(suppressing `value` when absent) was never implemented.

**Grep proof.** `grep -rn "hasValue"`:
- Set to `true` at `Trace.go:121` (Recv), `:139` (Send), `:150` (SendWire), and in tests
  `marshal_contract_test.go:44,46`.
- Read: NOWHERE. `marshalEvent` (`Trace.go:291`) emits `Value` unconditionally; there is no
  `if e.hasValue` anywhere. The field has no effect on output or behavior.

**Removing it — what to check.** Pure cleanup; remove the field and the `hasValue: true`
literals. The marshal contract test compares against the fixture and does not assert on the
field, so it stays green. (Note: if the value-0-vs-absent distinction is genuinely wanted,
this is the dangling stub of that intent — flag for the substrate owner rather than silently
deleting if that distinction is on the roadmap.)

## Finding 3 — Wire props `arrowStyle` and `concurrent` are persisted but never rendered (LIKELY-DEAD)

**What it is.** `schema/wire-defs.ts` `WIRE_PROPS` declares `arrowStyle` and `concurrent`
(generated from `wire:"..."` tags in `nodes/Wiring/loader.go`). They flow into `EdgeData`
(`webview/types.ts:87`) and round-trip through save/load via `pickWireProps`
(`spec-to-flow-helpers.ts`) and `flow-to-spec.ts`.

**Grep proof of no render consumer.** `grep -rn "arrowStyle\|concurrent"` under
`tools/topology-vscode/src/webview` (the render layer) returns ZERO matches outside an
unrelated log comment. They are parsed (`parse-nodes-edges.ts`), typed, and serialized, but
no `SingleEdgeTube`/`scene-content.tsx`/`geometry-helpers.ts` code reads them — they never
affect what is drawn. `label` (the third optional wire prop) IS read at
`ThreeView.tsx:251` and `edge-creation.ts:88`, so it is healthy; `arrowStyle`/`concurrent`
are the orphans.

**Removing it — what to check.** This is the classic half-built-render signature: the
schema/persistence plumbing exists, the render consumer was never built (or was removed).
Decision needed: (a) finish — thread both into `SingleEdgeTube` per the CLAUDE.md "Wire props"
landing rule; or (b) remove from `WIRE_PROPS`, `WireProps`, the parser branch in
`parse-nodes-edges.ts:127`, and the corresponding `wire:` tags in `loader.go` (cross-layer —
must regen `wire-defs.ts`). Check `test/fixtures/specs/*` for any spec pinning these props
before removal (`edge-data-roundtrip.json` round-trips wire props generically).

## Finding 4 — NODE_DEFS carries generated fields no consumer reads (LIKELY-DEAD, mixed)

**What it is.** `gen-node-defs` emits a rich `NodeDef` (`node-defs.ts:12`), but the only
structural consumer, `defToTypeDef` in `node-types.ts:21`, reads just
`role, inputs, outputs, shape, fill||bg, stroke||border, width||minWidth, height`.

**Grep proof of unused fields** (`grep -rEn` across `src`, excluding the `node-defs.ts`
definition itself):
- `.accent` → 0 reads; `defaultLabel` → 0; `.targets` → 0; `.sources` → 0;
  `displays`/`DisplayKind` → 0; `requiredInputs` → 0; `defaultData` → 0.
- Rendering is generic (`scene-content.tsx` reads `fill`/`stroke`), so the visual fields are
  covered; the listed ones are inert.

**Context / confidence split.**
- `requiredInputs` and the red-node diagnostic were explicitly removed 2026-06-01 (per
  `memory/feedback_enforce_required_inputs.md`); `requiredInputs` in NODE_DEFS is the
  leftover codegen output of that removed feature → **dead**.
- `displays`/`DisplayKind` ("queue"|"repeat"|"held") has no reader — same family as the
  removed slot-badge / held-value display work → **likely-dead**.
- `defaultLabel`, `defaultData`, `targets`, `sources`, `accent` may be intended for an
  editor "add node" palette / port-accent rendering that isn't wired yet → flag as
  **in-progress vs dead** ambiguous; decide per field.

**Removing it — what to check.** These are generator outputs, so removal means editing
`tools/gen-node-defs/main.go` (it builds `displays`, `defaultData`, `accent`, `defaultLabel`,
etc. — see `gen-node-defs/main.go:49-69`) AND the `NodeDef` type, then regenerating.
Confirm the "add node" / palette UI does not plan to read `defaultData`/`defaultLabel`
before trimming those two. `inputs`/`outputs` ARE consumed (keep).

## Finding 5 — pump.ts `recv`/`fire` no-op cases (IN-PROGRESS / BY-DESIGN, not a removal)

`pump.ts:39-42` has `case "recv": return;` and `case "fire": return;`. These look like the
slot-badge `case "slot": return;` signature, BUT they are by-design: Go genuinely emits
`recv` and `fire` events (verified live producers: `Trace.Recv`/`Trace.Fire` called from
all four node packages and `Wiring/builders.go:200`), and the render layer intentionally
visualizes only `send`→pulse and `done`→clear. The exhaustiveness `switch` over the
generated `TRACE_EVENT_KINDS` requires a branch per kind. Keep both — removing them would
break the `assertNever` exhaustiveness contract and drop real events on the floor. Listed
only so the next reader does not mistake them for the slot-badge pattern.

---

## Healthy / confirmed fully-wired (do not re-investigate)

- **Trace emitters Fire / Recv / Send / SendWire / Done / Breadcrumb** — all have live Go
  callers in `nodes/` (grep `\.Fire(`, `.Recv(`, `.Send(`, `SendWire`, `.Done(`, `Breadcrumb`
  all return production call sites, not just tests).
- **Trace kind vocabulary** `recv/fire/send/done` — Go `TraceEventKinds` (`Trace.go:42`) →
  generated `trace-kinds.ts` → pump.ts exhaustiveness → TS `TraceEvent` union → fixture
  `trace-events.jsonl` → `trace-event-fields.test.ts` are all in sync (4 kinds, no orphan kind).
- **NODE_DEFS ↔ Go node packages** — exactly 4 keys (ChainInhibitor, InhibitRightGate,
  Input, ReadGate) match the 4 `nodes/<Kind>/` dirs and `RUNTIME_IMPLEMENTED_KINDS`.
- **IPC verbs** — every webview→host verb in `messages.ts` `WEBVIEW_TO_HOST_TYPES` is handled
  in `handle-message.ts dispatch`. The stdin verbs forwarded to Go (`delivered`, `fade`,
  `deleteEdge`, `addEdge`, `node-move`) all have live handlers in `stdin_reader.go:209-261`
  doing real work. `pause`/`resume`/`stop`/`run-cancel` are runner-process controls (not
  stdin verbs) and are correctly handled by the runner, not Go — not one-sided.
- **`label` wire prop** — rendered (`ThreeView.tsx:251`, `edge-creation.ts:88`); healthy.
- **`marshalEvent` raw form + contract tests** — pinned and exercised; the RAW path
  (`WriteJSONL` from `main.go:55`) is the live one.
