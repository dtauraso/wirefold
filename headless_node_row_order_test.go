// headless_node_row_order_test.go — drives the REAL compiled binary headlessly against
// the real topology/ dir SEVERAL times and asserts the Node block's row→id order (read
// back via each row's Label bytes — the same one-representation label section the buffer
// already carries, no sidecar; topology/nodes/*/meta.json have no data.label so each
// node's label falls back to its id) is:
//
//  1. IDENTICAL across runs, and
//  2. equal to spec order — the directory-sorted node id order LoadTopology reads the
//     topology in (readDirNames + sort.Strings; lexicographic, so "10" sorts before "2").
//
// This is the end-to-end proof that row order is a deterministic PROJECTION OF THE
// DIAGRAM (CLAUDE.md/MODEL.md), not a discovery log built by racing node goroutines to
// their first geometry emit. See headless_clock_test.go for the spawn/fd3/cleanup
// pattern this reuses; NEVER run the sim in the foreground
// (memory/feedback_no_foreground_sim_runs.md).

package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	B "github.com/dtauraso/wirefold/Buffer"
)

// wantNodeRowOrder is the expected spec order for the repo's real topology/nodes dir:
// lexicographic directory-name sort, per nodes/Wiring/loader_tree.go readDirNames +
// sort.Strings. Recomputed from the actual directory listing (not hardcoded) so this
// test does not silently go stale if a node is added/removed from topology/.
func wantNodeRowOrder(t *testing.T, repoRoot string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repoRoot, "topology", "nodes"))
	if err != nil {
		t.Fatalf("ReadDir topology/nodes: %v", err)
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids
}

// nodeRowIDs decodes the Node block's row→id order out of one raw snapshot buffer via
// the Label section (LabelOff/LabelLen columns) — the buffer's row identity is carried
// by row index + this label, never a sidecar.
func nodeRowIDs(snap []byte) []string {
	readU32 := func(off int) uint32 { return binary.LittleEndian.Uint32(snap[off:]) }
	beadCount := int(readU32(4))
	nodeCount := int(readU32(8))
	edgeCount := int(readU32(12))
	portCount := int(readU32(16))
	layoutLinkCount := int(readU32(36))

	nodeOff := B.BufHeaderSize + beadCount*B.BufBeadStride
	labelSecOff := nodeOff +
		nodeCount*B.BufNodeStride +
		nodeCount*B.BufInteriorSlotsPerNode*B.BufInteriorStride +
		edgeCount*B.BufEdgeStride +
		layoutLinkCount*B.BufLayoutLinkStride +
		portCount*B.BufPortStride +
		B.BufCameraStride +
		B.BufOverlayStride +
		B.BufSceneStride +
		B.BufClockStride

	ids := make([]string, nodeCount)
	for i := 0; i < nodeCount; i++ {
		off := int(readU32(nodeOffFieldOff(nodeOff, i, B.BufNodeColLabelOff)))
		ln := int(readU32(nodeOffFieldOff(nodeOff, i, B.BufNodeColLabelLen)))
		ids[i] = string(snap[labelSecOff+off : labelSecOff+off+ln])
	}
	return ids
}

func nodeOffFieldOff(nodeOff, row, col int) int { return nodeOff + row*B.BufNodeStride + col }

func readU32(snap []byte, off int) uint32 { return binary.LittleEndian.Uint32(snap[off:]) }

// runOnceAndReadFirstFrame builds (once, cached by the caller) and runs the real binary
// headlessly against topology/, reads the FIRST fd-3 snapshot frame (which, once row
// seeding lands, already carries every node's row from LoadTopology — before any node
// goroutine's own emit), and returns its decoded node-row id order.
func runOnceAndReadFirstFrame(t *testing.T, binPath, repoRoot string, wantNodeCount int) []string {
	t.Helper()
	runCtx, runCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer runCancel()

	cmd := exec.CommandContext(runCtx, binPath, "-topology", filepath.Join(repoRoot, "topology"))
	cmd.Dir = repoRoot
	cmd.Stderr = os.Stderr

	fd3Read, fd3Write, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	cmd.ExtraFiles = []*os.File{fd3Write}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	fd3Write.Close()

	t.Cleanup(func() {
		runCancel()
		_ = fd3Read.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	br := bufio.NewReader(fd3Read)
	// The very FIRST fd-3 frame is the pre-load Halted trace event (clk.Halt() fires before
	// LoadTopology even runs, let alone the row pre-emit) and so carries nodeCount==0; the
	// pre-emit loop itself then emits ONE frame PER node.NodeGeometry call (Update's
	// KindNodeGeometry case always calls emitSnapshot), so nodeCount climbs 0,1,2,...
	// across several frames before reaching the full set. Skip forward to the first frame
	// that carries ALL of them (bounded, same pattern as headless_clock_test.go's
	// waitForHalted).
	const maxFrames = 200
	for i := 0; i < maxFrames; i++ {
		snap, err := readOneSnapshotFrame(br)
		if err != nil {
			t.Fatalf("frame %d: readOneSnapshotFrame: %v", i, err)
		}
		if int(readU32(snap, 8)) == wantNodeCount { // nodeCount
			return nodeRowIDs(snap)
		}
	}
	t.Fatalf("no frame with %d node rows seen within %d frames", wantNodeCount, maxFrames)
	return nil
}

// TestHeadlessNodeRowOrderIsDeterministic runs the real binary 3+ times against the real
// topology/ dir and asserts the first frame's node-row id order is IDENTICAL every run
// AND equals directory/spec order.
func TestHeadlessNodeRowOrderIsDeterministic(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	binPath := filepath.Join(t.TempDir(), "wirefold-headless-row-order-test")

	buildCtx, buildCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer buildCancel()
	buildCmd := exec.CommandContext(buildCtx, "go", "build", "-o", binPath, ".")
	buildCmd.Dir = repoRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	want := wantNodeRowOrder(t, repoRoot)

	const runs = 5
	var first []string
	for i := 0; i < runs; i++ {
		got := runOnceAndReadFirstFrame(t, binPath, repoRoot, len(want))
		if i == 0 {
			first = got
			if len(got) != len(want) {
				t.Fatalf("run %d: node row count %d != topology/nodes dir count %d", i, len(got), len(want))
			}
			for row := range want {
				if got[row] != want[row] {
					t.Fatalf("run %d row %d: got id %q, want spec-order id %q (full: got=%v want=%v)", i, row, got[row], want[row], got, want)
				}
			}
			continue
		}
		if len(got) != len(first) {
			t.Fatalf("run %d: node row count %d != run 0's %d", i, len(got), len(first))
		}
		for row := range first {
			if got[row] != first[row] {
				t.Fatalf("run %d row %d: got id %q, want %q (run 0's order) — row order is NOT deterministic across runs (full: run0=%v run%d=%v)", i, row, got[row], first[row], first, i, got)
			}
		}
	}
}
