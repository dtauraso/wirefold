# HoldNode

## View

| Field | Value |
|-------|-------|
| kind | hold |
| bg | #f3e5f5 |
| border | #6a1b9a |
| text | #4a148c |
| accent | #6a1b9a |
| minWidth | 60 |
| displays | held |
| defaultLabel | Hold |
| role | hold |
| shape | rect |
| fill | #f3e5f5 |
| stroke | #6a1b9a |
| width | 60 |
| height | 60 |

## Non-channel fields

| Field | Type | Source | Notes |
|-------|------|--------|-------|
| Held | int | `data.state` | last value received and displayed (no downstream forward) |

## Ports

| Name | Direction | EdgeKind | Notes |
|------|-----------|----------|-------|
| In | in | chain | single received value to hold and display |
| ToHoldNewSendOld | out | chain | declared output to a HoldNewSendOld node (inert) |

## Firing rule

On each value received on In:
1. Fire.
2. Update Held to the received value and emit the held bead.

The Hold is a terminal node: it holds the last received value and displays it; it has no output ports and sends nothing downstream.

## Runtime status

- Loader-registered: yes
- TSX render: present
