package main

//go:generate go run ./tools/gen-kind-imports

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	W "github.com/dtauraso/wirefold/nodes/Wiring"
)

// RunTest wires the topology and lets it run for `dur` before
// cancelling. If tracePath is non-empty, a Trace recorder is attached
// and its raw event stream is dumped as JSON-lines to the path on
// shutdown. Raw form keys send events by (node, port); the canonical
// edge-keyed form requires a spec-aware Resolve pass (out of scope for
// the runtime entrypoint).
func RunTest(dur time.Duration, tracePath string, topologyPath string) {
	// Always stream trace events to stdout as JSONL in real time.
	// Trace event lines are routed by the extension to the webview;
	// all other stdout output is build/log noise sent to OutputChannel.
	tr := T.NewWithSink(0, os.Stdout)

	nodes, err := W.LoadTopology(topologyPath, tr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load topology: %v\n", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	wg := new(sync.WaitGroup)
	wg.Add(len(nodes))

	for _, node := range nodes {
		go func() {
			defer wg.Done()
			node.Update(ctx)
		}()
	}
	time.Sleep(dur)
	cancel()
	wg.Wait()

	tr.Close()
	// Optionally also write to a file for offline diffing/replay.
	if tracePath != "" {
		f, err := os.Create(tracePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "trace write: %v\n", err)
			return
		}
		defer f.Close()
		if err := tr.WriteJSONL(f); err != nil {
			fmt.Fprintf(os.Stderr, "trace write: %v\n", err)
		}
	}
}

func main() {
	tracePath := flag.String("trace", "", "if set, write a raw JSONL trace to this path on shutdown")
	dur := flag.Duration("duration", 100*time.Millisecond, "how long to run before cancelling")
	topologyPath := flag.String("topology", "topologies/line.json", "path to topology JSON spec")
	flag.Parse()
	RunTest(*dur, *tracePath, *topologyPath)
}
