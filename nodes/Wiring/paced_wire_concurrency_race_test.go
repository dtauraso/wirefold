// paced_wire_concurrency_race_test.go — CHECKS BY CODE the concurrency PacedWire.mu's
// doc comment claims to guard. No prior test in this package drove PacedWire's real
// cross-goroutine contention under -race (grepped: no PacedWire/paced_wire_test.go race
// test existed).
//
// The ACTUAL contending goroutines (traced from every non-test production call site,
// grepped below), not the goroutines the pre-existing doc comment merely asserted:
//   - placeBeadNoWalker(At) and Out.StepOnce(At): both called only via the source node's
//     OWN Out (ports.go PlaceDriven/PlaceDrivenAt, Out.StepOnce/StepOnceAt) -- i.e. the
//     SOURCE node's own driving goroutine (e.g. gatecommon/drive.go DriveHeld,
//     holdnewsendold/node.go's Update loop, pacer/node.go). Same goroutine as each
//     other, sequential, not concurrent with itself.
//   - PollRecv/PollRecvTick: called only via the DESTINATION node's own In
//     (ports.go In.PollRecv) -- a DIFFERENT goroutine than the source side (every
//     PollRecv call site above is in a *different* node package's Update loop than the
//     StepOnce/PlaceDriven call sites).
//   - ReviseInFlightGeometry: called only by edgeMover.recomputeGeometry
//     (node_mover.go), i.e. the EDGE's own move-handler goroutine -- a THIRD goroutine,
//     distinct from both node goroutines, fired on a node-move/anchor edit.
//
// So one wire's mu is genuinely contended by three independent goroutines: source
// (place+step), dest (recv), edge (geometry revise). This test drives exactly that.
package Wiring

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestPacedWireSourceDestEdgeConcurrentRace drives one PacedWire the way production
// actually does: a source goroutine repeatedly placing beads and stepping them, a dest
// goroutine repeatedly polling for delivered values, and an edge goroutine repeatedly
// revising in-flight geometry -- all concurrently for a bounded window. Run under
// `go test -race`, this reports a DATA RACE if pw.mu stops guarding the fields these
// three goroutines actually touch (inflight, delivered, nextGen).
func TestPacedWireSourceDestEdgeConcurrentRace(t *testing.T) {
	pw := NewPacedWire(0, PulseSpeedWuPerMs)
	// Per-goroutine-clock model: each of the three goroutines below owns its
	// OWN clock copy, Copy()'d once at its own start, exactly as production
	// does (docs/planning/visual-editor/per-goroutine-clock.md) — not one
	// shared clock read from all three.
	origin := NewRealClock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const window = 300 * time.Millisecond
	deadline := time.Now().Add(window)

	var wg sync.WaitGroup

	// Source goroutine: place + step, exactly as Out.PlaceDrivenAt/Out.StepOnceAt do
	// from the source node's own driving goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		clk := origin.Copy()
		val := 0
		for time.Now().Before(deadline) {
			val++
			pw.placeBeadNoWalkerAt(val, beadPlacement{InFlightMs: 4, Start: vec3{}, End: vec3{X: 1}, Node: "src", Port: "Out"}, clk.Tick())
			pw.StepOnceAt(ctx, clk.Tick())
		}
	}()

	// Dest goroutine: poll-receive, exactly as In.PollRecv does from the destination
	// node's own goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			pw.PollRecvTick()
		}
	}()

	// Edge goroutine: revise in-flight geometry, exactly as edgeMover.recomputeGeometry
	// does from the edge's own move-handler goroutine on a node-move/anchor edit.
	wg.Add(1)
	go func() {
		defer wg.Done()
		clk := origin.Copy()
		i := 0.0
		for time.Now().Before(deadline) {
			i++
			pw.ReviseInFlightGeometry(clk.Tick(), 4+i*0.01, wireSegment{Start: vec3{}, End: vec3{X: 1 + i*0.001}})
		}
	}()

	wg.Wait()
}
