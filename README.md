# wirefold

A concurrent dataflow system written in Go, paired with a visual editor where the diagram is the source of truth and Go code is generated from it.

## What this is

Two things in one repo:

1. **A dataflow runtime in Go.** Behavior emerges from how nodes are wired together, not from procedural code. Goroutines and channels replace conventional control flow. Primitives include lateral inhibition, contrast detection (XOR edges), partition timing windows, AND-gate reduction trees, and a latch + AND-gate backpressure pattern for safe pipelining.

2. **A visual topology editor** (vscode webview, React Flow) where the diagram is the spec; a runtime loader reads `topology.json` directly at startup — no codegen step required.

## Running it

```bash
go build ./...
go run .
```

The editor lives in [tools/topology-vscode/](tools/topology-vscode/) — see its README for vscode extension build/run instructions.

## License

See [LICENSE](LICENSE).
