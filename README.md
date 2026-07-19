# wirefold

A concurrent dataflow system written in Go, paired with a visual editor where the diagram is the spec — no codegen step required.

## The vision

Animated networks of different nodes showing the process of `1 * 2 = 2 * 1`.

Both sides produce the same number, but they are not the same process: one is two taken once, the other is one taken twice. Each is a different topology of nodes, wired differently and animating differently, arriving at the same result.

## What this is

Two things in one repo:

1. **A dataflow runtime in Go.** Behavior emerges from how nodes are wired together, not from procedural code. Goroutines and channels replace conventional control flow.

2. **A visual topology editor** (vscode webview, Three.js / React Three Fiber) where the diagram is the spec; a runtime loader reads the topology — a directory tree of `topology/nodes/<id>/meta.json`, `inputs|outputs/*.json`, and `topology/edges/*.json` (a legacy monolithic `topology.json` form is also still accepted) — directly at startup.

## Running it

```bash
go build ./...
go run .
```

The editor lives in [tools/topology-vscode/](tools/topology-vscode/) — see its README for vscode extension build/run instructions.

## License

See [LICENSE](LICENSE).
