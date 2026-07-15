package main

//go:generate go run ./tools/gen-kind-imports

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	B "github.com/dtauraso/wirefold/Buffer"
	T "github.com/dtauraso/wirefold/Trace"
	W "github.com/dtauraso/wirefold/nodes/Wiring"
)

// runTopology loads and runs the topology under ctx, blocking until ctx is
// cancelled or all nodes exit. Shared by Run and RunTest.
//
// clk is the single monotonic clock every wire reads to time its own delivery
// (MODEL.md). Both callers (Run, RunTest) pass a real clock; it is always non-nil.
// The global play/pause gate is this clock's Halt/Resume.
func runTopology(ctx context.Context, cancel context.CancelFunc, tracePath string, topologyPath string, clk W.Clock) {
	// Open the binary snapshot output channel (default fd 3; set WIREFOLD_BUF_OUT_FD=0
	// to disable). Writes are fire-and-forget: if fd 3 is not connected nothing reads
	// it and write errors are silently ignored (on-but-harmless until rollout flip).
	// At rollout flip (a later phase) this becomes the sole framed stdout once JSON
	// trace is removed; for now it runs in parallel on a side file descriptor.
	var snapOut *os.File
	{
		fdNum := 3
		if v := os.Getenv("WIREFOLD_BUF_OUT_FD"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				fdNum = n
			}
		}
		if fdNum > 0 {
			snapOut = os.NewFile(uintptr(fdNum), fmt.Sprintf("fd%d", fdNum))
		}
	}
	snapState := B.NewSnapshotState(snapOut)
	// Coalesce the high-volume KindPosition stream to at most one emit per tick (clock.go: "the
	// tick IS the animation clock", not per-bead-event). clk already exists here (runTopology's
	// parameter), so wire it in directly rather than reordering construction.
	snapState.SetTickSource(clk.Tick)
	// sink=nil: the JSON-trace-on-stdout emitter is REMOVED. Trace still assigns Step, buffers
	// events (WriteJSONL -trace file), and drives snapState.Update (the onEvent hook) which packs
	// the binary content buffer's EVENT block. The .probe log is now the ext-host DECODE of that
	// buffer (buffer-log.ts) — the spec's "one representation including logs". Nothing writes
	// trace-event JSON to stdout; the ext host no longer parses trace lines from stdout.
	tr := T.NewWithSinkHook(0, nil, snapState.Update)
	// DEBUG BREADCRUMB channel: production breadcrumbs ride stdout as {"kind":"breadcrumb",...}
	// lines; the ext host routes them to .probe/go-debug.jsonl (see runCommand.ts). This is the
	// Go analogue of the webview's postLog — a cheap, structured, one-call diagnostic that lands
	// in .probe/ without scattering fmt.Fprintf(os.Stderr, ...). It is sparse (control events,
	// not a per-tick firehose) and fire-and-forget.
	tr.SetDebugSink(os.Stdout)

	// Wire the clock's halted-state trace hook so Halt()/Resume() (the only emit point —
	// see clock.go/Trace.Halted) streams the running-vs-paused truth into the buffer's Clock
	// block. RealClock is the only production Clock implementation; a test/fake Clock (none
	// exist today) simply has no hook and streams nothing, which is safe.
	if rc, ok := clk.(*W.RealClock); ok {
		rc.SetHaltedHook(tr.Halted)
	}

	// starts halted; geometry still emits in LoadTopology; first `play` stdin signal resumes.
	clk.Halt()

	nodes, slotReg, md, err := W.LoadTopology(ctx, topologyPath, tr, clk)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load topology: %v\n", err)
		os.Exit(1)
	}
	// One example startup breadcrumb — proves the debug channel end-to-end and is genuinely
	// useful (which topology loaded, how many nodes). Sparse: once per run.
	tr.Breadcrumb("topology-loaded", topologyPath, "", fmt.Sprintf("nodes=%d", len(nodes)))

	// Initial camera viewpoint = FILE DATA. Go reads the saved camera from
	// <topologyPath>/view/scene.json itself and installs it into the gesture-FSM viewpoint,
	// so the buffer camera columns carry a real, non-degenerate saved pose from the first
	// frame (pan works immediately). Absent/malformed file → a fixed non-degenerate default.
	//
	// Wire the buffer's port-row table into the gesture FSM so a port hit (which carries
	// only a numeric buffer PORT-ROW index) resolves back to its (node, port) here in Go —
	// Go owns the topology and wrote the Port block in that row order.
	md.SetPortRowResolver(snapState)
	// Likewise the edge-row table: an edge hit carries only a numeric buffer EDGE-ROW index;
	// Go resolves it back to its edge label here (Go wrote the Edge block in that row order)
	// to mark the Go-owned edge selection.
	md.SetEdgeRowResolver(snapState)
	// Likewise the node-row table: a node hit carries only a numeric buffer NODE-ROW index;
	// Go resolves it back to its node id here (Go wrote the Node block in that row order) to
	// drag/select the Go-owned node — no node id crosses the bridge.
	md.SetNodeRowResolver(snapState)
	// Initial camera viewpoint = FILE DATA: Go reads the saved camera from
	// <topologyPath>/view/scene.json and installs it into the gesture-FSM viewpoint.
	W.SeedInitialViewpoint(topologyPath, md, tr)
	// Restore persisted fade: seed the FSM's directly-faded sets from scene.json and emit
	// them so the buffer snapshot rebuilds the fade fixpoint from the first frame.
	md.SeedFade(topologyPath, tr)
	// Restore persisted overlay visibility: seed md.ov from scene.json and emit each flag so
	// the buffer streams the saved overlay state from the first frame. Seed BEFORE
	// EnableEditPersist so the seed's own emit does not write the loaded state back.
	md.LoadOverlays(topologyPath, tr)
	// Arm the WRITE side AFTER the seeds: from here, every gesture that changes the FSM
	// viewpoint (orbit/zoom/pan/home) debounces a write of the current pose back to
	// <topologyPath>/view/scene.json's cameraPolar, so navigate-then-reload round-trips.
	// Arming after the seed keeps the seed's own emit from persisting the loaded/default pose.
	md.EnableViewpointPersist(topologyPath)
	// Arm disk persistence for the three FSM-applied edits (node-drag position, ring-move
	// anchor, fade) — debounced Go-side read-modify-writes, armed after the seeds so their
	// own emits do not write loaded state back.
	md.EnableEditPersist(topologyPath)

	// Launch the per-node and per-edge move-handler goroutines (decentralized
	// node-move: each node/edge drains its own inbox and recomputes its own geometry).
	md.Start(ctx)

	// Read the editor→Go bridge: "edit" JSON lines (op = create/update/delete/fade)
	// from stdin. When stdin reaches EOF (extension host disconnect), cancel the context.
	go func() {
		W.RunStdinReader(ctx, os.Stdin, slotReg, md, tr, clk)
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

	// Each node kind also satisfies UpdateLayout (Node interface, layout_holder.go)
	// via the embedded Wiring.LayoutHolder, but nothing calls it today: it is just
	// <-ctx.Done() with no runtime mutation, so a dedicated per-node goroutine
	// would exist purely to block. Spawning that loop is deferred to the slice
	// that gives it actual work (drag-time local-polar recomputation); see
	// memory/project_two_goroutine_node_split.md.

	// Bounded poll (not a fixed sleep) on the atomically-published node-row table —
	// concurrency-safe (NodeRowCount), gives up after 500ms so a load never hangs.
	for i := 0; i < 500 && snapState.NodeRowCount() < len(nodes); i++ {
		time.Sleep(time.Millisecond)
	}
	md.LoadSceneSphere(topologyPath)

	// Wait for all nodes to exit, but never block forever: in a timed/cancelled
	// run (e.g. -duration, or SIGINT) some nodes may be parked on paced-wire
	// delivery gated by a halted clock and so never observe ctx cancellation.
	// On cancellation, Resume the clock so any ctx-aware paced waits proceed and
	// nodes can return, then wait a brief grace and exit regardless. The startup
	// geometry is already emitted (in LoadTopology) before any pacing, so
	// flushing the trace here still captures it.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		clk.Resume()
		select {
		case <-done:
		case <-time.After(250 * time.Millisecond):
		}
	}

	tr.Close()
	// Trace.Close drained every remaining event into snapState.Update; flush a final snapshot
	// so trailing causal events (recv/fire/done/arrive not followed by a position emit) still
	// reach the buffer-decoded .probe log.
	snapState.FinalFlush()
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
