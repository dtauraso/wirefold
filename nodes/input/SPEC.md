# InputNode

## View

| Field | Value |
|-------|-------|
| kind | input |
| bg | #e0e0e0 |
| border | #666 |
| text | #1a1a1a |
| accent | #3fb950 |
| minWidth | 90 |
| defaultLabel | input |
| role | input |
| shape | rect |
| fill | #e0e0e0 |
| stroke | #666 |
| width | 80 |
| height | 60 |

## Non-channel fields

| Field | Type | Source | Notes |
|-------|------|--------|-------|
| Init | []int | `data.init` | sequence of values to emit |
| Repeat | bool | `data.repeat` | if true, cycle through Init indefinitely |

## Ports

| Name | Direction | EdgeKind | Optional | Notes |
|------|-----------|----------|----------|-------|
| ToChainInhibitor | out | chain |  | forwards Init values to the chain inhibitor |
| FeedbackIn | in | chain | yes | receives step (1=advance, 0=hold index) from ChainInhibitor; enables feedback-ring mode when wired |

## Firing rule

Plain emit path (FeedbackIn not wired): iterate through Init (wrapping if Repeat), Fire and send each value on ToChainInhibitor in order. Exit when all values sent (or never if Repeat).

Feedback-ring path (FeedbackIn wired): iterate indefinitely (index `i` starting at 0).
1. Fire.
2. Send Init[i % len(Init)] on ToChainInhibitor.
3. Block on FeedbackIn for a step value `s` from ChainInhibitor.
4. Advance: `i = (i + s) % len(Init)`.
5. Loop (exit on ctx cancel or wire close).

The first send (i=0) is the ring seed; there is no t=0 deadlock because the send precedes the first FeedbackIn read.

## Runtime status

- Loader-registered: yes
- TSX render: present

## Default data

```json
{ "init": [0, 1] }
```
