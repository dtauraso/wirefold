package Wiring

// time_node_abc_drag_breadcrumb_test.go — proves the "abc-drag" breadcrumb added to
// neighborSetCRequantize (node_move.go) fires for EVERY direct neighbor that receives a
// moveMsgKindNeighborSetC when a node is dragged — not just HoldNewSendOld ("time") nodes.
// This guards against:
//   - if the abc-drag Breadcrumb/AbcDrag call were re-gated to a specific NodeKind (e.g.
//     `md.NodeKind(selfID) == "HoldNewSendOld"`), the non-time neighbor "n" would emit
//     nothing and assertion (b) below (which requires exactly one line for n) would fail.
//   - if the Breadcrumb call itself were deleted, both "t" and "n" would receive their
//     moveMsgKindNeighborSetC exactly as before (behavior is unchanged — this is
//     observability only) but the debug sink would capture zero "abc-drag" lines, so
//     assertions (a) and (b) below would both fail.

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
// BOTH a moveMsgKindNeighborSetC. Both are positive cases now: every drag-message
// recipient must log an abc-drag breadcrumb, regardless of kind.
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

// TestEveryDragRecipientLogsAbcDragBreadcrumb drags x and asserts that EVERY direct
// neighbor that receives a moveMsgKindNeighborSetC — both the time node t (HoldNewSendOld)
// and the non-time node n (FanInSink) — emits exactly one "abc-drag" breadcrumb matching
// its freshly re-quantized abc, keyed node=<recipient> port=x.
func TestEveryDragRecipientLogsAbcDragBreadcrumb(t *testing.T) {
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
	lhN, ok := md.layoutHolders["n"]
	if !ok {
		t.Fatal("no LayoutHolder for n")
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
	// Let the neighborSetC messages to both t and n (and their requantize + breadcrumb)
	// settle before reading the debug sink.
	deadline := time.Now().Add(2 * time.Second)
	var lpTAfter, lpNAfter LocalPolar
	for {
		for _, lp := range lhT.LocalPolarsSnapshot() {
			if lp.To == "x" {
				lpTAfter = lp
			}
		}
		for _, lp := range lhN.LocalPolarsSnapshot() {
			if lp.To == "x" {
				lpNAfter = lp
			}
		}
		if lpTAfter.To == "x" && lpNAfter.To == "x" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("t's and/or n's local polar to x never appeared")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)

	lines := parseBreadcrumbLines(t, dbg.String())

	var tLines, nLines []breadcrumbLine
	for _, b := range lines {
		if b.Label != "abc-drag" {
			continue
		}
		switch b.Node {
		case "t":
			tLines = append(tLines, b)
		case "n":
			nLines = append(nLines, b)
		}
	}

	checkOne := func(who string, got []breadcrumbLine, lp LocalPolar) {
		t.Helper()
		// If the abc-drag Breadcrumb/AbcDrag call were re-gated to a specific NodeKind (or
		// deleted outright), one of the two recipients would emit zero lines here and this
		// assertion would fail.
		if len(got) != 1 {
			t.Fatalf("expected exactly one abc-drag breadcrumb for %s; got %d: %+v", who, len(got), got)
		}
		b := got[0]
		if b.Kind != "breadcrumb" {
			t.Fatalf("%s: expected kind=breadcrumb, got %q", who, b.Kind)
		}
		if b.Node != who || b.Port != "x" {
			t.Fatalf("%s: expected node=%s port=x, got node=%q port=%q", who, who, b.Node, b.Port)
		}
		if !strings.Contains(b.Value, "peer=x") {
			t.Fatalf("%s: value must name the peer: %q", who, b.Value)
		}
		wantAbc := fmt.Sprintf("abc=(%d,%d,%d)", lp.QuantITheta, lp.QuantIPhi, lp.QuantIR)
		if !strings.Contains(b.Value, wantAbc) {
			t.Fatalf("%s: value abc triple must match freshly re-quantized LocalPolar to x: want substring %q, got %q", who, wantAbc, b.Value)
		}
	}

	// (a) t (HoldNewSendOld, a "time node") must emit its abc-drag breadcrumb.
	checkOne("t", tLines, lpTAfter)
	// (b) n (FanInSink, NOT a time node) must ALSO emit its abc-drag breadcrumb — the old
	// kind gate is gone; every drag-message recipient is logged.
	checkOne("n", nLines, lpNAfter)

	md.quantOffsetPersist.flush()
}
