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

	B "github.com/dtauraso/wirefold/Buffer"
	T "github.com/dtauraso/wirefold/Trace"
	W "github.com/dtauraso/wirefold/nodes/Wiring"
)

// toStreamEvents converts a nodeMover/edgeMover/interiorStream goroutine's own
// row-resolved events (Wiring.RowEvent, string kind — kept Buffer-independent there) into
// Buffer.StreamEvent (numeric kind, via Buffer.KindID) for packing into that SAME
// goroutine's own frame's trailing EVENTS section (memory/feedback_no_single_writer_bridge.md).
// Pure value conversion — no shared state, safe to call from any owner goroutine.
func toStreamEvents(events []W.RowEvent) []B.StreamEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]B.StreamEvent, len(events))
	for i, e := range events {
		out[i] = B.StreamEvent{
			Kind:          B.KindID(e.Kind),
			NodeRow:       e.NodeRow,
			PortRow:       e.PortRow,
			TargetRow:     e.TargetRow,
			TargetPortRow: e.TargetPortRow,
			EdgeRow:       e.EdgeRow,
			Slot:          e.Slot,
			Value:         e.Value,
			Bead:          uint32(e.Bead),
			ArcLength:     float32(e.ArcLength),
			SimLatencyMs:  float32(e.SimLatencyMs),
			X:             float32(e.X),
			Y:             float32(e.Y),
			Z:             float32(e.Z),
			F:             float32(e.F),
		}
	}
	return out
}

