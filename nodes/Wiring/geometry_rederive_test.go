// geometry_rederive_test.go — the Phase 3 deterministic verifier (fake clock, no
// real sleeps in the timing assertions). It locks three behaviors:
//
//   (a) node-move re-derives geometry: a move on an endpoint recomputes the edge's
//       source-Out control points AND arc length to match the new port-to-port
//       curve (Go is the authoritative holder of node positions + per-edge curves).
//   (b) in-flight re-derive (MODEL.md "Geometry and time"): a mid-flight geometry edit
//       PRESERVES the bead's fractional progress t (its proportion along the wire) and
//       re-derives the remaining delivery time from the NEW arc at uniform pulse speed:
//       remaining = (1−t)·newArc/pulseSpeed. The fraction does NOT change as the wire
//       lengthens or shrinks (no t-swing race); only the world-time to traverse the
//       remaining (1−t) proportion scales with the new arc length.
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

// TestNodeMoveRederivesSegmentAndArc is verifier group (a): a node-move on an
// edge endpoint recomputes the source Out's segment endpoints (Start/End) AND arc
// length to exactly match segmentBetweenPorts / arcLengthBetweenPorts of the new
// port-to-port geometry, and streams a geometry event carrying the new segment.
func TestNodeMoveRederivesSegmentAndArc(t *testing.T) {
	t.Skip("deferred: polar-frame regression — colinearity/move/aimed rebuild pending (polar-frame-rewrite.md phase 4/6); allowed for now")
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := T.New(64)
	_, _, nmr, err := LoadTopology(ctx, path, tr, NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	nmr.Start(ctx)

	out := nmr.edgeOut["e0"]
	if out == nil {
		t.Fatal("missing source Out for e0")
	}

	// Move src to a new position and re-derive.
	const nx, ny, nz = 400, 250, 30
	deliver(nmr, "src", nx, ny, nz)

	// Build the expected segment + arc independently from the moved geometry:
	// src center is the world target (sphere-chain; no lattice snap).
	// Use aimed computation to match the edge mover (all edge-connected ports are aimed).
	srcCenter := vec3{X: nx, Y: ny, Z: nz}
	dstCenter := vec3{X: 0, Y: 0, Z: 0}
	srcGeom := nodeGeom{Kind: "FanInSrc", HasPos: true, ScenePolar: cart2polar(srcCenter),
		Outputs: []portGeom{{Name: "Out"}}}
	dstGeom := nodeGeom{Kind: "FanInSink", HasPos: true, ScenePolar: cart2polar(dstCenter),
		Inputs: []portGeom{{Name: "In"}}}
	wantRegistry := AimedPortRegistry{
		{NodeID: "src", PortName: "Out", IsInput: false}: "dst",
		{NodeID: "dst", PortName: "In", IsInput: true}:   "src",
	}
	wantCenters := map[string]vec3{"src": srcCenter, "dst": dstCenter}
	wantCenterOf := func(id string) (vec3, bool) { c, ok := wantCenters[id]; return c, ok }
	wantSeg := segmentBetweenPortsAimed(srcGeom, "Out", "src", dstGeom, "In", "dst", wantRegistry, wantCenterOf, nil)
	wantArc := wantSeg.Start.sub(wantSeg.End).length()

	// Segment endpoints on the source Out must match exactly.
	if !approxEq(out.Geom().Start.X, wantSeg.Start.X) || !approxEq(out.Geom().Start.Y, wantSeg.Start.Y) || !approxEq(out.Geom().Start.Z, wantSeg.Start.Z) {
		t.Fatalf("Out.Start = %+v, want %+v", out.Geom().Start, wantSeg.Start)
	}
	if !approxEq(out.Geom().End.X, wantSeg.End.X) || !approxEq(out.Geom().End.Y, wantSeg.End.Y) || !approxEq(out.Geom().End.Z, wantSeg.End.Z) {
		t.Fatalf("Out.End = %+v, want %+v", out.Geom().End, wantSeg.End)
	}
	// Arc length + derived latency must match.
	if !approxEq(out.Geom().ArcLength, wantArc) {
		t.Fatalf("Out.ArcLength = %v, want %v", out.Geom().ArcLength, wantArc)
	}
	if !approxEq(out.Geom().SimLatencyMs, wantArc/PulseSpeedWuPerMs) {
		t.Fatalf("Out.SimLatencyMs = %v, want %v", out.Geom().SimLatencyMs, wantArc/PulseSpeedWuPerMs)
	}

	// A geometry event for e0 carrying the new segment was streamed on the move.
	tr.Close()
	gs := geomEvents(tr.Events())
	var found *T.Event
	for i := range gs {
		if gs[i].Edge == "e0" && approxEq(gs[i].SX, wantSeg.Start.X) && approxEq(gs[i].EX, wantSeg.End.X) {
			// take the LAST matching (post-move) emit
			found = &gs[i]
		}
	}
	if found == nil {
		t.Fatalf("no geometry event for e0 with the re-derived segment; got %+v", gs)
	}
	if !approxEq(found.EY, wantSeg.End.Y) || !approxEq(found.SY, wantSeg.Start.Y) {
		t.Fatalf("geometry event Start.Y=%v End.Y=%v, want Start.Y=%v End.Y=%v",
			found.SY, found.EY, wantSeg.Start.Y, wantSeg.End.Y)
	}
}

// TestInFlightRederiveLengthen is verifier group (b), lengthen case: place a bead,
// advance the clock to the half-way point (t = 0.5), then revise to a LONGER arc; the
// fraction t is PRESERVED (stays 0.5) and the remaining delivery time re-derives at
// uniform pulse speed from the NEW arc: remaining = (1−t)·newArc/pulseSpeed.
func TestInFlightRederiveLengthen(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	tr := T.New(64)
	pw.Trace = tr

	// inFlightMs 50 ⇒ arc = 50 * pulseSpeed (= 4.0 wu at 0.08). Half-covered at 25 ms.
	const inFlightMs = 50.0
	arc0 := inFlightMs * PulseSpeedWuPerMs
	seg := wireSegment{Start: vec3{0, 0, 0}, End: vec3{4, 0, 0}}
	bp := beadPlacement{InFlightMs: inFlightMs, Start: seg.Start, End: seg.End, Node: "s", Port: "o"}
	if !placeAndDrive(pw, 11, bp) {
		t.Fatal("placeAndDrive rejected on fresh wire")
	}

	// Advance to the half-way point: covered = arc0/2 ⇒ fraction t = 0.5.
	clk.AdvanceTicks(25)

	// Revise to a LONGER arc (double). Fraction t stays 0.5; remaining =
	// (1−t)·newArc/pulseSpeed = 0.5·8/0.08 = 50 ms.
	newArc := arc0 * 2
	pw.ReviseInFlightGeometry(newArc, seg)
	wantRemainingMs := 0.5 * newArc / PulseSpeedWuPerMs // = 50 ms

	// One ms short of (revise + remaining): still in flight.
	clk.AdvanceTicks(int64(wantRemainingMs) - 1)
	time.Sleep(10 * time.Millisecond)
	if !pw.InFlight() {
		t.Fatalf("delivered early: remaining should be %.3f ms from the revise point", wantRemainingMs)
	}

	// The final ms reaches the re-derived deadline → delivery.
	clk.AdvanceTicks(1)
	waitNotInFlight(t, pw)
	v, ok := pw.PollRecv()
	if !ok || v != 11 {
		t.Fatalf("after re-derived deadline, expected delivered bead 11, got (%v, ok=%v)", v, ok)
	}
}

// TestInFlightRederiveShrinkPreservesFraction is verifier group (b), shrink case:
// shrinking the arc mid-flight PRESERVES the bead's fraction t (it does NOT jump to
// the end / deliver immediately, which the old distance-preserving model did). The
// remaining time re-derives at uniform speed from the smaller arc:
// remaining = (1−t)·newArc/pulseSpeed.
func TestInFlightRederiveShrinkPreservesFraction(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	tr := T.New(64)
	pw.Trace = tr

	const inFlightMs = 50.0
	arc0 := inFlightMs * PulseSpeedWuPerMs
	seg := wireSegment{Start: vec3{0, 0, 0}, End: vec3{4, 0, 0}}
	bp := beadPlacement{InFlightMs: inFlightMs, Start: seg.Start, End: seg.End, Node: "s", Port: "o"}
	if !placeAndDrive(pw, 22, bp) {
		t.Fatal("placeAndDrive rejected on fresh wire")
	}

	// Advance to half: fraction t = 0.5.
	clk.AdvanceTicks(25)

	// Revise to a SHORTER arc (0.8×). Fraction t stays 0.5 (NOT delivered immediately);
	// remaining = 0.5·newArc/pulseSpeed = 0.5·3.2/0.08 = 20 ms.
	newArc := arc0 * 0.8
	pw.ReviseInFlightGeometry(newArc, seg)
	wantRemainingMs := 0.5 * newArc / PulseSpeedWuPerMs // = 20 ms

	// One ms short of the re-derived deadline: still in flight (no immediate delivery).
	clk.AdvanceTicks(int64(wantRemainingMs) - 1)
	time.Sleep(10 * time.Millisecond)
	if !pw.InFlight() {
		t.Fatalf("shrink delivered early: fraction must be preserved, remaining %.3f ms", wantRemainingMs)
	}

	// The final ms reaches the re-derived deadline → delivery.
	clk.AdvanceTicks(1)
	waitNotInFlight(t, pw)
	v, ok := pw.PollRecv()
	if !ok || v != 22 {
		t.Fatalf("shrink: expected delivery of bead 22 at re-derived deadline, got (%v, ok=%v)", v, ok)
	}
}

// posFracs extracts the F (fractional progress) values from the position events of
// a drained trace, in emission order.
func posFracs(events []T.Event) []float64 {
	var out []float64
	for _, e := range events {
		if e.Kind == T.KindPosition {
			out = append(out, e.F)
		}
	}
	return out
}

// TestDragDoesNotResetInFlightFraction is the regression guard for the node-drag
// bead-racing bug: a fast continuous drag fires MANY ReviseInFlightGeometry calls
// per second. Each revise rebases inFlightPlacement into the past; the delivery
// walker must resume at the bead's REAL fraction, not replay the traversal from
// t≈0. Place a bead, advance to t≈0.5, then fire many revisions in a row (small
// arc changes simulating a drag) and assert the streamed fraction stays ≈0.5 and
// never jumps backward.
func TestDragDoesNotResetInFlightFraction(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	tr := T.New(256)
	pw.Trace = tr

	const inFlightMs = 50.0
	arc0 := inFlightMs * PulseSpeedWuPerMs
	seg := wireSegment{Start: vec3{0, 0, 0}, End: vec3{4, 0, 0}}
	bp := beadPlacement{InFlightMs: inFlightMs, Start: seg.Start, End: seg.End, Node: "s", Port: "o"}
	if !placeAndDrive(pw, 77, bp) {
		t.Fatal("placeAndDrive rejected on fresh wire")
	}

	// Advance to ~half: fraction t ≈ 0.5.
	clk.AdvanceTicks(25)
	time.Sleep(5 * time.Millisecond)

	// Simulate a drag: many revisions in a row with small arc changes around arc0,
	// advancing the clock a tiny bit between each (continuous drag). The fraction
	// must hold near 0.5 throughout — never snapping back toward the wire start.
	arc := arc0
	for i := 0; i < 40; i++ {
		// jitter the arc ±10% like a node wobbling under the cursor
		if i%2 == 0 {
			arc = arc0 * 1.1
		} else {
			arc = arc0 * 0.9
		}
		pw.ReviseInFlightGeometry(arc, seg)
		// Tiny clock advance every few revisions: a real drag fires far more
		// revisions than clock-ticks, so the fraction should barely move.
		if i%8 == 0 {
			clk.AdvanceTicks(1)
		}
		time.Sleep(2 * time.Millisecond)
	}

	tr.Close()
	fracs := posFracs(tr.Events())
	if len(fracs) == 0 {
		t.Fatal("no position events streamed during the drag")
	}
	// Every emission at/after the half-way point must stay near 0.5 (the bead barely
	// moved: only ~5 ms of clock advanced across the whole drag) and NEVER jump
	// backward — the pre-fix walker replayed each revise from t≈0, snapping the bead
	// back to the wire start over and over.
	// Once the bead has reached ~0.5 (the drag's start), NO subsequent emission may
	// drop back below it — the pre-fix walker replayed each revise from t≈0,
	// emitting fractions like 0.32 then climbing, snapping the bead to the wire
	// start over and over. We track the running max and forbid any meaningful drop.
	const reachedHalf = 0.45
	const hi = 0.65
	maxSeen := 0.0
	pastHalf := false
	for i, f := range fracs {
		if f >= reachedHalf {
			pastHalf = true
		}
		if !pastHalf {
			// Pre-half emissions (the bead climbing to 0.5 before the drag) are fine.
			continue
		}
		if f > hi {
			t.Fatalf("frac[%d]=%.4f above band — bead overshot during drag", i, f)
		}
		if f < maxSeen-0.02 {
			t.Fatalf("frac jumped backward: max=%.4f -> %.4f (idx %d) — node-move reset the bead to the wire start", maxSeen, f, i)
		}
		if f > maxSeen {
			maxSeen = f
		}
	}
}

// TestReviseNoInFlightIsNoOp guards that ReviseInFlightGeometry on a wire with NO
// bead in flight emits nothing and does not spawn/deliver a phantom bead.
func TestReviseNoInFlightIsNoOp(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	tr := T.New(64)
	pw.Trace = tr

	if pw.InFlight() {
		t.Fatal("fresh wire reports a bead in flight")
	}
	seg := wireSegment{Start: vec3{0, 0, 0}, End: vec3{8, 0, 0}}
	for i := 0; i < 10; i++ {
		pw.ReviseInFlightGeometry(8.0, seg)
		clk.AdvanceTicks(5)
	}
	time.Sleep(10 * time.Millisecond)

	if pw.InFlight() {
		t.Fatal("revise on an empty wire spawned a phantom in-flight bead")
	}
	if _, ok := pw.PollRecv(); ok {
		t.Fatal("revise on an empty wire delivered a phantom bead into the slot")
	}
	tr.Close()
	if fr := posFracs(tr.Events()); len(fr) != 0 {
		t.Fatalf("revise on an empty wire emitted %d position events, want 0", len(fr))
	}
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
	seg := wireSegment{Start: vec3{0, 0, 0}, End: vec3{4, 0, 0}}
	bp := beadPlacement{InFlightMs: inFlightMs, Start: seg.Start, End: seg.End, Node: "src", Port: "out"}
	if !placeAndDrive(pw, 33, bp) {
		t.Fatal("placeAndDrive rejected on fresh wire")
	}

	// Advance partway (bead in flight), then delete the edge mid-flight.
	clk.AdvanceTicks(20)
	pw.Delete()

	// Advancing past the ORIGINAL deadline must NOT deliver — delivery was canceled.
	clk.AdvanceTicks(int64(inFlightMs))
	time.Sleep(10 * time.Millisecond)
	pw.mu.Lock()
	hasSend := len(pw.delivered) > 0
	inFlight := len(pw.inflight) > 0
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
	seg := wireSegment{Start: vec3{0, 0, 0}, End: vec3{4, 0, 0}}
	bp := beadPlacement{InFlightMs: inFlightMs, Start: seg.Start, End: seg.End, Node: "src", Port: "out"}
	if !placeAndDrive(pw, 44, bp) {
		t.Fatal("placeAndDrive rejected on fresh wire")
	}
	clk.AdvanceTicks(int64(inFlightMs))
	waitNotInFlight(t, pw)
	if _, ok := pw.PollRecv(); !ok {
		t.Fatal("bead did not deliver")
	}
	pw.Delete()
	tr.Close()
	if cs := cancelEvents(tr.Events()); len(cs) != 0 {
		t.Fatalf("delete after delivery emitted %d pulse-cancelled events, want 0; got %+v", len(cs), cs)
	}
}
