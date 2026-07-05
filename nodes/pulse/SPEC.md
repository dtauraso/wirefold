# PulseNode

## View

| Field | Value |
|-------|-------|
| kind | pulse |
| bg | #e1f5fe |
| border | #01579b |
| text | #01579b |
| accent | #01579b |
| minWidth | 90 |
| defaultLabel | pulse |
| role | pulse |
| shape | rect |
| fill | #e1f5fe |
| stroke | #01579b |
| width | 90 |
| height | 60 |

## Ports

| Name | Direction | EdgeKind | Notes |
|------|-----------|----------|-------|
| FromInput | in | chain | sampled input value; updates the held value |
| Out | out | chain | continuously drives the held value (starts -1) |
| Out2 | out | chain | optional second continuous output of the same held value (fan to a second destination); inert when unwired |
| ToInput | out | chain | declared output back to an Input node (inert) |
| ToHoldNewSendOld | out | chain | declared output to a HoldNewSendOld node (inert) |
| FromLeftGate | in | chain | declared input from a WindowAndInhibitLeftGate node (inert) |
| FromRightGate | in | chain | declared input from a WindowAndInhibitRightGate node (inert) |

## Firing rule

Sample-and-hold. Holds one int value (initialized to -1) and drives it out
continuously, even before any input arrives. When a value arrives on FromInput,
the held value is updated and subsequent outputs emit the new value. The output
is not precondition-gated — Pulse self-emits -1 from the start.

## Runtime status

- Loader-registered: yes
- TSX render: present
