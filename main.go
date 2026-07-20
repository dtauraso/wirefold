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
// The clock is free-running (no play/pause gate).
func runTopology(ctx context.Context, cancel context.CancelFunc, tracePath string, topologyPath string, clk W.Clock) {
	// Open the binary snapshot output channel (default fd 3; set WIREFOLD_BUF_OUT_FD=0
	// to disable). Writes are fire-and-forget: if fd 3 is not connected nothing reads
	// it and write errors are silently ignored. This is the SOLE framed output channel
	// today — the JSON trace on stdout was already removed (see the sink=nil comment
	// below); there is no pending migration.
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
	// tick IS the animation clock", not per-bead-event).
	//
	// DELIBERATE per-goroutine-clock.md item 6 decision (was previously an accidental default:
	// `clk.Tick` read the ORIGIN clock directly, which nothing ever applies speed changes to
	// post-demolition, so this cadence was silently pinned to speed 1 forever regardless of the
	// slider). snapshot.go's own doc comment already claims "the tick IS the animation clock" —
	// the SAME scaled, speed-aware notion of tick used everywhere else in the network (paced
	// wires, node windows). A frozen stand-in silently redefines "tick" here to mean literal wall
	// tick, contradicting that claim, so this is a correctness/consistency fix, not a cosmetic
	// one (this file's justification is comprehension, never performance).
	//
	// The Trace DRAIN goroutine (Trace/drain.go) is the sole caller of snapState.Update, hence
	// the sole caller of the tickSource func below, hence a legitimate SINGLE owner for its own
	// clock copy plus its own speed channel — exactly the shape every other clock-holder in the
	// network uses (per-goroutine-clock.md), not a second shared object. snapClk is Copy()'d ONCE
	// here (never handed to a second goroutine) and snapSpeedCh is appended to speedSinks below
	// so stdin_reader's speed broadcast reaches it exactly like every node/edge goroutine's own
	// channel.
	snapClk := clk.Copy()
	snapSpeedCh := make(chan float64, 1)
	snapState.SetTickSource(func() int64 {
		W.ApplySpeedNonBlocking(snapClk, snapSpeedCh)
		return snapClk.Tick()
	})
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
	// Give the snapshot builder the same breadcrumb channel, so a layout link dropped for an
	// unresolvable endpoint (resolvableLayoutLinks) surfaces on go-debug.jsonl instead of
	// vanishing silently. Sparse: it fires only when the dropped count changes.
	snapState.SetBreadcrumbSink(tr.Breadcrumb)

	// The clock is free-running (no play/pause gate): it starts ticking at construction
	// and never halts. Startup geometry is NOT emitted here — each node's own goroutine
	// emits its geometry once at startup (below, after this function's node-goroutine
	// launch loop); see the row-seeding comment there for why the buffer's row tables do
	// not depend on that emit order.
	nodes, slotReg, md, speedSinks, err := W.LoadTopology(ctx, topologyPath, tr, clk)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load topology: %v\n", err)
		os.Exit(1)
	}
	// Register the snapshot tick source's own speed channel alongside every other
	// clock-holder's, so a speed change reaches it too (see the SetTickSource comment above).
	speedSinks = append(speedSinks, snapSpeedCh)
	// One example startup breadcrumb — proves the debug channel end-to-end and is genuinely
	// useful (which topology loaded, how many nodes). Sparse: once per run.
	tr.Breadcrumb("topology-loaded", topologyPath, "", fmt.Sprintf("nodes=%d", len(nodes)))

	// Seed the buffer's node/edge row tables from the diagram itself — SPEC ORDER,
	// prefilled with the diagram's own load-time geometry — BEFORE any node goroutine
	// starts (the launch loop below). This makes row order a deterministic projection of
	// the diagram instead of a discovery log built by racing node goroutines to their
	// first geometry emit (CLAUDE.md/MODEL.md); each node's own later EmitGeometry then
	// just overwrites its own pre-assigned row, exactly as node_move.go's newNodeMover
	// already seeds its own atomic snap from this same load-time geometry.
	//
	// This goes through the SAME tr.NodeGeometry/tr.Geometry event pipeline every node's
	// own later startup emit uses — NOT a direct SnapshotState mutation. SnapshotState's
	// own doc comment (Buffer/snapshot.go) requires every method call to come from the
	// single Trace-drain goroutine; a first attempt at this feature called a bypass
	// SnapshotState.SeedNodes/SeedEdges method directly from this (main) goroutine and hit
	// a genuine data race with the drain goroutine — go test panicked inside
	// writeEdgeBlock/SetEdgeRow with a corrupted slice. Routing through tr.NodeGeometry/
	// tr.Geometry instead queues these onto Trace's channel, so the drain goroutine
	// processes them in FIFO order exactly like any other event — single-writer
	// preserved, and ordering (spec order, before any node goroutine can race in) still
	// comes from THIS loop running synchronously before the node-goroutine launch loop
	// below. Ports and edge endpoints are REAL, not placeholder: every node's center is
	// already known at load (b.nodeGeoms), so md.NodeSeeds()/md.EdgeSeeds() compute the
	// same aimed-port and edge-segment geometry the node/edge's own live emit would
	// produce (node_move.go's newMoveDispatch, reusing builders.go's
	// aimedPortPosDir/buildPortGeoms and port_geometry.go's edgeSegment) — the row is
	// fully valid, not degenerate, the instant this loop returns. The node's real startup
	// emit then just re-writes its own pre-assigned row with the identical values
	// (onNodeGeometry's exists-check), which is a no-op in practice.
	for _, sd := range md.NodeSeeds() {
		tr.NodeGeometry(sd.ID, sd.Label, sd.Kind, sd.CX, sd.CY, sd.CZ, sd.Radius, sd.SphereR, sd.Ports,
			sd.VRX, sd.VRY, sd.VRZ, sd.FRX, sd.FRY, sd.FRZ)
	}
	for _, sd := range md.EdgeSeeds() {
		tr.Geometry(sd.Label, sd.SrcNode, sd.DstNode, sd.SX, sd.SY, sd.SZ, sd.EX, sd.EY, sd.EZ)
	}
	// Sparse, one-time startup sanity check (CLAUDE.md DEBUG BREADCRUMB channel): every
	// node LoadTopology returned should have gotten a row-seed entry above. A mismatch
	// means md.NodeSeeds() (spec order) and LoadTopology's node list diverged — a real
	// topology bug — and must be visible, not silently reconciled by a later node
	// goroutine's own emit landing on whatever row it happens to get.
	if len(md.NodeSeeds()) != len(nodes) {
		tr.Breadcrumb("row-seed-count-mismatch", "", "", fmt.Sprintf("NodeSeeds=%d nodes=%d", len(md.NodeSeeds()), len(nodes)))
	}

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
	// Restore persisted overlay visibility: seed md.ov from scene.json and emit each flag so
	// the buffer streams the saved overlay state from the first frame. Seed BEFORE
	// EnableEditPersist so the seed's own emit does not write the loaded state back.
	md.LoadOverlays(topologyPath, tr)
	// Arm the WRITE side AFTER the seeds: from here, every gesture that changes the FSM
	// viewpoint (orbit/zoom/pan/home) debounces a write of the current pose back to
	// <topologyPath>/view/scene.json's cameraPolar, so navigate-then-reload round-trips.
	// Arming after the seed keeps the seed's own emit from persisting the loaded/default pose.
	md.EnableViewpointPersist(topologyPath)
	// Arm disk persistence for the FSM-applied edits (node-drag position, ring-move
	// anchor) — debounced Go-side read-modify-writes, armed after the seeds so their
	// own emits do not write loaded state back.
	md.EnableEditPersist(topologyPath)

	// Install the scene sphere (persisted, or a content-fit centroid for a fresh
	// scene) BEFORE launching the movers and the stdin reader. It only needs the
	// movers to be BUILT (their seeded centers, available since LoadTopology), not
	// running; installing it after Start left md.sceneSphere written unsynchronized
	// while the mover/gesture goroutines could already read it on the drag path.
	md.LoadSceneSphere(topologyPath)

	// Launch the per-node and per-edge move-handler goroutines (decentralized
	// node-move: each node/edge drains its own inbox and recomputes its own geometry).
	md.Start(ctx)

	// Read the editor→Go bridge: "edit" JSON lines (op = create/update/delete)
	// from stdin. When stdin reaches EOF (extension host disconnect), cancel the context.
	go func() {
		W.RunStdinReader(ctx, os.Stdin, slotReg, md, tr, speedSinks)
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

	// Wait for all nodes to exit, but never block forever: in a timed/cancelled
	// run (e.g. -duration, or SIGINT) a node could still be mid-cycle when ctx is
	// cancelled. Pacing loops are all ctx-aware (SleepCycle selects on ctx.Done),
	// so they observe cancellation on their own; we wait a brief grace and exit
	// regardless. The buffer's row tables are already seeded from the diagram
	// (above, before the node-goroutine launch loop), so flushing the trace here
	// still captures a correct scene even if a node's own startup geometry emit
	// never got scheduled.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
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
// free-running RealClock (no play/pause gate).
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
