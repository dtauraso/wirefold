// headless_edge_fd_test.go — drives the REAL compiled binary headlessly and proves the
// per-edge dedicated-stream migration end-to-end (memory/feedback_no_single_writer_bridge.md,
// Buffer/stream_fds.go's StreamKindEdge): with every per-owner fd wired (mandatory — no
// fd-3 fallback left, memory/feedback_no_single_writer_bridge.md's final step), every edge's own combined
// frame (Edge fields + its wire's live beads) arrives on its OWN fd.
//
// See headless_stream_helpers_test.go for the spawn/cleanup pattern this reuses; NEVER run
// the sim in the foreground (memory/feedback_no_foreground_sim_runs.md).
package main

import (
	"os"
	"testing"

	B "github.com/dtauraso/wirefold/Buffer"
)

// TestHeadlessEdgeFdDedicatedStream proves each edge's combined frame (Edge fields + its
// wire's own live beads) arrives on its OWN fd with resolvable geometry.
func TestHeadlessEdgeFdDedicatedStream(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	binPath := buildHeadlessBinary(t, repoRoot, "wirefold-headless-edge-fd-test")
	ds := spawnDedicatedAllStreams(t, binPath, repoRoot)
	if ds.edgeN == 0 {
		t.Fatal("topology/edges has 0 edges — test cannot assert per-edge frames without any")
	}

	edgeFrames := readLastFrames(t, ds.edgeReads, "edge", 120)

	for row, frame := range edgeFrames {
		// Combined frame layout (Buffer.BuildEdgeStreamFrame): [tick:u32] + one
		// BufEdgeStride row (SrcPortRow/DstPortRow/Selected, EdgeLabelOff=0/Len) + label
		// bytes + [beadCount:u32] + beadCount × BufBeadStride bead rows.
		if len(frame) < 4+B.BufEdgeStride+4 {
			t.Fatalf("edge row %d: frame too short (%d bytes) to hold [tick][EdgeRow][beadCount]", row, len(frame))
		}
		edgeRowOff := 4
		srcPortRow := int32(readU32(frame, edgeRowOff+0))
		dstPortRow := int32(readU32(frame, edgeRowOff+4))
		if srcPortRow < 0 {
			t.Fatalf("edge row %d: SrcPortRow = %d, want >= 0 (a resolvable source port)", row, srcPortRow)
		}
		if dstPortRow < 0 {
			t.Fatalf("edge row %d: DstPortRow = %d, want >= 0 (a resolvable dest port)", row, dstPortRow)
		}
		labelLenOff := edgeRowOff + B.BufEdgeStride - 4 // EdgeLabelLen is the last u32 column of the Edge row
		labelLen := readU32(frame, labelLenOff)
		labelStart := edgeRowOff + B.BufEdgeStride
		if labelStart+int(labelLen)+4 > len(frame) {
			t.Fatalf("edge row %d: label/beadCount overruns frame (labelLen=%d, frameLen=%d)", row, labelLen, len(frame))
		}
		label := string(frame[labelStart : labelStart+int(labelLen)])
		if label == "" {
			t.Fatalf("edge row %d: empty inline label", row)
		}
		beadCountOff := labelStart + int(labelLen)
		beadCount := readU32(frame, beadCountOff)
		if int(beadCount)*B.BufBeadStride+beadCountOff+4 > len(frame) {
			t.Fatalf("edge row %d: beadCount %d overruns frame", row, beadCount)
		}
	}
}
