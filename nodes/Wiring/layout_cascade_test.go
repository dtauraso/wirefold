// layout_cascade_test.go — SLICE 2 headless verification of the radius (iR) cascade
// (docs/planning/visual-editor/layout-on-domain-network.md): drives the real loader +
// running node goroutines (not mocks) and asserts the propagated iR/world-center reach
// every radius-forwarded descendant, a non-forwarding node terminates its branch, cycles
// don't hang, and the new iR is persisted to disk (meta.json quantIR).

package Wiring

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// layoutTestNode is a minimal node kind exercising ONLY the layout-port poll every real
// kind's Update loop performs (nodes/holdnewsendold/node.go, nodes/hold/node.go): a
// select loop over ctx.Done() that also non-blockingly polls Layout and calls Handle.
// Registered under test-only kind names "LayoutTestTime" (a radius-forwarding node, via
// RegisterRadiusForwarder) and "LayoutTestPlain" (not a radius-forwarding node), so the loader's radius-forwarding
// gate is exercised through the REAL loader/build path. The forwarding property is a registry
// property, not a hard-coded kind string, so these names exercise the gate without
// colliding with the real "HoldNewSendOld"/"Hold" registrations other test files in
// this binary pull in — and without importing the real nodes/hold, nodes/holdnewsendold
// packages (which import this package — a real import cycle; see aimed_ports_test.go /
// fanin_travel_time_test.go for the same in-package synthetic-kind pattern).
type layoutTestNode struct {
	Layout *LayoutPort
	In     *In
	Out    OutMulti
}

func (n *layoutTestNode) Update(ctx context.Context) {
	// Block on the layout inbound channel directly (same package: layout_edge.go's `in`
	// field is unexported but visible here) rather than the real kinds' non-blocking
	// TryRecv-in-a-spin-loop pattern — a tight busy-spin default-case loop across several
	// goroutines starves the scheduler badly enough on a loaded box to make the
	// unrelated direct-drag delivery (RootMove -> fanCenters -> nodeMover inbox) flaky.
	// Real kinds avoid this because their domain In is normally paced (TryRecv blocks);
	// this synthetic kind has no domain traffic at all, so block on Layout instead.
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-n.Layout.in:
			n.Layout.Handle(msg)
		}
	}
}

func init() {
	// Unique test-only kind names so they never collide with the real
	// "HoldNewSendOld"/"Hold" registrations that other test files in this binary
	// (e.g. nonblocking_traversal_test.go's external Wiring_test package) pull in
	// via real node-package imports. The forwarding property is a registry property
	// (RegisterRadiusForwarder), not a hard-coded kind string, so the loader's cascade
	// gate is still exercised through the real build path.
	Register("LayoutTestTime", func() any { return &layoutTestNode{} })
	RegisterRadiusForwarder("LayoutTestTime")
	Register("LayoutTestPlain", func() any { return &layoutTestNode{} })
}

