package main

//go:generate go run ./tools/gen-kind-imports

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	S "github.com/dtauraso/wirefold/nodes/SafeWorker"
	T "github.com/dtauraso/wirefold/Trace"
	W "github.com/dtauraso/wirefold/nodes/Wiring"

)

// RunTest wires the topology and lets it run for `dur` before
// cancelling. If tracePath is non-empty, a Trace recorder is attached
// to SafeWorker and its raw event stream is dumped as JSON-lines to
// the path on shutdown. Raw form keys send events by (node, port);
// the canonical edge-keyed form requires a spec-aware Resolve step
// (out of scope for the runtime entrypoint).
func RunTest(dur time.Duration, tracePath string, topologyPath string) {
	nodes, err := W.LoadTopology(topologyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load topology: %v\n", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	wg := new(sync.WaitGroup)
	wg.Add(len(nodes))

	// Always stream trace events to stdout as JSONL in real time.
	// Lines starting with {"step": are trace events; all other stdout
	// output is build/log noise the extension routes to OutputChannel.
	tr := T.NewWithSink(0, os.Stdout)
	s := S.SafeWorker{Ctx: ctx, Wg: wg, Trace: tr}

	for _, node := range nodes {
		go node.Update(&s)
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
	topologyPath := flag.String("topology", "topology.json", "path to topology JSON spec")
	flag.Parse()
	RunTest(*dur, *tracePath, *topologyPath)
}
