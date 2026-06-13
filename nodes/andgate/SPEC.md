# AndGateNode

## View

| Field | Value |
|-------|-------|
| kind | andGate |
| bg | #fce4ec |
| border | #880e4f |
| text | #880e4f |
| accent | #880e4f |
| minWidth | 110 |
| defaultLabel | andGate |
| role | and-gate |
| shape | rect |
| fill | #fce4ec |
| stroke | #880e4f |
| width | 80 |
| height | 60 |

## Ports

| Name | Direction | EdgeKind | Notes |
|------|-----------|----------|-------|
| FromLeft | in | chain | left operand |
| FromRight | in | chain | right operand |
| ToPassed | out | chain | emits result (1 if Left=1 AND Right=1, else 0) |

## Firing rule

Coincidence-window AND gate. Polls both inputs concurrently. The window W opens on the first arrival and equals `1.5 × max(SimLatencyMs over input wires)`, recomputed live from current wire geometry.

- If both FromLeft and FromRight arrive within W: hold both for the ~800ms fire dwell (both interior beads visible), then Fire, consume both, compute result = 1 if Left=1 AND Right=1, else 0. Send result on ToPassed. Reset window. Continue.
- If the window expires before both arrive: Done both held inputs without firing. Reset window. Continue.

## Runtime status

- Loader-registered: yes
- TSX render: present
