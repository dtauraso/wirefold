# StartHoldNewSendOldNode

## View

| Field | Value |
|-------|-------|
| kind | chainStartHoldNewSendOld |
| bg | #e0f2f1 |
| border | #00695c |
| text | #004d40 |
| accent | #00695c |
| minWidth | 90 |
| displays | held |
| defaultLabel | chainStartHoldNewSendOld |
| role | startholdnewsendold |
| shape | rect |
| fill | #e0f2f1 |
| stroke | #00695c |
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
| ToNext | out | chain | fan-out to downstream nodes (multi-output) |
| ToInput | out | chain | declared step back to the Input node (inert) |
| ToHoldNewSendOld | out | chain | declared output to another HoldNewSendOld node (inert) |
| FromHoldNewSendOld | in | chain | declared input from another HoldNewSendOld node (inert) |
| FromPulse | in | chain | declared input from a Pulse node (inert) |
| FromHold | in | chain | declared input from a Hold node (inert) |

## Firing rule

Identical to HoldNewSendOld. On each value received from FromPrevHoldNewSendOldNode:
1. Fire.
2. Fan-out the current Held value concurrently on all ToNext outputs.
3. Update Held to value.

The StartHoldNewSendOld is a pure forwarder: it holds the last value and re-emits it on
the next fire. Its ONLY divergence from HoldNewSendOld is layout-time, not bead-time: when
dragged, the move dispatcher applies the move-distance-equalize update to its connected
Pulse and time (HoldNewSendOld) neighbors only, with the connected time neighbor as the
source (see nodes/Wiring/node_move.go equalizeNeighborDistances).

**Output invariant:** -1 (the empty-Held sentinel) is never sent on an output channel. A
fire whose Held is -1 emits nothing on that channel — Held still updates to the received
value, only the send is suppressed.

## Runtime status

- Loader-registered: yes
- TSX render: present
