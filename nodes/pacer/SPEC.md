# PacerNode

## View

| Field | Value |
|-------|-------|
| kind | pacer |
| bg | #e8f5e9 |
| border | #2e7d32 |
| text | #1b5e20 |
| accent | #2e7d32 |
| minWidth | 60 |
| displays | held |
| defaultLabel | Pacer |
| role | pacer |
| shape | rect |
| fill | #e8f5e9 |
| stroke | #2e7d32 |
| width | 60 |
| height | 60 |

## Non-channel fields

| Field | Type | Source | Notes |
|-------|------|--------|-------|
| Held | int | `data.state` | last value received from the Input node |

## Ports

| Name | Direction | EdgeKind | Notes |
|------|-----------|----------|-------|
| FromInput | in | chain | receives node 1's emitted value |
| FeedbackOut | out | chain | sends step (1=advance, 0=hold) back to the Input node; fire-and-forget (no consume acknowledgment) |

## Firing rule

On each value received from FromInput:
1. Fire.
2. Compute step: 1 if value ≠ last held value (or first recv), else 0.
3. Place step on FeedbackOut. FeedbackOut is **fire-and-forget**: the node emits the step and does NOT wait for the Input node to consume/acknowledge it (no back-pressure, per MODEL.md).
4. Update Held to value.

**Output invariant:** FeedbackOut sends a step (0 or 1), never Held, so it is never -1.

## Runtime status

- Loader-registered: yes
- TSX render: present
