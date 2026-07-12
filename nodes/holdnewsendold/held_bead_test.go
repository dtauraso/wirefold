package holdnewsendold

import (
	"sync"
	"testing"
	"time"

	"github.com/dtauraso/wirefold/nodes/gatecommon"
)

// TestEmitHeldBead asserts the interior held-value bead lifecycle: at startup the
// held value is -1 (the bead is absent), and after node 2 receives its first value
// EmitHeldBead fires with that value. Emission happens only on a held-value change.
func TestEmitHeldBead(t *testing.T) {
	const latMs = 40.0

	var mu sync.Mutex
	var emitted []int

	r, _ := newPacedRigConfig(t, latMs, 1, func(n *Node) {
		n.Held = gatecommon.NoValue
		n.EmitHeldBead = func(held int) {
			mu.Lock()
			emitted = append(emitted, held)
			mu.Unlock()
		}
	})

	waitEmits := func(n int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			mu.Lock()
			got := len(emitted)
			mu.Unlock()
			if got >= n {
				return
			}
			if time.Now().After(deadline) {
				r.close()
				t.Fatalf("only %d interior emits after wait, want %d", got, n)
			}
			r.clk.AdvanceTicks(1)
			time.Sleep(200 * time.Microsecond)
		}
	}

	// Startup emit (held == -1) lands before any input.
	waitEmits(1)
	mu.Lock()
	if emitted[0] != gatecommon.NoValue {
		mu.Unlock()
		r.close()
		t.Fatalf("startup emit: got %v, want [-1] (present=false)", emitted)
	}
	mu.Unlock()

	// First received value 0 → held changes -1→0, interior emit fires with 0.
	// ToNext forwards the PRIOR Held (-1), which is suppressed → no output bead.
	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 0, 0) {
		t.Fatal("PlaceAndDriveDeliverOnly returned false")
	}
	waitEmits(2)
	if v, ok := pollRecv(r, r.observer, 30); ok {
		t.Fatalf("first fire emitted %d on ToNext; expected nothing (Held was -1)", v)
	}

	// Same value 0 again → interior held unchanged; ToNext forwards Held 0.
	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 0, 0) {
		t.Fatal("second PlaceAndDriveDeliverOnly returned false")
	}
	if _, ok := pollRecv(r, r.observer, 20000); !ok {
		t.Fatal("timeout waiting for ToNext (Held=0)")
	}

	// New value 1 → interior emit fires with 1; ToNext forwards Held 0.
	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 1, 0) {
		t.Fatal("third PlaceAndDriveDeliverOnly returned false")
	}
	if _, ok := pollRecv(r, r.observer, 20000); !ok {
		t.Fatal("timeout waiting for ToNext (Held=0, forwarding prior)")
	}

	// Wait for all three interior emits (-1, 0, 1) to land before tearing down.
	waitEmits(3)
	r.close()

	mu.Lock()
	defer mu.Unlock()
	want := []int{gatecommon.NoValue, 0, 1}
	if len(emitted) != len(want) {
		t.Fatalf("emitted = %v, want %v", emitted, want)
	}
	for i, w := range want {
		if emitted[i] != w {
			t.Fatalf("emitted = %v, want %v", emitted, want)
		}
	}
}

// TestNoSentinelOnToNext asserts the output invariant: starting Held=-1, the
// first fire emits NOTHING on ToNext (the -1 sentinel is suppressed), and once
// Held has become a real value (0/1) a subsequent fire DOES emit on ToNext.
func TestNoSentinelOnToNext(t *testing.T) {
	const latMs = 40.0
	r, _ := newPacedRigConfig(t, latMs, 1, func(n *Node) {
		n.Held = gatecommon.NoValue
	})
	defer r.close()

	// First recv: Held is -1, so ToNext must emit NOTHING.
	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 0, 0) {
		t.Fatal("PlaceAndDriveDeliverOnly returned false")
	}
	if v, ok := pollRecv(r, r.observer, 30); ok {
		t.Fatalf("first fire emitted %d on ToNext; expected nothing (Held was -1)", v)
	}

	// Held is now 0. Next recv forwards the real held value 0.
	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 1, 0) {
		t.Fatal("second PlaceAndDriveDeliverOnly returned false")
	}
	got, ok := pollRecv(r, r.observer, 20000)
	if !ok {
		t.Fatal("timeout waiting for ToNext")
	}
	if got != 0 {
		t.Fatalf("second fire ToNext: got %d, want 0", got)
	}
}
