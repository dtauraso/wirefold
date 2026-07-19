package Wiring

// time_node_abc_drag_breadcrumb_test.go — proves the "time.abc-drag" breadcrumb added
// to neighborSetCRequantize (node_move.go) is both OBSERVABLE (the debug sink captures a
// real, well-formed line for the HoldNewSendOld neighbor) and CORRECTLY GATED (a non-time
// neighbor of the same drag emits nothing). Two failure modes this guards against:
//   - if the `md.NodeKind(selfID) == "HoldNewSendOld"` gate were removed/widened, the
//     non-time neighbor "n" would ALSO emit a breadcrumb and assertion (b) below would see
//     a "time.abc-drag" line keyed to "n" — it currently sees none, so removing the gate
//     flips this test to fail.
//   - if the Breadcrumb call itself were deleted, the time neighbor "t" would receive its
//     moveMsgKindNeighborSetC exactly as before (behavior is unchanged — this is
//     observability only) but the debug sink would capture zero "time.abc-drag" lines,
//     so assertion (a) below (which requires exactly one) would fail.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer wraps a bytes.Buffer with a mutex — Trace.Breadcrumb's debugSink write is
// itself mutex-guarded inside Trace (t.mu), but that lock is private to the Trace value;
// this wrapper gives the TEST its own safe read path once the drag has quiesced (we only
// read after pollDragConverged + a settle sleep, never concurrently with a write, but the
// wrapper costs nothing and removes any doubt).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// writeXTN lays down a 3-node topology: "x" (FanInSrc, the node that will be dragged),
// "t" (a real HoldNewSendOld node — a "time node") and "n" (FanInSink, a plain non-time
// node) — both t and n are direct neighbors of x via one edge each, so dragging x sends
// BOTH a moveMsgKindNeighborSetC. t is the positive case (breadcrumb expected), n is the
// negative control (no breadcrumb expected).
func writeXTN(t *testing.T) string {
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
	mk("nodes/x/meta.json", `{"id":"x","type":"FanInSrc","r":100,"scenePolarR":40,"scenePolarTheta":1.0,"scenePolarPhi":1.2}`)
	mk("nodes/x/outputs/Out.json", `{"name":"Out"}`)
	mk("nodes/t/meta.json", `{"id":"t","type":"HoldNewSendOld","data":{"state":{"held":0}},"r":100,"scenePolarR":90,"scenePolarTheta":0.9,"scenePolarPhi":-2.1}`)
	mk("nodes/t/inputs/FromPrevHoldNewSendOldNode.json", `{"name":"FromPrevHoldNewSendOldNode"}`)
	mk("nodes/n/meta.json", `{"id":"n","type":"FanInSink","r":100,"scenePolarR":90,"scenePolarTheta":2.0,"scenePolarPhi":0.4}`)
	mk("nodes/n/inputs/In.json", `{"name":"In"}`)
	mk("edges/eXT.json", `{"label":"eXT","kind":"chain","source":"x","sourceHandle":"Out","target":"t","targetHandle":"FromPrevHoldNewSendOldNode"}`)
	mk("edges/eXN.json", `{"label":"eXN","kind":"data","source":"x","sourceHandle":"Out","target":"n","targetHandle":"In"}`)
	return root
}

// breadcrumbLine is the decoded shape of one {"kind":"breadcrumb",...} JSONL line.
type breadcrumbLine struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Node  string `json:"node"`
	Port  string `json:"port"`
	Value string `json:"value"`
}

func parseBreadcrumbLines(t *testing.T, raw string) []breadcrumbLine {
	t.Helper()
	var out []breadcrumbLine
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if line == "" {
			continue
		}
		var b breadcrumbLine
		if err := json.Unmarshal([]byte(line), &b); err != nil {
			t.Fatalf("breadcrumb line is not valid JSON: %v (%q)", err, line)
		}
		out = append(out, b)
	}
	return out
}

// TestTimeNodeLogsAbcDragBreadcrumbGatedByKind drags x and asserts (a) the time neighbor
// t emits exactly one time.abc-drag breadcrumb matching its freshly re-quantized abc, and
// (b) the non-time neighbor n emits none.
func TestTimeNodeLogsAbcDragBreadcrumbGatedByKind(t *testing.T) {
	root := writeXTN(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)

	var dbg syncBuffer
	md.tr.SetDebugSink(&dbg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	lhT, ok := md.layoutHolders["t"]
	if !ok {
		t.Fatal("no LayoutHolder for t")
	}

	xBefore, ok := md.centerOfNode("x")
	if !ok {
		t.Fatal("no center for x")
	}
	target := xBefore.add(vec3{X: 55, Y: -20, Z: 30})
	if !md.RootMove("x", target) {
		t.Fatal("RootMove(x) returned false")
	}
	pollDragConverged(t, md, "x", target)
	// Let the neighborSetC messages to both t and n (and t's requantize + breadcrumb)
	// settle before reading the debug sink.
	deadline := time.Now().Add(2 * time.Second)
	var lpAfter LocalPolar
	for {
		for _, lp := range lhT.LocalPolarsSnapshot() {
			if lp.To == "x" {
				lpAfter = lp
			}
		}
		if lpAfter.To == "x" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("t's local polar to x never appeared")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)

	lines := parseBreadcrumbLines(t, dbg.String())

	var tLines, nLines []breadcrumbLine
	for _, b := range lines {
		if b.Label != "time.abc-drag" {
			continue
		}
		switch b.Node {
		case "t":
			tLines = append(tLines, b)
		case "n":
			nLines = append(nLines, b)
		}
	}

	// (b) NEGATIVE CONTROL: n is not a time node (FanInSink), so its neighborSetC
	// handling must emit zero time.abc-drag breadcrumbs. If the `NodeKind == "HoldNewSendOld"`
	// gate in neighborSetCRequantize were removed (or widened to match any kind), n would
	// receive its own neighborSetC from x exactly like t does and would emit one here too —
	// this assertion would then fail, proving the gate is load-bearing.
	if len(nLines) != 0 {
		t.Fatalf("(b) non-time neighbor n must never emit time.abc-drag; got %+v", nLines)
	}

	// (a) POSITIVE CASE: t (HoldNewSendOld) must emit exactly one time.abc-drag
	// breadcrumb for this drag, keyed node=t, port=x (the peer), whose value carries the
	// peer id and an abc triple matching t's freshly re-quantized LocalPolar to x. If the
	// Breadcrumb call were deleted from neighborSetCRequantize entirely, tLines would be
	// empty and this assertion would fail.
	if len(tLines) != 1 {
		t.Fatalf("(a) expected exactly one time.abc-drag breadcrumb for t; got %d: %+v", len(tLines), tLines)
	}
	b := tLines[0]
	if b.Kind != "breadcrumb" {
		t.Fatalf("(a) expected kind=breadcrumb, got %q", b.Kind)
	}
	if b.Node != "t" || b.Port != "x" {
		t.Fatalf("(a) expected node=t port=x, got node=%q port=%q", b.Node, b.Port)
	}
	if !strings.Contains(b.Value, "peer=x") {
		t.Fatalf("(a) value must name the peer: %q", b.Value)
	}
	wantAbc := fmt.Sprintf("abc=(%d,%d,%d)", lpAfter.QuantITheta, lpAfter.QuantIPhi, lpAfter.QuantIR)
	if !strings.Contains(b.Value, wantAbc) {
		t.Fatalf("(a) value abc triple must match t's freshly re-quantized LocalPolar to x: want substring %q, got %q", wantAbc, b.Value)
	}

	md.quantOffsetPersist.flush()
}
