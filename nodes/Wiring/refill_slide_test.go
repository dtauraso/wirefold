package Wiring

import (
	"context"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// TestRefillSlideClockPaced drives the Input node's animated refill slide with a
// FakeClock and asserts the geometry of the slide:
//   - during the slide, row-1 (working/bottom) beads are emitted with their local
//     y interpolating MONOTONICALLY from the row-0 (top) position DOWN to the row-1
//     position (the top row sliding down into the working row);
//   - row-0 (top/backup) is emitted present=false for the whole slide (empty top);
//   - the final frame lands the row-1 beads exactly at their row-1 slot offsets.
//
// The FakeClock makes this deterministic: emitRefillSlide blocks in WaitUntil; the
// test Advances the clock step-by-step to release each frame.
func TestRefillSlideClockPaced(t *testing.T) {
	tr := T.New(0)
	clk := NewFakeClock()
	beads := []int{1, 0}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		emitRefillSlide(ctx, tr, "in", clk, beads)
		close(done)
	}()

	// rowPitch and duration mirror emitRefillSlide.
	rowPitch := interiorSlotOffset(0, 0).Y - interiorSlotOffset(1, 0).Y
	durationMs := rowPitch / PulseSpeedWuPerMs
	step := time.Duration(positionEmitIntervalMs * float64(time.Millisecond))

	// Advance the clock past the full slide duration in 16ms steps. A couple extra
	// steps guarantee the t>=1 landing frame is reached.
	totalSteps := int(durationMs/positionEmitIntervalMs) + 3
	advanceDone := make(chan struct{})
	go func() {
		for i := 0; i < totalSteps; i++ {
			// Give the slide goroutine a chance to park in WaitUntil before each
			// advance so frames are released one at a time (best-effort; correctness
			// does not depend on it — final assertions hold regardless of batching).
			time.Sleep(time.Millisecond)
			clk.Advance(step)
		}
		close(advanceDone)
	}()

	// Timeout derives from the step count (each step sleeps ~1ms) plus generous
	// slack, so it scales with interiorSlideDurationMul rather than a fixed bound.
	timeout := time.Duration(totalSteps)*10*time.Millisecond + 2*time.Second
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("emitRefillSlide did not finish")
	}
	<-advanceDone
	tr.Close()

	row0Y := interiorSlotOffset(0, 0).Y
	row1Y := interiorSlotOffset(1, 0).Y

	var row1Ys []float64
	sawRow0Present := false
	row0Count := 0
	for _, e := range tr.Events() {
		if e.Kind != T.KindNodeBead {
			continue
		}
		switch e.Row {
		case 0:
			row0Count++
			if e.Present {
				sawRow0Present = true
			}
		case 1:
			if e.Col == 0 {
				row1Ys = append(row1Ys, e.Y)
			}
			if !e.Present || e.Value != beads[e.Col] {
				t.Errorf("row-1 col %d: present=%v value=%d, want present=true value=%d",
					e.Col, e.Present, e.Value, beads[e.Col])
			}
		}
	}

	if sawRow0Present {
		t.Error("row-0 (top) was emitted present=true during the slide; want empty (present=false)")
	}
	if row0Count == 0 {
		t.Error("no row-0 frames emitted during the slide")
	}
	if len(row1Ys) < 2 {
		t.Fatalf("got %d row-1 col-0 frames, want >= 2", len(row1Ys))
	}

	// First frame starts at the top (row-0) position; monotonically decreases to
	// the row-1 position; final frame lands exactly on row-1.
	if !approxEq(row1Ys[0], row0Y) {
		t.Errorf("first row-1 frame y = %v, want top position %v", row1Ys[0], row0Y)
	}
	for i := 1; i < len(row1Ys); i++ {
		if row1Ys[i] > row1Ys[i-1]+1e-9 {
			t.Errorf("row-1 y not monotonically descending: frame %d y=%v > frame %d y=%v",
				i, row1Ys[i], i-1, row1Ys[i-1])
		}
	}
	if !approxEq(row1Ys[len(row1Ys)-1], row1Y) {
		t.Errorf("final row-1 frame y = %v, want bottom position %v", row1Ys[len(row1Ys)-1], row1Y)
	}
}
