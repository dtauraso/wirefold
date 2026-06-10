// geometry_rederive_test.go — the Phase 3 deterministic verifier (fake clock, no
// real sleeps in the timing assertions). It locks three behaviors:
//
//   (a) node-move re-derives geometry: a move on an endpoint recomputes the edge's
//       source-Out control points AND arc length to match the new port-to-port
//       curve (Go is the authoritative holder of node positions + per-edge curves).
//   (b) in-flight re-derive (MODEL.md "Geometry and time"): with distanceCovered =
//       pulseSpeed × elapsed, a mid-flight geometry edit re-derives the remaining
//       delivery time from (newArc − covered)/pulseSpeed — and delivers immediately
//       when newArc ≤ covered.
//   (c) delete-mid-flight: deleting an edge while a bead is in flight cancels the
//       clock-delivery (no delivery ever lands) and emits a pulse-cancelled event
//       so the renderer drops the sprite.
//
// Timing is driven by advancing a FakeClock explicitly; the only sleeps are short
// guards waiting for the delivery goroutine to settle (no assertion depends on
// wall-clock duration).

package Wiring

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// cancelEvents extracts the KindPulseCancelled events from a drained trace.
func cancelEvents(events []T.Event) []T.Event {
	var out []T.Event
	for _, e := range events {
		if e.Kind == T.KindPulseCancelled {
			out = append(out, e)
		}
	}
	return out
}

// geomEvents extracts the KindGeometry events from a drained trace.
func geomEvents(events []T.Event) []T.Event {
	var out []T.Event
	for _, e := range events {
		if e.Kind == T.KindGeometry {
			out = append(out, e)
		}
	}
	return out
}

