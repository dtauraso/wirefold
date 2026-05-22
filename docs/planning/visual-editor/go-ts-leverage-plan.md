---
branch: task/go-ts-leverage
---

# Go↔TS Leverage Plan

Audit scope: places in the Go↔TS contract surface where two sides must agree on a string,
schema, or shape, and could instead share one source of truth. Goal is to find structural
ties (generated code, shared fixtures, cross-language tests) that make drift detectable
or impossible at build/test time.

## Known leverage opportunities

### 1. Port names: Go struct fields → topology.json handles

Go struct field names (e.g. `ToNext`, `ToNext0`, `In0`) are used as `sourceHandle`/`targetHandle`
values in `topology.json`. The loader validates handle-exists-on-kind at load time, but only
against the running binary's reflection. A Go rename updates the binary and passes `go test`;
`topology.json` silently stops loading (or loads with a bad-handle error at runtime, not at
build time). Need either: a generated fixture that the loader test compares against, or a
cross-checked snapshot that CI enforces.

### 2. Trace event field names: kind / node / port

Go emits JSONL trace events with fields `kind`, `node`, `port` (and others). `pump.ts`
reads these by string key. A Go field rename (or json tag change) passes `go build` and
`go test` but silently breaks the pump — no animation, no error. No test currently covers
this boundary.

### 3. Topology JSON schema: two independent parsers

`tools/topology-vscode/src/webview/rf/schema.ts` parses topology JSON on the TS side.
`nodes/Wiring/loader.go` parses the same format on the Go side. They must stay in sync
but there is no shared fixture or contract test. Adding a new field requires touching both,
and a mismatch produces silent data loss rather than a visible error.

### 4. data.state and data.edgeSeeds keys

Go derives `data.state` keys via reflection from `wire:"data.state"` struct tags (e.g.
`ChainInhibitorNode.Pass`). The TS schema parser validates `data.state` keys per-kind
independently. A new `wire:` field added in Go must be manually mirrored in the TS
validator; no build step enforces this.

### 5. Trace event kinds (send, recv, fire)

The set of valid `kind` values in trace events (`send`, `recv`, `fire`, `slot`) is
enumerated implicitly in both Go (where events are emitted) and `pump.ts` (where they
are consumed in a switch/if chain). No shared enum or schema; adding a new event kind
requires two independent edits with no cross-check.

### 6. Node kind names: three-way coordination required

Node kind names (`Input`, `ReadGate`, `ChainInhibitor`, `InhibitRightGate`) appear in:
- TS `RF_NODE_TYPES` in `_constants.ts`
- TS schema parser kind allowlist in `schema.ts`
- Go package names under `nodes/<kind>/`
- `topology.json` `type` values

Per CLAUDE.md, adding a kind requires manual coordination across all three places. A code-
generated kind registry (even a simple JSON fixture committed to the repo) would make
omissions detectable at test time.

### 7. Port direction: Go type → TS render side

Go struct field type determines port direction: `*Wiring.In` → input port (left side),
`*Wiring.Out` / `Wiring.OutMulti` → output port (right side). The topology editor must
independently know which handles render on which side, either hardcoded per kind or derived
from topology JSON. A mismatch produces visually wrong edges (connected to wrong sides).
A generated port-map fixture would let the TS side derive direction from Go's AST rather
than re-specifying it.

## Suggested investigation order

1. Port names (#1) — highest impact, loader tests are already in place as a foundation.
2. Trace event field names (#2) — silent failure mode, easy to add a Go→JSONL fixture test.
3. Trace event kinds (#5) — small enumeration, easy shared fixture.
4. Node kind names (#6) — three-way, but a simple generated list would cover it.
5. Port direction (#7) — depends on having a generated port-map (related to #1).
6. Topology JSON schema (#3) — larger scope; may warrant a JSON Schema document shared by both sides.
7. data.state / data.edgeSeeds keys (#4) — depends on #3 approach.