// runTopology loads and runs the topology under ctx, blocking until ctx is
// cancelled or all nodes exit. Shared by Run and RunTest.
//
// clk is the single monotonic clock every wire reads to time its own delivery
// (MODEL.md). Both callers (Run, RunTest) pass a real clock; it is always non-nil.
// The clock is free-running (no play/pause gate).
func runTopology(ctx context.Context, cancel context.CancelFunc, topologyPath string, clk W.Clock) {
	// The VIEW stream (camera+overlay+scene, one singleton row) — per-owner buffer rows
	// (per-owner-buffer-rows.md, memory/feedback_no_single_writer_bridge.md): WIREFOLD_STREAM_FDS
	// is now MANDATORY (the fd-3 SnapshotState accumulator + fallback packer were deleted along
	// with this migration's final step — see Buffer/stream_fds.go). The gesture/stdin-reader
	// goroutine (nodes/Wiring's MoveDispatch, wired below once it exists) is the sole WRITER of
	// this stream.
	streamFDs := B.ParseStreamFDs(os.Getenv("WIREFOLD_STREAM_FDS"))
	viewFile, viewStreamWired := streamFDs.Open(B.StreamKindView, 0)
	// Trace is now just the breadcrumb writer (the central event channel/drain and the
	// -trace JSONL dump were deleted — per-owner-buffer-rows.md's final step: every
	// emitting goroutine packs its own frame directly; see Trace/Trace.go's doc comment).
	tr := T.New(0)
	// DEBUG BREADCRUMB channel: production breadcrumbs ride stdout as {"kind":"breadcrumb",...}
	// lines; the ext host routes them to .probe/go-debug.jsonl (see runCommand.ts). This is the
	// Go analogue of the webview's postLog — a cheap, structured, one-call diagnostic that lands
	// in .probe/ without scattering fmt.Fprintf(os.Stderr, ...). It is sparse (control events,
	// not a per-tick firehose) and fire-and-forget.
	tr.SetDebugSink(os.Stdout)

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
	// The per-edge dedicated stream (memory/feedback_no_single_writer_bridge.md): when
	// WIREFOLD_STREAM_FDS carries an "edge" entry, wire every edgeMover to its OWN fd
	// (fd = baseFd + edgeRow, edgeRow = the stable seed order — see
	// MoveDispatch.SetEdgeStreams). No edges (edgeBase absent) leaves every edgeMover's
	// streamOut at its zero value (nil) — there is nothing to stream.
	if edgeBase, ok := streamFDs[B.StreamKindEdge]; ok {
		// Edge selection is no longer an injected lookup: each edgeMover owns its OWN
		// selected bit, set via a moveMsgKindSelect message the gesture goroutine sends
		// on select/deselect (MoveDispatch.sendEdgeSelect).
		md.SetEdgeStreams(edgeBase, md.PortRowFor, md.NodeRowFor,
			func(tick uint32, srcPortRow, dstPortRow int32, selected uint8, label string, beadVal []int32, beadX, beadY, beadZ []float32, events []W.RowEvent) []byte {
				return B.BuildEdgeStreamFrame(tick, srcPortRow, dstPortRow, selected, label, beadVal, beadX, beadY, beadZ, toStreamEvents(events))
			})
	}
	// The two per-node dedicated streams (memory/feedback_no_single_writer_bridge.md):
	// NODE (geometry+ports+label, written by each nodeMover) and INTERIOR (interior
	// beads, written by each node's OWN Update goroutine — the SECOND emitting goroutine
	// per node). Both require the SAME "node" AND "interior" WIREFOLD_STREAM_FDS entries
	// (a node stream with no interior counterpart, or vice versa, would leave one of the
	// two goroutines with nowhere fresh to write while the other has one — so both are
	// required together).
	if nodeBase, ok := streamFDs[B.StreamKindNode]; ok {
		if interiorBase, ok2 := streamFDs[B.StreamKindInterior]; ok2 {
			// Selection/hover/abc-drag/kind are no longer injected lookups: each
			// nodeMover owns its OWN selected/hovered/latchedSel/gotDragMsg/dragDelta*
			// bits, set via moveMsgKindSelect/Hover/Latched/AbcReset messages the gesture
			// goroutine sends (or, for kindID, resolved once here at construction).
			// kindIDFor resolves a node's static load-time kind string to its NODE_DEFS
			// index (Buffer.NodeKindID) — injected so Wiring stays Buffer-independent.
			md.SetNodeStreams(nodeBase, interiorBase,
				md.NodeRowFor, md.EdgeRowForPair,
				func(tick uint32, nodeRow int32, cx, cy, cz, radius, sphereR float32, vrx, vry, vrz, frx, fry, frz float32, selected, kindID, hovered, latchedSel, gotDragMsg uint8, dragDeltaA, dragDeltaB, dragDeltaC int32, label string, portNames []string, portDX, portDY, portDZ, portPX, portPY, portPZ []float32, portIsInput, portHovered []uint8, dstNodeRows, edgeRows []int32, events []W.RowEvent) []byte {
					return B.BuildNodeStreamFrame(tick, nodeRow, cx, cy, cz, radius, sphereR, vrx, vry, vrz, frx, fry, frz,
						selected, kindID, hovered, latchedSel, gotDragMsg, dragDeltaA, dragDeltaB, dragDeltaC,
						label, portNames, portDX, portDY, portDZ, portPX, portPY, portPZ, portIsInput, portHovered,
						dstNodeRows, edgeRows, toStreamEvents(events))
				},
				func(tick uint32, present []uint8, value []int32, ox, oy, oz []float32, events []W.RowEvent) []byte {
					return B.BuildInteriorStreamFrame(tick, present, value, ox, oy, oz, toStreamEvents(events))
				},
				B.NodeKindID)
		}
	}
	// The VIEW stream's write side (Step C, per-owner-buffer-rows.md): wire md as the
	// stream's owner/writer BEFORE anything that can change camera/overlay/scene-sphere/
	// selection/hover reaches it (SeedInitialViewpoint/LoadOverlays/LoadSceneSphere below,
	// then the launched movers/stdin reader) — mirrors SetEdgeStreams/SetNodeStreams'
	// "wire before it can fire" ordering above. Only when the dedicated fd is actually
	// wired (viewStreamWired) — left uncalled otherwise (no WIREFOLD_STREAM_FDS "view"
	// entry, e.g. a non-extension launch with no dedicated pipes at all).
	if viewStreamWired {
		md.SetViewStream(viewFile,
			func(tick uint32,
				camPX, camPY, camPZ, camR, camPosTheta, camPosPhi, camUpTheta, camUpPhi float32,
				sceneTori, scenePoles, nodePoles, selSpherePoles, handholds, labelsGlobal, overlaysVis, doubleLinks uint8,
				abcDragCount uint32,
				sceneCX, sceneCY, sceneCZ, sceneRadius float32,
				events []W.RowEvent,
			) []byte {
				return B.BuildViewStreamFrame(tick,
					camPX, camPY, camPZ, camR, camPosTheta, camPosPhi, camUpTheta, camUpPhi,
					B.OverlayRow{
						SceneTori: sceneTori, ScenePoles: scenePoles, NodePoles: nodePoles,
						SelSpherePoles: selSpherePoles, Handholds: handholds, LabelsGlobal: labelsGlobal,
						OverlaysVis: overlaysVis, DoubleLinks: doubleLinks, AbcDragCount: abcDragCount,
					},
					sceneCX, sceneCY, sceneCZ, sceneRadius,
					toStreamEvents(events))
			})
		// LayoutLink is load-time-once — emit each pair once, now that the view stream is
		// wired, so the .probe log's per-kind count still matches the -trace reference.
		// tr.LayoutLink itself (loader.go's emitLayoutLinks, already run inside
		// LoadTopology above) is UNCHANGED — this is purely the EVENT-block/.probe-log
		// representation; the render-path LayoutLink section is node_mover.go's own
		// layoutLinkTos, carried on each node's own stream frame.
		for _, pair := range md.LayoutLinkPairs() {
			nodeRow := int32(-1)
			if r, ok := md.NodeRowFor(pair[0]); ok {
				nodeRow = r
			}
			targetRow := int32(-1)
			if r, ok := md.NodeRowFor(pair[1]); ok {
				targetRow = r
			}
			md.EmitLayoutLinkViewEvent(nodeRow, targetRow)
		}
	}
	// One example startup breadcrumb — proves the debug channel end-to-end and is genuinely
	// useful (which topology loaded, how many nodes). Sparse: once per run.
	tr.Breadcrumb("topology-loaded", topologyPath, "", fmt.Sprintf("nodes=%d", len(nodes)))

	// Sparse, one-time startup sanity check (CLAUDE.md DEBUG BREADCRUMB channel): every
	// node LoadTopology returned should have a row-seed entry (md.NodeSeeds(), the SAME
	// spec-order row table nodes/Wiring's own move-dispatch/stream wiring above already
	// uses). A mismatch means md.NodeSeeds() (spec order) and LoadTopology's node list
	// diverged — a real topology bug — and must be visible.
	if len(md.NodeSeeds()) != len(nodes) {
		tr.Breadcrumb("row-seed-count-mismatch", "", "", fmt.Sprintf("NodeSeeds=%d nodes=%d", len(md.NodeSeeds()), len(nodes)))
	}

	// Initial camera viewpoint = FILE DATA. Go reads the saved camera from
	// <topologyPath>/view/scene.json itself and installs it into the gesture-FSM viewpoint,
	// so the buffer camera columns carry a real, non-degenerate saved pose from the first
	// frame (pan works immediately). Absent/malformed file → a fixed non-degenerate default.
	//
	// The buffer's node/edge/port row-identity tables now live ON md itself (built once at
	// load, in newMoveDispatch's buildRowTables call, from the same spec-order nodeSeeds/
	// edgeSeeds used to seed SnapshotState's rows below) — a node/edge/port hit (which
	// carries only a numeric buffer row index) resolves back to its identity via
	// md.LookupNodeRow/LookupEdgeRow/LookupPortRow with no separate resolver wiring.
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
	// moverWG covers every nodeMover/edgeMover goroutine Start launched (see its doc
	// comment). Waiting on it is what lets Close() run with nothing still emitting —
	// the reason Trace needs no mutex.
	moverWG := md.Start(ctx)

	// Read the editor→Go bridge: "edit" JSON lines (op = create/update/delete)
	// from stdin. When stdin reaches EOF (extension host disconnect), cancel the context.
	//
	// stdinWG tracks ONLY this dispatch-loop goroutine, not RunStdinReader's internal
	// frame-reader goroutine. That inner goroutine blocks in io.ReadFull(os.Stdin),
	// which does NOT select on ctx — it is unblocked only by closing the fd (which
	// RunStdinReader itself arranges when r is an io.Closer and ctx is done). On a
	// non-pollable fd that close could still leave the read parked, so waiting on it
	// here would turn a leak into a hang. RunStdinReader's dispatch loop, in contrast,
	// selects on ctx.Done() and returns immediately on cancel regardless of the frame
	// reader's state — that promptness is what stdinWG actually certifies. The frame
	// reader goroutine is deliberately left un-waited (detached); in production it
	// outlives the process only as long as it takes the OS to tear down the closed fd,
	// which is bounded by process exit, not by this WaitGroup.
	stdinWG := new(sync.WaitGroup)
	stdinWG.Add(1)
	go func() {
		defer stdinWG.Done()
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

	// Wait for every tracked goroutine to exit — node Update loops, nodeMover/
	// edgeMover goroutines, and the stdin dispatch loop — before closing the trace.
	// No grace timeout: every one of these goroutines' only blocking call is
	// SleepCycle, which selects on ctx.Done(), so cancel-to-return is bounded by one
	// clock tick (~16ms), not by an arbitrary grace window. If a goroutine ever fails
	// to exit, wg.Wait() below hangs visibly instead of silently proceeding past a
	// still-running goroutine — a hang names the bug; a grace timeout hides it.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		moverWG.Wait()
		stdinWG.Wait()
		close(done)
	}()
	<-done
}

// Run wires the topology and blocks until SIGTERM/SIGINT or stdin EOF.
// This is the live-run path used by the extension host. It uses a production
// free-running RealClock (no play/pause gate).
func Run(topologyPath string) {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	runTopology(ctx, cancel, topologyPath, W.NewRealClock())
}

// RunTest wires the topology and lets it run for dur before cancelling, using a
// production RealClock. Used by automated tests that need a self-terminating run.
func RunTest(dur time.Duration, topologyPath string) {
	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()
	runTopology(ctx, cancel, topologyPath, W.NewRealClock())
}

func main() {
	dur := flag.Duration("duration", 0, "if non-zero, run for this duration then exit (test mode)")
	topologyPath := flag.String("topology", "topology", "path to topology JSON spec")
	flag.Parse()
	if *dur > 0 {
		RunTest(*dur, *topologyPath)
	} else {
		Run(*topologyPath)
	}
}