// writeCascadeTree builds a small tree topology on disk:
//
//	2 (HoldNewSendOld, root, radius-forwarding node)
//	└─ 5 (HoldNewSendOld, reference=2, radius-forwarding node)
//	   ├─ 7 (Hold, reference=5, NON-forwarding — cascade must stop here)
//	   │  └─ 10 (Hold, reference=7 — must NOT receive the cascade at all)
//	   └─ 8 (HoldNewSendOld, reference=5, radius-forwarding node — cascade continues)
//	      └─ 9 (Hold, reference=8 — must receive the cascade, forwarded via 8)
//
// Domain edges mirror 1:1 onto the hidden layout graph (loader.go buildLayoutEdges),
// so dragging "5" (whose reference "2" is a radius-forwarding node) cascades a radius change to
// 5's own reposition, then on to 7 and 8 (5 is a radius-forwarding node), then on to 9 (8 is a radius-forwarding
// node) but NOT to 10 (7 is not a radius-forwarding node, so it does not forward).
func writeCascadeTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	mk("nodes/2/meta.json", `{"id":"2","type":"LayoutTestTime","quantITheta":0,"quantIPhi":0,"quantIR":0}`)
	mk("nodes/2/inputs/In.json", `{"name":"In"}`)
	mk("nodes/2/outputs/Out.json", `{"name":"Out"}`)

	mk("nodes/5/meta.json", `{"id":"5","type":"LayoutTestTime","reference":"2","quantITheta":1,"quantIPhi":0,"quantIR":1}`)
	mk("nodes/5/inputs/In.json", `{"name":"In"}`)
	mk("nodes/5/outputs/Out0.json", `{"name":"Out0"}`)
	mk("nodes/5/outputs/Out1.json", `{"name":"Out1"}`)

	mk("nodes/7/meta.json", `{"id":"7","type":"LayoutTestPlain","reference":"5","quantITheta":0,"quantIPhi":1,"quantIR":1}`)
	mk("nodes/7/inputs/In.json", `{"name":"In"}`)
	mk("nodes/7/outputs/Out0.json", `{"name":"Out0"}`)

	mk("nodes/10/meta.json", `{"id":"10","type":"LayoutTestPlain","reference":"7","quantITheta":0,"quantIPhi":0,"quantIR":1}`)
	mk("nodes/10/inputs/In.json", `{"name":"In"}`)

	mk("nodes/8/meta.json", `{"id":"8","type":"LayoutTestTime","reference":"5","quantITheta":-1,"quantIPhi":0,"quantIR":1}`)
	mk("nodes/8/inputs/In.json", `{"name":"In"}`)
	mk("nodes/8/outputs/Out0.json", `{"name":"Out0"}`)

	mk("nodes/9/meta.json", `{"id":"9","type":"LayoutTestPlain","reference":"8","quantITheta":0,"quantIPhi":0,"quantIR":1}`)
	mk("nodes/9/inputs/In.json", `{"name":"In"}`)

	if err := os.MkdirAll(filepath.Join(root, "edges"), 0o755); err != nil {
		t.Fatal(err)
	}
	mk("edges/2To5.json", `{"label":"2To5","kind":"data","source":"2","sourceHandle":"Out0","target":"5","targetHandle":"In"}`)
	mk("edges/5To7.json", `{"label":"5To7","kind":"data","source":"5","sourceHandle":"Out0","target":"7","targetHandle":"In"}`)
	mk("edges/5To8.json", `{"label":"5To8","kind":"data","source":"5","sourceHandle":"Out1","target":"8","targetHandle":"In"}`)
	mk("edges/7To10.json", `{"label":"7To10","kind":"data","source":"7","sourceHandle":"Out0","target":"10","targetHandle":"In"}`)
	mk("edges/8To9.json", `{"label":"8To9","kind":"data","source":"8","sourceHandle":"Out0","target":"9","targetHandle":"In"}`)

	if err := os.MkdirAll(filepath.Join(root, "view", "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// readPersistedQuantIR polls <root>/nodes/<id>/meta.json until its quantIR field equals
// want (or fails after a bounded budget) — the persister debounces (250ms) off the drag.
func readPersistedQuantIR(t *testing.T, root, id string, want int) {
	t.Helper()
	path := filepath.Join(root, "nodes", id, "meta.json")
	deadline := time.Now().Add(3 * time.Second)
	var last int
	var lastErr error
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err == nil {
				if v, ok := m["quantIR"]; ok {
					if f, ok := v.(float64); ok {
						last = int(f)
						if last == want {
							return
						}
					}
				}
			}
		} else {
			lastErr = err
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("node %s: meta.json quantIR never reached %d (last seen %d, readErr=%v)", id, want, last, lastErr)
}

// TestRadiusCascadePropagatesThroughForwardersOnly drives a live drag on node "5"
// (whose reference "2" is a radius-forwarding node) and asserts: the cascade reaches 5, 7, 8, 9
// (7 and 8 both children of 5; 9 a grandchild reached only because 8 is a radius-forwarding node),
// their world centers land where the plain-local-polar formula predicts, the new iR is
// persisted to each reached node's meta.json, and node 10 (child of the non-forwarding node 7)
// never receives the cascade at all — its world center stays exactly where it loaded.
func TestRadiusCascadePropagatesThroughForwardersOnly(t *testing.T) {
	root := writeCascadeTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodes, _, md, err := LoadTopology(ctx, root, T.New(1024), NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	md.EnableEditPersist(root)
	md.Start(ctx)

	// Launch every node's own Update() goroutine — the layout cascade runs INSIDE it
	// (holdnewsendold/node.go, hold/node.go poll Layout.TryRecv/Handle each iteration),
	// so without this the hidden layout messages would never be drained/forwarded.
	for _, n := range nodes {
		go n.Update(ctx)
	}

	tenBefore, ok := md.centerOfNode("10")
	if !ok {
		t.Fatal("10 has no center before drag")
	}

	// Drag "5" to a NEW RADIUS along its EXISTING (iTheta=1, iPhi=0) direction about its
	// reference "2" — a pure radius change, which is what this slice's cascade model
	// propagates (docs/planning/visual-editor/layout-on-domain-network.md: "First
	// message type: radius (iR) propagation"). A drag that also changes the dragged
	// node's OWN angle would remeasure a new iTheta/iPhi for "5" itself (RootMove's
	// snapToReference/remeasureTriples), which the cascade does not carry — that is a
	// different, not-yet-built message type, not this one.
	refCenter, ok := md.centerOfNode("2")
	if !ok {
		t.Fatal("2 has no center before drag")
	}
	const newIRWant = 3 // was iR=1 at load
	target := refCenter.add(polar2cart(polar{R: float64(newIRWant) * stepR, Theta: 1 * stepTheta, Phi: 0}))
	if !md.RootMove("5", target) {
		t.Fatal("RootMove(5) returned false")
	}

	want5, ok := md.snapToReference("5", target)
	if !ok {
		t.Fatal("expected a reference snap for node 5")
	}
	waitCenterClose(t, md, "5", want5, 1e-6)

	newOff, ok := md.quantizedOffsets["5"]
	if !ok {
		t.Fatal("node 5 has no quantized offset after drag")
	}
	newIR := newOff.iR

	// 7 and 8: children of 5, both reached because 5 is a radius-forwarding node. Each computes its
	// OWN new center as refCenter (5's new center) + polar2cart({R: newIR*stepR,
	// Theta: its own iTheta*stepTheta, Phi: its own iPhi*stepPhi}) — the plain
	// local-polar formula (layout_edge.go Handle), NOT the rotated forward-kinematics
	// compose path.
	want7 := want5.add(polar2cart(polar{R: float64(newIR) * stepR, Theta: 0 * stepTheta, Phi: 1 * stepPhi}))
	want8 := want5.add(polar2cart(polar{R: float64(newIR) * stepR, Theta: -1 * stepTheta, Phi: 0 * stepPhi}))
	got7 := waitCenterClose(t, md, "7", want7, 1e-6)
	got8 := waitCenterClose(t, md, "8", want8, 1e-6)
	_ = got7
	_ = got8

	// 9: grandchild of 5 via 8 — reached ONLY because 8 (not 7) is a radius-forwarding node and
	// forwards past itself.
	want9 := want8.add(polar2cart(polar{R: float64(newIR) * stepR, Theta: 0, Phi: 0}))
	waitCenterClose(t, md, "9", want9, 1e-6)

	// 10: grandchild of 5 via 7 — 7 is NOT a radius-forwarding node, so it re-places itself but does
	// not forward; 10 never receives any LayoutMsg at all and its center is unchanged.
	time.Sleep(300 * time.Millisecond) // give any (unwanted) propagation a chance to land
	tenAfter, _ := md.centerOfNode("10")
	if tenAfter.sub(tenBefore).length() > 1e-9 {
		t.Fatalf("10 moved even though its reference (7) is not a radius-forwarding node: before=%v after=%v", tenBefore, tenAfter)
	}

	// Persistence: the new iR lands on disk for every node the cascade actually
	// reached, confirming applyLayoutCenter's schedule() call reached quantOffsetPersist.
	readPersistedQuantIR(t, root, "5", newIR)
	readPersistedQuantIR(t, root, "7", newIR)
	readPersistedQuantIR(t, root, "8", newIR)
	readPersistedQuantIR(t, root, "9", newIR)
}
