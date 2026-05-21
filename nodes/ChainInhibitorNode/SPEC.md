# ChainInhibitorNode

## Ports

| Name | Direction | Element type | Cardinality | TSX handle | Side | EdgeKind |
|------|-----------|--------------|-------------|------------|------|----------|
| FromPrevChainInhibitorNode | in | int | single | FromPrevChainInhibitorNode | left | chain |
| ToNext0 | out | int | single | ToNext0 | right | chain |
| ToNext1 | out | int | single | ToNext1 | right | chain |

## Non-channel fields

| Field | Type | Source | Notes |
|-------|------|--------|-------|
| HeldValue | int | `data.initialSlots.held` | Initial value held in the inhibitor slot; defaults to 0 |

## Firing rule

Block until a value arrives on FromPrevChainInhibitorNode. On each arrival:

1. Emit HeldValue on ToNext0 and ToNext1 (if wired).
2. Update HeldValue = incoming value.

HeldValue is the previously-held value. Both ToNext0 and ToNext1 receivers get the old value. Destinations include downstream ChainInhibitor nodes, edge-gate inputs, and the ReadGate pacing port.

## View

| Field | Value |
|-------|-------|
| kind | chainInhibitor |
| bg | #fff3e0 |
| border | #e65100 |
| text | #bf360c |
| accent | #e65100 |
| minWidth | 90 |
| displays | held |
| defaultLabel | chainInhibitor |
| role | inhibitor |
| shape | rect |
| fill | #fff3e0 |
| stroke | #e65100 |
| width | 90 |
| height | 60 |

## Runtime status

- Loader-registered: yes
- TSX render: present

