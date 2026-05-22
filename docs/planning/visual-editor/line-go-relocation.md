# Line.go relocation plan

## Motivation

`nodes/Line/Line.go` hand-allocates channels and struct-literal-constructs
nodes in parallel to `Wiring.LoadTopology`. Two construction paths means
drift surface: any `populateInput`-style field added to a kind's populate
hook (or any new wire shape) must be mirrored in `Line.go` manually, and
the mismatch won't surface until a behavioural test catches it.

Relocating to a JSON topology eliminates that surface and makes the wiring
inspectable in the visual editor.

## Current shape

`Line.Setup()` builds five nodes and five channels by hand:

- `InputNode` (id 0) — emits `[]int{0, 1, 0}` via `inputNodeToReadGate`.
- `ReadGateNode` (id 0) — waits for both `inputNodeToReadGate` (value) and
  `i1ToReadGate` (ack); forwards to `readGateToI0`. Primed with one ack so
  the first input flows through immediately.
- `ChainInhibitorNode i0` (id 0) — receives `readGateToI0`, forwards to
  `i0ToI1`; also fans out to `i0ToInhibitRight0` (edge wire).
- `ChainInhibitorNode i1` (id 1) — receives `i0ToI1`, sends ack back to
  `i1ToReadGate` (backpressure); also fans out to `i1ToInhibitRight0`.
- `InhibitRightGateNode inhibitRight0` (id 0) — receives left (`i0`) and
  right (`i1`) edge signals; output wire goes nowhere (buf 1, dropped).

`main.go` constructs `Line{}`, calls `Setup()`, then starts each node
goroutine from `l.Line`. The ack prime (`i1ToReadGate <- 1`) is inlined
in `Setup()`.

## Target shape

- `topologies/line.json` — declarative wiring matching the topology above.
- `main.go` calls `Wiring.LoadTopology("topologies/line.json")` in place
  of `Line{}` / `Setup()`.
- `nodes/Line/Line.go` deleted (or moved to `testdata/` as a fixture if
  hand-wiring is ever useful for benchmarks).

## Steps

1. Read `topology.json` (canonical) for the JSON shape `LoadTopology`
   expects — wire buffer sizes, `data.init`, `data.seed` conventions.
2. Write `topologies/line.json`:
   - Five nodes: `Input`, `ReadGate`, `ChainInhibitor` ×2,
     `InhibitRightGate`.
   - Wire `inputNodeToReadGate` as the value port on ReadGate.
   - Wire `i1ToReadGate` as the ack port; set `data.seed: 1` to replicate
     the `i1ToReadGate <- 1` prime.
   - Wire `readGateToI0`, `i0ToI1`, `i0ToInhibitRight0`, `i1ToInhibitRight0`.
   - `Input` node gets `data.init: [0, 1, 0]`.
3. Update `main.go`: replace the `Line` import and `Line{}.Setup()` call
   with `Wiring.LoadTopology("topologies/line.json")`.
4. `go build ./...` — fix any parse errors.
5. `go run .` — verify the same value sequence (`0 1 0`) appears in trace
   output.
6. Delete `nodes/Line/Line.go` (and the package if empty).
7. Check `CLAUDE.md`, `MODEL.md`, `handoff.md`, and any grep hits for
   `Line.go` / `nodes/Line`; update references.

## Open questions

- **Ack-prime as seed:** `LoadTopology` supports `data.seed` on feedback
  wires to break startup deadlocks (see `feedback_edge_seed_required_for_rings.md`).
  Confirm `ReadGate`'s ack port is wired as a feedback edge and that seed=1
  is read on mount.
- **`data.init` serialisation:** `InputNode.Init []int` is populated via
  `populateInput`; verify that `data.init` in JSON deserialises to the right
  field name (should already work, but confirm before deleting Line.go).
- **`ToEdge` slice on ChainInhibitor:** `i0.ToEdge` and `i1.ToEdge` are
  set as single-element slices pointing at the InhibitRightGate inputs.
  Confirm `LoadTopology` / `populate` wires `ToEdge[]` correctly from JSON
  (or whether this needs a new populate step).
- **Test scaffold policy:** decide whether any hand-wired topology should
  live as a `testdata/` fixture for unit tests, or whether JSON covers all
  cases going forward.

## Out of scope

- Any redesign of the nodes involved.
- Multi-topology selection in `main.go`.
- Decisions on `ChainInhibitor` or `InhibitRightGate`.
