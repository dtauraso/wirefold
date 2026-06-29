# WindowAndInhibitLeftGateNode

## View

| Field | Value |
|-------|-------|
| kind | windowAndInhibitLeftGate |
| bg | #fce4ec |
| border | #880e4f |
| text | #880e4f |
| accent | #880e4f |
| minWidth | 110 |
| defaultLabel | windowAndInhibitLeftGate |
| role | window-and-inhibit-left-gate |
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
| ToPassed | out | chain | emits result (1 if Left=0 AND Right=1, else 0) |

## Firing rule

Window AND gate (AND gate with a coincidence window). Polls both inputs concurrently. The window W opens on the first arrival and equals `1.5 × max(SimLatencyMs over input wires)`, recomputed live from current wire geometry.

- If both FromLeft and FromRight arrive within W: hold both for the ~800ms fire dwell (both interior beads visible), then Fire, consume both, compute result = 1 if Left=0 AND Right=1 (NOT(Left) AND Right), else 0. Send result on ToPassed. Reset window. Continue.
- If the window expires before both arrive: Done both held inputs without firing. Reset window. Continue.

## Runtime status

- Loader-registered: yes
- TSX render: present
