package Wiring

import (
	"testing"
	"time"
)

// gesture_author_test.go — drive the keyboard-authoring channel (gesture.go's
// AuthorBegin/AuthorLatchHalfTerm/AuthorNode/AuthorPort/AuthorTorus/SetHover*ByRow) and assert
// it commits the SAME polarEq shapes the click path (trySelectSphereRule) commits, since a
// typed token is meant to be the resolved equivalent of a click.

// mapNodeRows is a trivial NodeRowResolver test double: row i -> its id in the slice.
type mapNodeRows []string

func (m mapNodeRows) LookupNodeRow(row int) (string, bool) {
	if row < 0 || row >= len(m) {
		return "", false
	}
	return m[row], true
}

// armedLocksPersist returns a polarEqsPersister that actually records `schedule` calls
// (path != "") without ever flushing to disk during the test (debounce set far in the
// future).
func armedLocksPersist() *polarEqsPersister {
	return &polarEqsPersister{path: "unused-test-path", debounce: time.Hour}
}

func TestAuthorNodeNodeEquationCommits(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	md.SetNodeRowResolver(mapNodeRows{"Center1", "A", "B"})
	md.locksPersist = armedLocksPersist()

	md.AuthorBegin(eqNodeNode, nil)
	md.AuthorNode(0, nil) // row 0 -> Center1 (no pending half-term -> latches as Center)
	md.AuthorLatchHalfTerm(compTheta, 1, nil)
	if !md.gest.hasPending {
		t.Fatalf("hasPending=false after AuthorLatchHalfTerm, want true (pending half-term observable)")
	}
	md.AuthorNode(1, nil) // row 1 -> A, completes the pending half-term
	if len(md.polarEqsSnap()) != 0 {
		t.Fatalf("polarEqs=%v after ONE authored term, want none yet", md.polarEqsSnap())
	}
	md.AuthorLatchHalfTerm(compTheta, -1, nil)
	md.AuthorNode(2, nil) // row 2 -> B, completes the 2nd term and commits

	eqs := md.polarEqsSnap()
	if len(eqs) != 1 {
		t.Fatalf("polarEqs=%v, want exactly 1", eqs)
	}
	want := polarEq{
		Center: "Center1",
		A:      polarTerm{Node: "A", Comp: compTheta, Sign: 1},
		B:      polarTerm{Node: "B", Comp: compTheta, Sign: -1},
		Active: true,
	}
	if eqs[0] != want {
		t.Fatalf("polarEqs[0]=%+v want %+v", eqs[0], want)
	}
	if want := []int{0}; !slicesEqualInt(md.selectedLocks, want) {
		t.Fatalf("selectedLocks=%v after authored commit, want %v", md.selectedLocks, want)
	}
	md.locksPersist.mu.Lock()
	has, pending := md.locksPersist.has, len(md.locksPersist.pending)
	md.locksPersist.mu.Unlock()
	if !has || pending != 1 {
		t.Fatalf("locksPersist not scheduled: has=%v pending=%d, want has=true pending=1", has, pending)
	}
}

// The torus is ALWAYS the port's own node (the sticky Center — see MODEL.md), never a free
// second-node choice: authoring is a ONE-STEP commit on the Center's own port.
func TestAuthorPortTorusEquationCommits(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	md.SetNodeRowResolver(mapNodeRows{"N1", "N2"})
	md.locksPersist = armedLocksPersist()

	md.AuthorBegin(eqPortTorus, nil)
	md.AuthorNode(0, nil) // row 0 -> N1, latches as the sticky Center (no pending half-term)
	md.AuthorPort(0, "out", false, nil)

	eqs := md.polarEqsSnap()
	if len(eqs) != 1 {
		t.Fatalf("polarEqs=%v, want exactly 1", eqs)
	}
	want := polarEq{Kind: eqPortTorus, PortNode: "N1", PortName: "out", PortIsInput: false, TorusNode: "N1", Active: true}
	if eqs[0] != want {
		t.Fatalf("polarEqs[0]=%+v want %+v", eqs[0], want)
	}
}

// A port that does NOT belong to the sticky Center must be rejected — a cross-node
// `port ∈ torus` lock can never be authored (TorusNode is always forced == PortNode == the
// Center, so an off-Center port has no valid torus to attach to).
func TestAuthorPortOffCenterRejected(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	md.SetNodeRowResolver(mapNodeRows{"N1", "N2"})

	md.AuthorBegin(eqPortTorus, nil)
	md.AuthorNode(0, nil)               // Center = N1
	md.AuthorPort(1, "out", false, nil) // row 1 -> N2, NOT the Center

	if eqs := md.polarEqsSnap(); len(eqs) != 0 {
		t.Fatalf("polarEqs=%v after off-Center AuthorPort, want none committed", eqs)
	}
}

func TestAuthorPreviewSetsHoverPortAndNode(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	md.SetNodeRowResolver(mapNodeRows{"N1", "N2"})

	md.SetHoverPortByRow(0, "out", false, nil)
	if md.hoverNode != "N1" || md.hoverPort != "out" || md.hoverInput != false {
		t.Fatalf("hover=(%q,%q,%v) after preview port, want (N1,out,false)", md.hoverNode, md.hoverPort, md.hoverInput)
	}

	md.SetHoverNodeByRow(1, nil)
	if md.hoverNode != "N2" || md.hoverPort != "" {
		t.Fatalf("hover=(%q,%q) after preview node, want (N2,\"\")", md.hoverNode, md.hoverPort)
	}
}

// The click path (trySelectSphereRule) is unchanged by the commitPolarEq refactor: driving it
// through raw pointer events still produces the exact same committed equation.
func TestClickPathUnchangedByCommitRefactor(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	md.ov.selSpherePolesVisible = true
	md.ruleCenter = "Center1"

	click := func(hit rawHit) {
		down := rawEvent("pointerdown", 400, 300)
		down.Hit = hit
		md.HandleRawInput(down, nil, nil)
		up := rawEvent("pointerup", 401, 300)
		up.Hit = hit
		md.HandleRawInput(up, nil, nil)
	}
	click(rawHit{Kind: "handhold", HandholdTerm: 0})
	click(rawHit{Kind: "node", Id: "A"})
	click(rawHit{Kind: "handhold", HandholdTerm: 2})
	click(rawHit{Kind: "node", Id: "B"})

	eqs := md.polarEqsSnap()
	if len(eqs) != 1 {
		t.Fatalf("polarEqs=%v, want exactly 1", eqs)
	}
	want := polarEq{
		Center: "Center1",
		A:      polarTerm{Node: "A", Comp: compTheta, Sign: 1},
		B:      polarTerm{Node: "B", Comp: compTheta, Sign: -1},
		Active: true,
	}
	if eqs[0] != want {
		t.Fatalf("polarEqs[0]=%+v want %+v", eqs[0], want)
	}
}