// waitNotInFlight spins (with a guard) until the wire's delivery goroutine has
// cleared inFlight. No assertion depends on the wall-clock duration.
func waitNotInFlight(t *testing.T, pw *PacedWire) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for pw.InFlight() {
		if time.Now().After(deadline) {
			t.Fatal("bead never delivered (inFlight stayed true)")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestNodeMoveRederivesControlPointsAndArc is verifier group (a): a node-move on an
// edge endpoint recomputes the source Out's control points (P0/P1/P2) AND arc
// length to exactly match curveBetweenPorts / arcLengthBetweenPorts of the new
// port-to-port geometry, and streams a geometry event carrying the new curve.
func TestNodeMoveRederivesControlPointsAndArc(t *testing.T) {
	const topo = `{
	  "nodes": [
	    {"id":"src","type":"FanInSrc","outputs":[{"name":"Out"}]},
	    {"id":"dst","type":"FanInSink","inputs":[{"name":"In"}]}
	  ],
	  "edges": [
	    {"label":"e0","kind":"data","source":"src","sourceHandle":"Out","target":"dst","targetHandle":"In"}
	  ],
	  "view": {"nodes": {
	    "src": {"x": 100, "y": 0, "z": 0},
	    "dst": {"x": 0,   "y": 0, "z": 0}
	  }}
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	tr := T.New(64)
	_, _, _, nmr, err := LoadTopology(ctx, path, tr, NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	out := nmr.edgeOut["e0"]
	if out == nil {
		t.Fatal("missing source Out for e0")
	}

	// Move src to a new position and re-derive.
	const nx, ny, nz = 400, 250, 30
	nmr.applyNodeMove("src", nx, ny, nz)

	// Build the expected curve + arc independently from the moved geometry.
	srcGeom := nodeGeom{Kind: "FanInSrc", Pos: vec3{X: nx, Y: ny, Z: nz},
		Outputs: []portGeom{{Name: "Out"}}}
	dstGeom := nodeGeom{Kind: "FanInSink", Pos: vec3{X: 0, Y: 0, Z: 0},
		Inputs: []portGeom{{Name: "In"}}}
	wantCurve := curveBetweenPorts(srcGeom, "Out", dstGeom, "In")
	wantArc := arcLengthBetweenPorts(srcGeom, "Out", dstGeom, "In")

	// Control points on the source Out must match exactly.
	if !approxEq(out.P0.X, wantCurve.P0.X) || !approxEq(out.P0.Y, wantCurve.P0.Y) || !approxEq(out.P0.Z, wantCurve.P0.Z) {
		t.Fatalf("Out.P0 = %+v, want %+v", out.P0, wantCurve.P0)
	}
	if !approxEq(out.P1.X, wantCurve.P1.X) || !approxEq(out.P1.Y, wantCurve.P1.Y) || !approxEq(out.P1.Z, wantCurve.P1.Z) {
		t.Fatalf("Out.P1 = %+v, want %+v", out.P1, wantCurve.P1)
	}
	if !approxEq(out.P2.X, wantCurve.P2.X) || !approxEq(out.P2.Y, wantCurve.P2.Y) || !approxEq(out.P2.Z, wantCurve.P2.Z) {
		t.Fatalf("Out.P2 = %+v, want %+v", out.P2, wantCurve.P2)
	}
	// Arc length + derived latency must match.
	if !approxEq(out.ArcLength, wantArc) {
		t.Fatalf("Out.ArcLength = %v, want %v", out.ArcLength, wantArc)
	}
	if !approxEq(out.SimLatencyMs, wantArc/PulseSpeedWuPerMs) {
		t.Fatalf("Out.SimLatencyMs = %v, want %v", out.SimLatencyMs, wantArc/PulseSpeedWuPerMs)
	}

	// A geometry event for e0 carrying the new curve was streamed on the move.
	tr.Close()
	gs := geomEvents(tr.Events())
	var found *T.Event
	for i := range gs {
		if gs[i].Edge == "e0" && approxEq(gs[i].P2X, wantCurve.P2.X) && approxEq(gs[i].P0X, wantCurve.P0.X) {
			// take the LAST matching (post-move) emit
			found = &gs[i]
		}
	}
	if found == nil {
		t.Fatalf("no geometry event for e0 with the re-derived curve; got %+v", gs)
	}
	if !approxEq(found.P1X, wantCurve.P1.X) || !approxEq(found.P1Y, wantCurve.P1.Y) {
		t.Fatalf("geometry event P1 = (%v,%v), want (%v,%v)", found.P1X, found.P1Y, wantCurve.P1.X, wantCurve.P1.Y)
	}
}

// TestInFlightRederiveLengthen is verifier group (b), lengthen case: place a bead,
// advance the clock so distanceCovered = ~half the arc, then revise to a LONGER
// arc; the remaining delivery time must re-derive from (newArc − covered)/pulseSpeed.
func TestInFlightRederiveLengthen(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	tr := T.New(64)
	pw.Trace = tr

	// inFlightMs 50 ⇒ arc = 50 * pulseSpeed (= 4.0 wu at 0.08). Half-covered at 25 ms.
	const inFlightMs = 50.0
	arc0 := inFlightMs * PulseSpeedWuPerMs
	curve := edgeCurve{P0: vec3{0, 0, 0}, P1: vec3{2, 4, 0}, P2: vec3{4, 0, 0}}
	bp := beadPlacement{InFlightMs: inFlightMs, P0: curve.P0, P1: curve.P1, P2: curve.P2, Node: "s", Port: "o"}
	if !pw.TryPlace(11, bp) {
		t.Fatal("TryPlace rejected on fresh wire")
	}

	// Advance to the half-way point: covered = pulseSpeed * 25 = arc0/2.
	clk.Advance(25 * time.Millisecond)
	covered := PulseSpeedWuPerMs * 25.0
	if !approxEq(covered, arc0/2) {
		t.Fatalf("setup: covered=%v, want arc0/2=%v", covered, arc0/2)
	}

	// Revise to a LONGER arc (double). Remaining = (newArc - covered)/pulseSpeed.
	newArc := arc0 * 2
	pw.ReviseInFlightGeometry(newArc, curve)
	wantRemainingMs := (newArc - covered) / PulseSpeedWuPerMs // = (8-2)/0.08 = 75 ms

	// One ms short of (25 + remaining): still in flight.
	clk.Advance(time.Duration(wantRemainingMs-1) * time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	if !pw.InFlight() {
		t.Fatalf("delivered early: remaining should be %.3f ms from the revise point", wantRemainingMs)
	}

	// The final ms reaches the re-derived deadline → delivery.
	clk.Advance(1 * time.Millisecond)
	waitNotInFlight(t, pw)
	v, ok := pw.PollRecv()
	if !ok || v != 11 {
		t.Fatalf("after re-derived deadline, expected delivered bead 11, got (%v, ok=%v)", v, ok)
	}
}

// TestInFlightRederiveShrinkImmediate is verifier group (b), shrink case: when the
// revised arc is BELOW the distance already covered, the traversal completes
// immediately (no further clock advance needed).
func TestInFlightRederiveShrinkImmediate(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	tr := T.New(64)
	pw.Trace = tr

	const inFlightMs = 50.0
	arc0 := inFlightMs * PulseSpeedWuPerMs
	curve := edgeCurve{P0: vec3{0, 0, 0}, P1: vec3{2, 4, 0}, P2: vec3{4, 0, 0}}
	bp := beadPlacement{InFlightMs: inFlightMs, P0: curve.P0, P1: curve.P1, P2: curve.P2, Node: "s", Port: "o"}
	if !pw.TryPlace(22, bp) {
		t.Fatal("TryPlace rejected on fresh wire")
	}

	// Advance to half: covered = arc0/2.
	clk.Advance(25 * time.Millisecond)
	covered := PulseSpeedWuPerMs * 25.0

	// Revise to an arc BELOW covered → traversal completes immediately, with NO
	// further Advance (the re-derived deadline is already behind the current clock).
	newArc := covered * 0.5
	if !(newArc < covered) {
		t.Fatalf("setup: newArc %v must be < covered %v", newArc, covered)
	}
	pw.ReviseInFlightGeometry(newArc, curve)

	waitNotInFlight(t, pw) // delivers without any Advance
	v, ok := pw.PollRecv()
	if !ok || v != 22 {
		t.Fatalf("shrink: expected immediate delivery of bead 22, got (%v, ok=%v)", v, ok)
	}
	_ = arc0
}

// TestDeleteMidFlightCancels is verifier group (c): deleting an edge while a bead
// is in flight cancels the clock-delivery (no delivery ever lands, even after
// advancing past the original deadline) and emits a pulse-cancelled event keyed by
// the bead's source node+port.
func TestDeleteMidFlightCancels(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	tr := T.New(64)
	pw.Trace = tr

	const inFlightMs = 50.0
	curve := edgeCurve{P0: vec3{0, 0, 0}, P1: vec3{2, 4, 0}, P2: vec3{4, 0, 0}}
	bp := beadPlacement{InFlightMs: inFlightMs, P0: curve.P0, P1: curve.P1, P2: curve.P2, Node: "src", Port: "out"}
	if !pw.TryPlace(33, bp) {
		t.Fatal("TryPlace rejected on fresh wire")
	}

	// Advance partway (bead in flight), then delete the edge mid-flight.
	clk.Advance(20 * time.Millisecond)
	pw.Delete()

	// Advancing past the ORIGINAL deadline must NOT deliver — delivery was canceled.
	clk.Advance(inFlightMs * time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	pw.mu.Lock()
	hasSend := pw.hasSend
	inFlight := pw.inFlight
	pw.mu.Unlock()
	if hasSend {
		t.Fatal("delete-mid-flight delivered the bead; delete must cancel clock-delivery")
	}
	if inFlight {
		t.Fatal("delete-mid-flight left the bead in flight; delete must drop it")
	}
	if _, ok := pw.PollRecv(); ok {
		t.Fatal("delete-mid-flight left a value in the slot")
	}

	// A pulse-cancelled event was emitted for the dropped bead, keyed by source.
	tr.Close()
	cs := cancelEvents(tr.Events())
	if len(cs) != 1 {
		t.Fatalf("emitted %d pulse-cancelled events, want exactly 1; got %+v", len(cs), cs)
	}
	c := cs[0]
	if c.Node != "src" || c.Port != "out" {
		t.Fatalf("pulse-cancelled routing key = (%q,%q), want (\"src\",\"out\")", c.Node, c.Port)
	}
	if c.Value != 33 {
		t.Fatalf("pulse-cancelled value = %d, want 33", c.Value)
	}
}

// TestDeleteAfterDeliveryNoCancel guards the converse: deleting a wire whose bead
// already delivered (and was consumed) must NOT emit a spurious pulse-cancelled —
// only an in-flight bead is echoed as cancelled.
func TestDeleteAfterDeliveryNoCancel(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	tr := T.New(64)
	pw.Trace = tr

	const inFlightMs = 50.0
	curve := edgeCurve{P0: vec3{0, 0, 0}, P1: vec3{2, 4, 0}, P2: vec3{4, 0, 0}}
	bp := beadPlacement{InFlightMs: inFlightMs, P0: curve.P0, P1: curve.P1, P2: curve.P2, Node: "src", Port: "out"}
	if !pw.TryPlace(44, bp) {
		t.Fatal("TryPlace rejected on fresh wire")
	}
	clk.Advance(inFlightMs * time.Millisecond)
	waitNotInFlight(t, pw)
	if _, ok := pw.PollRecv(); !ok {
		t.Fatal("bead did not deliver")
	}
	pw.Done() // consume

	pw.Delete()
	tr.Close()
	if cs := cancelEvents(tr.Events()); len(cs) != 0 {
		t.Fatalf("delete after delivery emitted %d pulse-cancelled events, want 0; got %+v", len(cs), cs)
	}
}
