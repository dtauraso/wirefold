# ChainInhibitorNode

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
| FromPrevChainInhibitorNode | in | chain | receives value from upstream chain inhibitor |
| ToNext | out | chain | fan-out to downstream nodes (multi-output) |
| FeedbackOut | out | chain | sends step (1=advance, 0=hold) back to Input node; active when wired |

## Firing rule

On each value received from FromPrevChainInhibitorNode:
1. Fire.
2. If FeedbackOut is wired:
   a. Compute step: 1 if value ≠ last seen value (or first recv), else 0.
   b. Send step on FeedbackOut **before** forwarding on ToNext (ordered: feedback precedes next recv so Input unblocks).
   c. Fan-out the current Held value concurrently on all ToNext outputs.
   d. Update Held to value.
3. If FeedbackOut is not wired:
   a. Fan-out the current Held value concurrently on all ToNext outputs.
   b. Update Held to value.

The node parks if any ToNext output wire is still occupied (bead in flight or unconsumed), to prevent drops when output transit time exceeds the input rate.

## Runtime status

- Loader-registered: yes
- TSX render: present
