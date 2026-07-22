# HoldNewSendOldNode

## View

| Field | Value |
|-------|-------|
| kind | chainHoldNewSendOld |
| bg | #fff3e0 |
| border | #e65100 |
| text | #bf360c |
| accent | #e65100 |
| minWidth | 90 |
| displays | held |
| defaultLabel | chainHoldNewSendOld |
| role | holdnewsendold |
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
| FromPrevHoldNewSendOldNode | in | chain | receives value from upstream chain holdnewsendold |
| ToNext | out | chain | broadcast to downstream nodes (multi-output) |

## Firing rule

On each value received from FromPrevHoldNewSendOldNode:
1. Fire.
2. Broadcast the current Held value concurrently on all ToNext outputs.
3. Update Held to value.

The HoldNewSendOld is a pure forwarder: it holds the last value and re-emits it on the next fire (feedback now lives on the Pacer kind, not here).

The node parks if any ToNext output wire is still occupied (bead in flight or unconsumed), to prevent drops when output transit time exceeds the input rate.

**Output invariant:** -1 (the empty-Held sentinel) is never sent on an output channel. A fire whose Held is -1 emits nothing on that channel — Held still updates to the received value, only the send is suppressed.

## Runtime status

- Loader-registered: yes
- TSX render: present
