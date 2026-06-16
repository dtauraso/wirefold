# InhibitorNode

## View

| Field | Value |
|-------|-------|
| kind | chainInhibitor |
| bg | #fff3e0 |
| border | #e65100 |
| text | #bf360c |
| accent | #e65100 |
| minWidth | 90 |
| displays | held |
| defaultLabel | chainInhibitor |
| role | inhibitor |
| shape | rect |
| fill | #fff3e0 |
| stroke | #e65100 |
| width | 90 |
| height | 60 |

## Non-channel fields

| Field | Type | Source | Notes |
|-------|------|--------|-------|
| Held | int | `data.state` | last value forwarded on the downstream chain |

## Ports

| Name | Direction | EdgeKind | Notes |
|------|-----------|----------|-------|
| FromPrevInhibitorNode | in | chain | receives value from upstream chain inhibitor |
| ToNext | out | chain | fan-out to downstream nodes (multi-output) |
| FeedbackOut | out | chain | sends step (1=advance, 0=hold) back to Input node; active when wired; fire-and-forget (no consume acknowledgment) |

## Firing rule

On each value received from FromPrevInhibitorNode:
1. Fire.
2. If FeedbackOut is wired:
   a. Compute step: 1 if value ≠ last seen value (or first recv), else 0.
   b. Place step on FeedbackOut **before** forwarding on ToNext. FeedbackOut is **fire-and-forget**: the node emits the step and does NOT wait for the Input node to consume/acknowledge it (no back-pressure, per MODEL.md). Only the placement is ordered before the fan-out; the node does not block on the feedback round-trip, so the Held value reaches ToNext at fire-time.
   c. Fan-out the current Held value concurrently on all ToNext outputs.
   d. Update Held to value.
3. If FeedbackOut is not wired:
   a. Fan-out the current Held value concurrently on all ToNext outputs.
   b. Update Held to value.

The node parks if any ToNext output wire is still occupied (bead in flight or unconsumed), to prevent drops when output transit time exceeds the input rate.

**Output invariant:** -1 (the empty-Held sentinel) is never sent on an output channel. A fire whose Held is -1 emits nothing on that channel — Held still updates to the received value, only the send is suppressed. FeedbackOut sends a step (0 or 1), never Held, so it is never -1.

## Runtime status

- Loader-registered: yes
- TSX render: present
