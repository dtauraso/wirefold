# LatchNode

## View

| Field | Value |
|-------|-------|
| kind | latch |
| bg | #e8f5e9 |
| border | #1b5e20 |
| text | #1b5e20 |
| accent | #1b5e20 |
| minWidth | 36 |
| defaultLabel | latch |
| role | latch |
| shape | rect |
| fill | #e8f5e9 |
| stroke | #1b5e20 |
| width | 36 |
| height | 36 |

## Ports

| Name | Direction | EdgeKind | Notes |
|------|-----------|----------|-------|
| FromInput | in | chain | sampled input value; updates the held value |
| Out | out | chain | continuously drives the held value (starts -1) |

## Firing rule

Sample-and-hold. Holds one int value (initialized to -1) and drives it out
continuously, even before any input arrives. When a value arrives on FromInput,
the held value is updated and subsequent outputs emit the new value. The output
is not precondition-gated — Latch self-emits -1 from the start.

## Runtime status

- Loader-registered: yes
- TSX render: present
