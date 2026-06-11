# InhibitRightGateNode

## View

| Field | Value |
|-------|-------|
| kind | inhibitRightGate |
| bg | #fce4ec |
| border | #880e4f |
| text | #880e4f |
| accent | #880e4f |
| minWidth | 110 |
| defaultLabel | inhibitRightGate |
| role | inhibit-right-gate |
| shape | rect |
| fill | #fce4ec |
| stroke | #880e4f |
| width | 110 |
| height | 36 |

## Ports

| Name | Direction | EdgeKind | Notes |
|------|-----------|----------|-------|
| FromLeft | in | chain | left operand |
| FromRight | in | chain | right operand (inhibitor) |
| ToPassed | out | chain | emits result (1 if Left=1 and Right=0, else 0) |

## Firing rule

Coincidence-window gate. Polls both inputs concurrently. The window W opens on the first arrival and equals `1.5 × max(SimLatencyMs over input wires)`, recomputed live from current wire geometry.

- If both FromLeft and FromRight arrive within W: Fire, consume both, compute result = 1 if Left=1 AND Right=0, else 0. Send result on ToPassed. Reset window. Continue.
- If the window expires before both arrive: Done both held inputs without firing. Reset window. Continue.

## Runtime status

- Loader-registered: yes
- TSX render: present
