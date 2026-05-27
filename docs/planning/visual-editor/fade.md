---
branch: task/undo-redo
---

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

- Faded edges render muted (reuse the existing `dimmed` view-state precedent on `NodeData`, `types.ts:61`; add the parallel field for edges on `EdgeData`).
- No pulse is drawn for a faded edge.
- A pulse already **in-flight** on a wire at the instant it is faded is **removed**: call `clearPulse(edgeId)` (`pulse-state.ts:51`). The pulse disappears without completing.

## Go side (firing/delivery)

Go holds an ignore set mirroring the TS ignore lists.

- A **faded node**'s poll loop treats its precondition as unmet and returns immediately — same shape as the existing context/precondition gate (e.g. `readgate/node.go:40`, `inhibitrightgate/node.go:42`). It never fires, so it never `Send`s. Because no `Send` is issued on a faded wire, no `NotifyDelivered` is awaited, so no backpressure deadlock can form (the deadlock that a render-only fade would cause).
- A **faded edge**: the source does not `Send` across it; the destination does not treat it as a satisfied input.

### The one new substrate primitive: dropping an in-flight value

A wire that is `in-flight(v)` at the instant of fade already has a blocked `Send` on it (`paced_wire.go:39`, blocked on `myDone` after filling the slot). Neither `Done` (which delivers) nor any current path releases that `Send` *without* writing the slot. Fade requires a new `PacedWire` operation:

- **`Drop`** (new): the channel's response to a TS fade mark. When TS marks a wire faded, the fade message crosses the bridge and Go calls `Drop` on that `PacedWire` — the capability to remove the in-flight signal lives on the channel itself. `Drop` unblocks the parked `Send` *without* delivery: clears the slot, returns a sentinel (e.g. `ErrFaded`) up through the sender's poll loop, broadcasts on `cond`. The slot is left `empty`; no value is consumed downstream. The sender's loop then re-evaluates its precondition (now faded) and stays inert. The dropped value is gone — unfade does not resurrect it; the node restarts fresh from its current state.

This is the only substrate addition the feature needs.

## Bridge

Fade is a **live control signal**, analogous to the existing global play/pause gate — not a spec/topology change. It crosses webview → host → Go stdin, following the `"delivered"` precedent (`stdin_reader.go:52`).

- New webview→host message kind: `"fade"` carrying the current faded set (node ids + edge ids). Add to `WebviewToHostMsg` / `WEBVIEW_TO_HOST_TYPES` (`messages.ts:35,95`) and to `stdin_reader.go`'s accepted types; keep `tools/check-message-kind-parity.sh` green.
- Go applies the fade set to the `WireRegistry` / per-node ignore sets live and issues `Drop` on any wire that is currently in-flight and newly faded.

## Persistence

Faded state is **view-state**: it serializes with the view and survives reload. On Go (re)start, the editor replays the current faded set to Go after load (the spec itself stays clean — topology is never the carrier of fade).

## Removed

- The entire undo/redo **stack recording system**: command stack, linear history, undo coalescing. Gone, not replaced by an equivalent. The §3a "undo (half-wired)" and §3c "undo coalescing at gesture level" audit items are closed as *removed*, not *built*.

## Open questions (decide before implementation)

1. **Fade message shape:** send the full faded set on every change (simplest, fewest states) vs. per-toggle deltas. Leaning full-set.
2. **Unfade semantics:** on unfade a node resumes polling and fires normally if its precondition holds; dropped in-flight values are gone (not restored). Confirm this "clean restart" is the intended behavior.
3. **Edge view field name:** mirror `dimmed`, or introduce `faded` and derive `dimmed` from it.
