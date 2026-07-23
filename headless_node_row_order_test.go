// headless_node_row_order_test.go — drives the REAL compiled binary headlessly against
// the real topology/ dir SEVERAL times and asserts the Node block's row→id order (read back
// via each row's own inline Label bytes on its dedicated NODE stream frame — see
// Buffer.BuildNodeStreamFrame; topology/nodes/*/meta.json have no data.label so each node's
// label falls back to its id) is:
//
//  1. IDENTICAL across runs, and
//  2. equal to spec order — the directory-sorted node id order LoadTopology reads the
//     topology in (readDirNames + sort.Strings; lexicographic, so "10" sorts before "2").
//
// This is the end-to-end proof that row order is a deterministic PROJECTION OF THE
// DIAGRAM (CLAUDE.md/MODEL.md), not a discovery log built by racing node goroutines to
// their first geometry emit. See headless_stream_helpers_test.go for the spawn/cleanup
// pattern this reuses; NEVER run the sim in the foreground
// (memory/feedback_no_foreground_sim_runs.md).

package main

import (
	"os"
	"testing"

	B "github.com/dtauraso/wirefold/Buffer"
)

// nodeStreamRowID decodes ONE dedicated NODE-stream frame's own inline Label bytes (see
// Buffer.BuildNodeStreamFrame's header: [tick,portCount,labelLen,portNameBytesCount,
// layoutLinkCount] = 5×u32, then the Node row, then this frame's own label bytes inline).
func nodeStreamRowID(frame []byte) string {
	const hdrSize = 20
	labelLen := int(readU32(frame, 8))
	labelOff := hdrSize + B.BufNodeStride
	return string(frame[labelOff : labelOff+labelLen])
}

// TestHeadlessNodeRowOrderIsDeterministic runs the real binary 3+ times against the real
// topology/ dir and asserts the node-row id order (each node row's own dedicated NODE-fd,
// keyed by fd position = row) is IDENTICAL every run AND equals directory/spec order.
func TestHeadlessNodeRowOrderIsDeterministic(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	binPath := buildHeadlessBinary(t, repoRoot, "wirefold-headless-row-order-test")

	want := wantNodeRowOrder(t, repoRoot)

	const runs = 5
	var first []string
	for i := 0; i < runs; i++ {
		ds := spawnDedicatedAllStreams(t, binPath, repoRoot)
		nodeFrames := readLastFrames(t, ds.nodeReads, "node", 200)
		got := make([]string, len(nodeFrames))
		for row, frame := range nodeFrames {
			got[row] = nodeStreamRowID(frame)
		}

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
