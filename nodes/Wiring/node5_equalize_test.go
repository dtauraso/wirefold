package Wiring

import (
	"context"
	"math"
	"testing"
	"time"
)

// pollNode5Converged waits until node 5's local-polar-radial distances to peers 2, 7, 8
// (all measured in node 5's own frame) have converged and are pairwise equal, since the
// nodeMover goroutines apply center messages asynchronously.
func pollNode5Converged(t *testing.T, md *MoveDispatch, target vec3) (d5to2, d5to7, d5to8 float64) {
	t.Helper()
	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	for {
		c5, _ := md.centerOfNode("5")
		c2, _ := md.centerOfNode("2")
		c7, _ := md.centerOfNode("7")
		c8, _ := md.centerOfNode("8")
		d5to2 = cart2polar(c2.sub(c5)).R
		d5to7 = cart2polar(c7.sub(c5)).R
		d5to8 = cart2polar(c8.sub(c5)).R
		if math.Abs(c5.X-target.X) <= eps && math.Abs(c5.Y-target.Y) <= eps && math.Abs(c5.Z-target.Z) <= eps &&
			math.Abs(d5to7-d5to2) <= eps && math.Abs(d5to8-d5to2) <= eps {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("did not converge: target=%v c5=%v d5to2=%v d5to7=%v d5to8=%v", target, c5, d5to2, d5to7, d5to8)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestNode5DragEqualizesNeighborDistances verifies the peer-frame local-polar-radial
// equalization scoped to node 5: dragging node 5 sets its double-link distances to
// peers 7 and 8 equal to its double-link distance to peer 2, all measured in node 5's
// own frame. Peer 2 stays put.
func TestNode5DragEqualizesNeighborDistances(t *testing.T) {
	geoms := map[string]nodeGeom{
		"2": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{40, 0, 0}), Outputs: []portGeom{{Name: "out"}}},
		"5": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 0}), Inputs: []portGeom{{Name: "in2"}}, Outputs: []portGeom{{Name: "out7"}, {Name: "out8"}}},
		"7": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{0, 30, 20}), Inputs: []portGeom{{Name: "in"}}},
		"8": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{-25, -10, 15}), Inputs: []portGeom{{Name: "in"}}},
	}
	edges := map[string]EdgeEndpoints{
		"2To5": {Source: "2", Target: "5", SourceHandle: "out", TargetHandle: "in2"},
		"5To7": {Source: "5", Target: "7", SourceHandle: "out7", TargetHandle: "in"},
		"5To8": {Source: "5", Target: "8", SourceHandle: "out8", TargetHandle: "in"},
	}
	md := newMoveDispatch(geoms, edges, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"2": {}, "5": {}, "7": {}, "8": {},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	targets := []vec3{
		{10, 5, -5},
		{-15, 20, 8},
	}

	var lastD5to7, lastD5to2, lastD5to8 float64
	for _, target := range targets {
		if ok := md.RootMove("5", target); !ok {
			t.Fatalf("RootMove(5, %v) = false", target)
		}
		lastD5to2, lastD5to7, lastD5to8 = pollNode5Converged(t, md, target)
	}
	t.Logf("final distances: d5to2=%v d5to7=%v d5to8=%v", lastD5to2, lastD5to7, lastD5to8)
}
