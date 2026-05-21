# InputNode

## Ports

| Name | Direction | Element type | Cardinality | TSX handle | Side | EdgeKind |
|------|-----------|--------------|-------------|------------|------|----------|
| ToReadGate | out | int | single | ToReadGate | right | chain |

## Firing rule

On each Update call: iterate through `Init` slice by index. For each value, attempt a non-blocking send on ToReadGate. On success, advance the index. Exits when all values have been sent or context is cancelled.

## View

| Field | Value |
|-------|-------|
| kind | input |
| bg | #1a1f2e |
| border | #3fb950 |
| text | #c9d1d9 |
| accent | #3fb950 |
| minWidth | 90 |
| displays | queue, repeat |
| defaultLabel | input |
| role | input |
| shape | rect |
| fill | #e0e0e0 |
| stroke | #666 |
| width | 80 |
| height | 60 |

## Runtime status

- Loader-registered: yes
- TSX render: present

## Open questions

- TSX handle id and Go struct field are now both `ToReadGate` (reconciled per post-fix-5 convention).
