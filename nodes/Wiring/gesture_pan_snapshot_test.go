package Wiring

import "testing"

func TestUnseededViewpointPanIsDegenerate(t *testing.T) {
	// Zero viewpoint: pos = up = {Theta:0, Phi:0} → both map to +Y → parallel.
	md := newGestureMD(viewpoint{})
	ev := rawEvent("wheel", 400, 300)
	ev.DeltaX = 40
	md.HandleRawInput(ev, nil, nil)

	posW := anglesToWorldOffset(1, md.vp.pos.Theta, md.vp.pos.Phi)
	upW := anglesToWorldOffset(1, md.vp.up.Theta, md.vp.up.Phi)
	// Degenerate: |pos × up| ≈ 0 (parallel), the collapsed-basis condition.
	if cross := posW.cross(upW).length(); cross > 1e-9 {
		t.Fatalf("expected degenerate (parallel pos/up) from zero viewpoint, |pos×up|=%v", cross)
	}

	// A seeded viewpoint keeps a valid (non-degenerate) basis after the same pan.
	md2 := newGestureMD(canonicalViewpoint())
	md2.HandleRawInput(ev, nil, nil)
	posW2 := anglesToWorldOffset(1, md2.vp.pos.Theta, md2.vp.pos.Phi)
	upW2 := anglesToWorldOffset(1, md2.vp.up.Theta, md2.vp.up.Phi)
	if cross := posW2.cross(upW2).length(); cross < 1e-6 {
		t.Fatalf("seeded viewpoint should keep a valid basis, but |pos×up|=%v", cross)
	}
}
