# Fade — non-destructive mask over nodes and edges

Status: spec. Supersedes and removes the undo/redo stack recording system entirely (no command stack, no linear history).

## What fade is

A user-toggled **mask** on nodes and edges. Fade is **non-destructive**: the topology — the spec, the Go wiring, the slot graph — is unchanged. A faded element stays in the graph; it is only *ignored*. This is distinct from delete/make-new-node, which are real topology mutations and behave exactly as a delete/create does today.

Fade is **symmetric across the layer boundary**:
- **TS (animation)** ignores faded edges: they render muted and draw no pulse.
- **Go (firing/delivery)** ignores faded elements: a faded node does not fire, and no value is `Send`/`Recv` across a faded wire. This is "Go's version of what TS does."

## Ignore lists

Each node carries an **ignore list**: the set of incident edge ids that are faded. The ignore list is the shared currency consumed by both layers — TS reads it to suppress animation, Go reads it to suppress firing.

Propagation rules:
- **Fade a node** → every incident edge becomes faded → every node on the other end of those edges adds those edge ids to its ignore list. Unfade reverses this.
- **Fade an edge** directly → only that edge is faded; both endpoint nodes update their ignore lists. The endpoint nodes are NOT themselves faded — unless this removes their last non-faded edge, which auto-fades them per the rule below.
- **A node with no non-faded edges auto-fades.** When fading an edge (or a cascade) leaves a node with zero non-faded incident edges, that node itself becomes faded — which in turn fades any remaining edges and can cascade to neighbors.
- Fade therefore spreads to a **fixpoint**: apply (node-fade → fade all incident edges) and (all incident edges faded → fade the node) repeatedly until the faded set is stable. Unfade likewise contracts to a fixpoint.
- **Unfade triggers:** (1) the user unfades a node or edge directly; (2) adding a new edge to a faded node — new edges are always created non-faded — gives that node a non-faded incident edge, which unfades it. This is the escape hatch out of a fully-faded region.
- A node is faded iff it has zero non-faded incident edges OR was directly faded by the user; gaining any non-faded edge clears the auto-fade.

## TS side (animation)

- A `faded: boolean` field on node data (`NodeData`) and edge data (`EdgeData`) is the source of truth. Muted rendering derives from it — there is no separate "dimmed" pipeline; `faded` replaces the role the `dimmed` view-state played.
- Faded edges render muted and draw no pulse.
- A pulse already **in-flight** on a wire when it is faded is **left to finish normally** — it completes on the usual delivery path. Fade does not interrupt in-flight pulses; it only prevents new ones from starting.

## Go side (firing/delivery)

The fade gate lives entirely in the `Wiring` layer (`paced_wire.go` + `MoveDispatch`). Per-kind node packages (`input`, `readgate`, `inhibitrightgate`, …) are **not touched** — there are ZERO per-kind changes. All per-kind loops inherit the gate automatically.

Mechanism: each `PacedWire` carries a `faded` flag. The `"fade"` bridge handler mail-sorts each `(edgeId, faded)` entry via `MoveDispatch` to the owning `edgeMover` goroutine, which calls `SetFaded` on its own `PacedWire` — no central fan-out. `Send` checks the flag at the top of the function, under the mutex; if the wire is faded, the send is **skipped** — returns benignly, does not fill the slot, does not block. A faded node has all its incident edges faded, so all of its sends skip and it goes inert without any poll-loop changes. In-flight values already past the gate finish normally.


## Bridge

Fade is a **live control signal**, analogous to the existing global play/pause gate — not a spec/topology change. It crosses webview → host → Go stdin, following the play/pause precedent (`stdin_reader.go`).

- New webview→host message kind: `"fade"` carrying the **complete current faded edge set** — every time anything changes, the full set is sent. Go replaces its set wholesale (idempotent, self-correcting, no delta stream to keep in sync). Add to `WebviewToHostMsg` / `WEBVIEW_TO_HOST_TYPES` (`messages.ts:35,95`) and to `stdin_reader.go`'s accepted types; keep `tools/check-message-kind-parity.sh` green.
- **Node fade is NOT sent.** A faded node expands to its incident edges via the TS fixpoint, so Go only ever receives faded **edge** ids. `MoveDispatch` routes each `(edgeId, faded)` entry to the owning `edgeMover` goroutine, which calls `SetFaded` on its `PacedWire`. No in-flight `Send` is interrupted — faded nodes simply stop initiating new sends.

## Persistence

Faded state is **view-state**: it serializes with the view and survives reload. On Go (re)start, the editor replays the current faded set to Go after load (the spec itself stays clean — topology is never the carrier of fade).

## Removed

- The entire undo/redo **stack recording system**: command stack, linear history, undo coalescing. Gone, not replaced by an equivalent. The §3a "undo (half-wired)" and §3c "undo coalescing at gesture level" audit items are closed as *removed*, not *built*.

