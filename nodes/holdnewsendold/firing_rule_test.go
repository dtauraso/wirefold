package holdnewsendold

import (
	"testing"
)

// On receive, emit Held to every ToNext entry, then store the new value.
func TestFireOnReceive(t *testing.T) {
	const latMs = 40.0
	r, _ := newPacedRig(t, latMs, 2)

	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 7, 0) {
		t.Fatal("PlaceAndDriveDeliverOnly returned false")
	}

	got0, ok0 := pollRecv(r, r.observers[0], 20000)
	if !ok0 {
		t.Fatal("timeout waiting for ToNext[0]")
	}
	got1, ok1 := pollRecv(r, r.observers[1], 20000)
	if !ok1 {
		t.Fatal("timeout waiting for ToNext[1]")
	}

	if got0 != 99 {
		t.Errorf("ToNext[0]: expected 99, got %d", got0)
	}
	if got1 != 99 {
		t.Errorf("ToNext[1]: expected 99, got %d", got1)
	}

	r.close()
	if r.node.Held != 7 {
		t.Errorf("Held after fire: expected 7, got %d", r.node.Held)
	}
}
