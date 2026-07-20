# wirefold

## The vision

An animated network in three dimensions, modeling `1 * 2 = 2 * 1`.

Arithmetic is normally written on a line and evaluated a step at a time, or drawn as a flat diagram that holds still. Here each side of the equation is a network in 3D space, where multiplication is expressed through geometry and timing rather than as a step to execute. Running the network plays out the arithmetic. Equality is a structure too. Its own nodes connect both sides into one network.

## What this is

Two things in one repo:

1. **A concurrent dataflow runtime in Go.** Behavior emerges from how nodes are wired together, not from procedural code. Goroutines and channels replace conventional control flow.

2. **A visual editor** (vscode webview, Three.js / React Three Fiber). The diagram is the spec, with no codegen step: the editor writes a directory tree of `topology/nodes/<id>/meta.json`, `inputs|outputs/*.json`, and `topology/edges/*.json`, which the runtime loader reads directly at startup. A legacy monolithic `topology.json` form is also still accepted.

## Running it

```bash
go build ./...
go run .
```

The editor lives in [tools/topology-vscode/](tools/topology-vscode/). See its README for vscode extension build/run instructions.

## License

See [LICENSE](LICENSE).
