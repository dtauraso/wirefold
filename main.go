package main

//go:generate go run ./tools/gen-kind-imports

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	W "github.com/dtauraso/wirefold/nodes/Wiring"
)

// runTopology loads and runs the topology under ctx, blocking until ctx is
// cancelled or all nodes exit. Shared by Run and RunTest.
//
// clk is the single monotonic clock every wire reads to time its own delivery
// (MODEL.md). Pass nil to use a production RealClock; tests pass a clock they
// control. The global play/pause gate is this clock's Halt/Resume.
func runTopology(ctx context.Context, cancel context.CancelFunc, tracePath string, topologyPath string, clk W.Clock) {
	tr := T.NewWithSink(0, os.Stdout)

	if clk == nil {
		clk = W.NewRealClock()
	}
	// starts halted; geometry still emits in LoadTopology; first `play` stdin signal resumes.
	clk.Halt()

	nodes, slotReg, _, md, err := W.LoadTopology(ctx, topologyPath, tr, clk)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load topology: %v\n", err)
		os.Exit(1)
	}

	// Emit the full spec to the TS webview before nodes start (Go startup message).
	// TS intercepts this line and sends { type: "load", text } to the webview — it
	// never reads topology/ files directly.
	if err := W.EmitSpecLine(os.Stdout, topologyPath); err != nil {
		fmt.Fprintf(os.Stderr, "emit spec: %v\n", err)
		// non-fatal; continue
	}

	// Launch the per-node and per-edge move-handler goroutines (decentralized
	// node-move: each node/edge drains its own inbox and recomputes its own geometry).
	md.Start(ctx)

	// Read the editor→Go bridge: "edit" JSON lines (op = create/update/delete/fade)
	// from stdin. When stdin reaches EOF (extension host disconnect), cancel the context.
	treeRoot := ""
	if info, err2 := os.Stat(topologyPath); err2 == nil && info.IsDir() {
		treeRoot = topologyPath
	}
	go func() {
		W.RunStdinReader(ctx, os.Stdin, slotReg, md, tr, clk, treeRoot)
		cancel()
	}()

	wg := new(sync.WaitGroup)
	wg.Add(len(nodes))
	for _, node := range nodes {
		go func() {
			defer wg.Done()
			node.Update(ctx)
		}()
	}
	wg.Wait()

	tr.Close()
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

// Run wires the topology and blocks until SIGTERM/SIGINT or stdin EOF.
// This is the live-run path used by the extension host. It uses a production
// RealClock; that clock's Halt/Resume is the global play/pause gate.
func Run(tracePath string, topologyPath string) {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	runTopology(ctx, cancel, tracePath, topologyPath, W.NewRealClock())
}

// RunTest wires the topology and lets it run for dur before cancelling, using a
// production RealClock. Used by automated tests that need a self-terminating run.
func RunTest(dur time.Duration, tracePath string, topologyPath string) {
	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()
	runTopology(ctx, cancel, tracePath, topologyPath, W.NewRealClock())
}

func main() {
	tracePath := flag.String("trace", "", "if set, write a raw JSONL trace to this path on shutdown")
	dur := flag.Duration("duration", 0, "if non-zero, run for this duration then exit (test mode)")
	topologyPath := flag.String("topology", "topology", "path to topology JSON spec")
	flag.Parse()
	if *dur > 0 {
		RunTest(*dur, *tracePath, *topologyPath)
	} else {
		Run(*tracePath, *topologyPath)
	}
}
