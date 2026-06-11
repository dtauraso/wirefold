# ReadGateNode

## View

| Field | Value |
|-------|-------|
| kind | readGate |
| bg | #f3e5f5 |
| border | #7b1fa2 |
| text | #4a148c |
| accent | #7b1fa2 |
| minWidth | 70 |
| defaultLabel | readgate |
| role | and-gate |
| shape | rect |
| fill | #f3e5f5 |
| stroke | #7b1fa2 |
| width | 70 |
| height | 40 |

## Ports

| Name | Direction | EdgeKind | Notes |
|------|-----------|----------|-------|
| FromInput | in | chain | value from upstream Input node |
| FromChainInhibitor | in | chain | gate signal from ChainInhibitor |
| ToChainInhibitor | out | chain | forwards the value from FromInput to the next ChainInhibitor |

## Firing rule

Coincidence-window AND-gate. Polls both inputs concurrently. The window W opens on the first arrival and equals `1.5 × max(SimLatencyMs over input wires)`, recomputed live from current wire geometry.

- If both FromInput and FromChainInhibitor arrive within W: Fire, consume both, send the FromInput value on ToChainInhibitor. Reset window. Continue.
- If the window expires before both arrive: Done both held inputs without firing (so any gated upstream source's WaitConsumed returns). Reset window. Continue.

The FromChainInhibitor value is not inspected; only its arrival matters.

## Runtime status

- Loader-registered: yes
- TSX render: present
