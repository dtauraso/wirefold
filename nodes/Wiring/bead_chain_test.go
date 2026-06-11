package Wiring

import (
	"math"
	"testing"
	"time"
)

// drainAndSettle reads from observer until no new report arrives for quietPeriod.
// Returns a map of item ID → latest ItemReport.
func drainAndSettle(observer chan ItemReport, quietPeriod time.Duration) map[int]ItemReport {
	state := make(map[int]ItemReport)
	timer := time.NewTimer(quietPeriod)
	defer timer.Stop()
	for {
		select {
		case r := <-observer:
			state[r.ID] = r
			// Reset timer on activity
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(quietPeriod)
		case <-timer.C:
			return state
		}
	}
}

// liveItems returns only items that are alive in the latest state.
func liveItems(state map[int]ItemReport) []ItemReport {
	var out []ItemReport
	for _, r := range state {
		if r.Alive {
			out = append(out, r)
		}
	}
	return out
}

// dot product of two vec3
func dot(a, b vec3) float64 {
	return a.X*b.X + a.Y*b.Y + a.Z*b.Z
}

// isColinear checks whether all positions lie on the line segment from start to end.
// Returns the max deviation.
func maxColinearDeviation(positions []vec3, start, end vec3) float64 {
	axis := end.sub(start)
	axisLen := axis.length()
	if axisLen < 1e-9 {
		// degenerate segment; all points should equal start
		maxD := 0.0
		for _, p := range positions {
			d := p.sub(start).length()
			if d > maxD {
				maxD = d
			}
		}
		return maxD
	}
	unitAxis := axis.scale(1.0 / axisLen)
	maxDev := 0.0
	for _, p := range positions {
		v := p.sub(start)
		// perpendicular component
		proj := dot(v, unitAxis)
		perp := v.sub(unitAxis.scale(proj))
		d := perp.length()
		if d > maxDev {
			maxDev = d
		}
	}
	return maxDev
}

// neighborSpacings computes the distances between adjacent live items sorted along
// the axis start→end.
func neighborSpacings(positions []vec3, start, end vec3) []float64 {
	if len(positions) < 2 {
		return nil
	}
	axis := end.sub(start)
	axisLen := axis.length()
	if axisLen < 1e-9 {
		return nil
	}
	unit := axis.scale(1.0 / axisLen)

	// project and sort by t
	type proj struct {
		t   float64
		pos vec3
	}
	projs := make([]proj, len(positions))
	for i, p := range positions {
		projs[i] = proj{t: dot(p.sub(start), unit), pos: p}
	}
	// simple sort
	for i := 0; i < len(projs); i++ {
		for j := i + 1; j < len(projs); j++ {
			if projs[j].t < projs[i].t {
				projs[i], projs[j] = projs[j], projs[i]
			}
		}
	}
	spacings := make([]float64, len(projs)-1)
	for i := 0; i < len(projs)-1; i++ {
		spacings[i] = projs[i+1].pos.sub(projs[i].pos).length()
	}
	return spacings
}

// TestBeadChain_StraightSettle verifies basic chain construction and quiescence.
func TestBeadChain_StraightSettle(t *testing.T) {
	t.Parallel()
	start := vec3{0, 0, 0}
	end := vec3{80, 0, 0} // 80 wu → ~20 beads at radius 4
	dist := end.sub(start).length()
	expectedN := int(math.Round(dist / BeadSizeWu))

	observer := make(chan ItemReport, 4096)
	bc := NewBeadChain(start, end, observer)
	defer bc.Stop()

	state := drainAndSettle(observer, 50*time.Millisecond)
	live := liveItems(state)

	// include anchors in count
	if math.Abs(float64(len(live)-expectedN-2)) > 1 {
		t.Errorf("expected ~%d live items (anchors+interior), got %d", expectedN+2, len(live))
	}

	// all items colinear on start→end
	positions := make([]vec3, len(live))
	for i, r := range live {
		positions[i] = r.Pos
	}
	dev := maxColinearDeviation(positions, start, end)
	if dev > relaxEpsilon*10 {
		t.Errorf("max colinear deviation %.4f exceeds tolerance", dev)
	}

	// neighbor spacings in band
	spacings := neighborSpacings(positions, start, end)
	for _, sp := range spacings {
		if sp > upperThreshold+relaxEpsilon*10 || sp < lowerThreshold-relaxEpsilon*10 {
			t.Errorf("spacing %.4f out of band [%.4f, %.4f]", sp, lowerThreshold, upperThreshold)
		}
	}
}


// TestBeadChain_ShortEdge verifies that a sub-BeadSizeWu gap yields 0 interior items
// and doesn't panic.
func TestBeadChain_ShortEdge(t *testing.T) {
	t.Parallel()
	start := vec3{0, 0, 0}
	end := vec3{BeadSizeWu * 0.4, 0, 0} // shorter than one bead → 0 interior

	observer := make(chan ItemReport, 256)
	bc := NewBeadChain(start, end, observer)
	defer bc.Stop()

	state := drainAndSettle(observer, 50*time.Millisecond)
	live := liveItems(state)

	// Only anchors (2 items), no interior
	if len(live) != 2 {
		t.Errorf("expected exactly 2 live items (anchors), got %d", len(live))
	}
}
