# HoldFlipNode

## View

| Field | Value |
|-------|-------|
| kind | holdFlip |
| bg | #eceff1 |
| border | #263238 |
| text | #263238 |
| accent | #263238 |
| minWidth | 36 |
| defaultLabel | holdFlip |
| role | hold-flip |
| shape | rect |
| fill | #eceff1 |
| stroke | #263238 |
| width | 36 |
| height | 36 |

## Ports

| Name | Direction | EdgeKind | Notes |
|------|-----------|----------|-------|
| In | in | chain | single received value (0 or 1) |
| Out | out | chain | emits the inverted value (1 - value) |

## Firing rule

Holds the single received value; fires immediately, emitting 1-value on Out, then clears held state.

## Runtime status

- Loader-registered: yes
- TSX render: present
