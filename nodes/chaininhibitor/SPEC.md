# ChainInhibitorNode

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

## Ports

| Name | Direction | EdgeKind | Notes |
|------|-----------|----------|-------|
| FromPrevChainInhibitorNode | in | chain | receives value from upstream chain inhibitor |
| ToNext | out | chain | fan-out to downstream nodes |
| FeedbackOut | out | chain | sends held value (1/0) back to input; geometry-only at this phase |

## Runtime status

- Loader-registered: yes
- TSX render: present

