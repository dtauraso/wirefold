# Node Kind SPEC Format

Each `nodes/<Kind>/SPEC.md` is the canonical description of one Go node kind. It is the source of truth that drives the Go runtime (firing rule + struct), the TSX render (port positions + label), and the AI-assisted parity check.

This document defines what goes in a SPEC and what each section means.

## File layout

```markdown
# <Kind>

## Loader-managed channels

(Optional. Channels the loader creates internally — not wirable.)

| Name | Element type | Source |
|------|--------------|--------|
| Input | int | `data.init: []int` (pre-fill) |

## Non-channel fields

(Optional. Struct fields populated from `topology.json` `data.*`.)

| Field | Type | Source | Notes |
|-------|------|--------|-------|
| HeldValue | int | `data.initialSlots.held` | initial excitatory value |

## Firing rule

Pseudocode — English + math, no Go syntax. Reference port names from
the Ports table. State what triggers a fire, what is emitted, what
state is updated.

## View

| Field | Value |
|-------|-------|
| kind | <rfType> |
| bg | #rrggbb |
| border | #rrggbb |
| text | #rrggbb |
| minWidth | 90 |
| role | <kind-lowercased> |
| shape | rect |
| fill | #rrggbb |
| stroke | #rrggbb |
| width | 110 |
| height | 60 |

## Runtime status

- Loader-registered: yes | no
- TSX render: present | missing

## Open questions

(Optional. Anything underspecified or contradictory.)
```

## Column semantics

### Loader-managed channels

For channels the loader allocates but does not wire to any edge — e.g., Input nodes' `Input` channel is created and pre-filled from `data.init`. Listing here makes "this isn't wirable" explicit, so the SPEC doesn't suggest you can connect to it.

### Non-channel fields

Struct fields populated from `topology.json` at load time (not wires). Examples: `HeldValue` on `HoldNewSendOld`, `Name` / `Id` on most kinds. Trivial fields like `Id`/`Name` are implicit and need not be listed; only list fields with substantive load-time semantics.

### Firing rule

Pseudocode. The goal is "what this kind does," expressible in English+math without committing to Go syntax. If a behavior is genuinely undecided (e.g., what to do on simultaneous arrival), say so in the rule or in Open Questions — don't silently pick a tiebreaker.

### Runtime status

Two flags. If `Loader-registered: no` but `TSX render: present`, the kind is stranded (can be dropped into the editor but won't run). If both `no`, the kind is currently dormant.

## Authoring flow

1. User describes the desired node in their own words.
2. AI writes the pseudocode + port manifest in this format. User confirms.
3. AI generates the Go struct skeleton, TSX render, and registry entries from the manifest. Mechanical.
4. AI transcribes the pseudocode into Go as the firing rule body. Not interpretation — direct.
5. Parity check: mechanical (port↔field↔handle names align) + behavioral (Go rule still matches pseudocode).

## View section

The `## View` section is required for any kind that has a TSX render. It drives the generated `node-defs.ts` manifest.

```markdown
## View

| Field | Value |
|-------|-------|
| kind | input |
| bg | #1a1f2e |
| border | #3fb950 |
| text | #c9d1d9 |
| minWidth | 90 |
| role | input |
| shape | rect |
| fill | #1a1f2e |
| stroke | #3fb950 |
| width | 110 |
| height | 60 |
```

- `kind` — required (non-empty), but **write-only/vestigial today**: `tools/gen-node-defs/main.go`
  parses it and fails loudly if it's empty, but it is never used as the `NODE_DEFS` key. The
  actual `node-defs.ts` key is the **PascalCase Go kind name** from `Wiring.Register(...)`
  (`goKind`), matching `CLAUDE.md`. Keep `kind` populated (any non-empty string; convention is
  the spec kind with first char lowercased) so a half-migrated SPEC.md still fails the required
  check, but do not rely on its value meaning anything downstream.
- `bg`, `border`, `text` — required hex colors.
- `minWidth` — optional integer pixel width.
- `role`, `shape`, `fill`, `stroke`, `width`, `height` — optional `NodeTypeDef`-compatible
  fields consumed by schema/adapter code; `width`/`height` also drive the generated Go
  `nodes/Wiring/node_dims_gen.go` (used for port-to-port arc length), falling back to
  110×60 if omitted.

A missing `## View` section (or one missing the `Field`/`Value` table columns) is a **hard
error**: the generator (`tools/gen-node-defs/main.go`) fails the whole build (`fatalf`), it is
not skipped or treated as not-yet-migrated. Every `nodes/<Kind>/` directory with a
`Wiring.Register(...)` call MUST have a valid `## View` section.

## Banned content

Do not include in a SPEC:

- Go code or Go syntax. The pseudocode is the contract; Go is downstream.
- Implementation notes about goroutine scheduling, select ordering, channel buffer sizes. Those are Go runtime concerns, not per-kind.
- TSX styling, CSS classes, port positions in pixel coordinates. Side is the only render-relevant column.
