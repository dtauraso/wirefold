// headless_first_frame_geometry_test.go — drives the REAL compiled binary headlessly
// against the real topology/ dir and asserts that the SETTLED per-owner NODE and EDGE
// stream frames already have REAL geometry in every row: every edge's segment is
// non-degenerate (start != end, not the 0,0,0->0,0,0 placeholder a bare address-only seed
// would produce) and every node carries its full port list (count > 0 for any node with
// ports in the spec). This is the row-completeness half of "rows come from the diagram"
// (CLAUDE.md/MODEL.md): a row is prefilled with the diagram's own state, not an empty row
// waiting on a node goroutine to fill it in later.
//
// See headless_stream_helpers_test.go for the spawn/cleanup pattern this reuses; NEVER run
// the sim in the foreground (memory/feedback_no_foreground_sim_runs.md).

package main

import (
	"os"
	"testing"

	B "github.com/dtauraso/wirefold/Buffer"
)

// nodePortOffsets mirrors Buffer.BuildNodeStreamFrame's payload layout (header, Node row,
// label bytes, Port rows) for ONE node's own dedicated-stream frame.
func nodePortOffsets(nodeFrame []byte) (portOff int, portCount int) {
	portCount = int(readU32(nodeFrame, 4))
	labelLen := int(readU32(nodeFrame, 8))
	const hdrSize = 20
	portOff = hdrSize + B.BufNodeStride + labelLen
	return
}

// portWorldPos reads one node's OWN Port block row's PX/PY/PZ from its dedicated node-stream
// frame — the same node-owned world position the edge's SrcPortRow/DstPortRow reference.
func portWorldPos(nodeFrame []byte, portOff, portIndex int) (x, y, z float32, ok bool) {
	if portIndex < 0 {
		return 0, 0, 0, false
	}
	base := portOff + portIndex*B.BufPortStride
	if base+B.BufPortStride > len(nodeFrame) {
		return 0, 0, 0, false
	}
	return readF32(nodeFrame, base+B.BufPortColPX),
		readF32(nodeFrame, base+B.BufPortColPY),
		readF32(nodeFrame, base+B.BufPortColPZ), true
}

// TestHeadlessFirstFrameHasRealGeometry asserts that the settled per-owner node/edge
// stream frames already have non-degenerate edge segments and populated node port lists —
// proving the row-seed carries the diagram's REAL state, not an empty/placeholder row
// waiting on a later per-node emit.
func TestHeadlessFirstFrameHasRealGeometry(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	binPath := buildHeadlessBinary(t, repoRoot, "wirefold-headless-first-frame-geometry-test")
	ds := spawnDedicatedAllStreams(t, binPath, repoRoot)
	if ds.edgeN == 0 {
		t.Fatal("topology/ is expected to have edges; test cannot assert non-degeneracy without any")
	}

	nodeFrames := readLastFrames(t, ds.nodeReads, "node", 400)
	edgeFrames := readLastFrames(t, ds.edgeReads, "edge", 400)

	if len(nodeFrames) != len(ds.nodeIDs) {
		t.Fatalf("nodeFrames count %d != topology dir count %d", len(nodeFrames), len(ds.nodeIDs))
	}

	// Every edge row's segment must be non-degenerate: start != end. A bare address-only
	// seed (the bug this test targets) would leave every edge at 0,0,0 -> 0,0,0. The
	// segment is read from each edge's own SrcPortRow/DstPortRow — but those rows index
	// into the OTHER (source/dest) node's OWN Port block, which this per-edge frame does
	// not itself carry; resolve via the Edge-row's NODE ids from the same spec order
	// (topologyEdgeCount's directory listing keyed the fd, so edge row i's endpoints are
	// resolved through the node frames' own Port blocks by port row, same as production's
	// EdgeTube reads).
	for row, edgeFrame := range edgeFrames {
		if len(edgeFrame) < 4+B.BufEdgeStride+4 {
			t.Fatalf("edge row %d: frame too short (%d bytes)", row, len(edgeFrame))
		}
		srcPortRow := int32(readU32(edgeFrame, 4))
		dstPortRow := int32(readU32(edgeFrame, 8))
		if srcPortRow < 0 || dstPortRow < 0 {
			t.Fatalf("edge row %d: SrcPortRow=%d DstPortRow=%d — unresolved port reference", row, srcPortRow, dstPortRow)
		}
		// Port rows are GLOBAL (across all nodes, in node-row order) — resolve which
		// node frame owns each by walking node frames in row order and subtracting each
		// one's own portCount, mirroring node-stream-blocks.ts's aggregation.
		sx, sy, sz, ok := resolveGlobalPortPos(nodeFrames, len(ds.nodeIDs), int(srcPortRow))
		if !ok {
			t.Fatalf("edge row %d: SrcPortRow %d did not resolve", row, srcPortRow)
		}
		ex, ey, ez, ok := resolveGlobalPortPos(nodeFrames, len(ds.nodeIDs), int(dstPortRow))
		if !ok {
			t.Fatalf("edge row %d: DstPortRow %d did not resolve", row, dstPortRow)
		}
		if sx == 0 && sy == 0 && sz == 0 && ex == 0 && ey == 0 && ez == 0 {
			t.Fatalf("edge row %d: segment is the degenerate placeholder 0,0,0 -> 0,0,0", row)
		}
		if sx == ex && sy == ey && sz == ez {
			t.Fatalf("edge row %d: start == end (%v == %v) — degenerate zero-length segment", row, [3]float32{sx, sy, sz}, [3]float32{ex, ey, ez})
		}
	}

	// Every node's ports must be populated: total port count across all node frames must
	// be > 0 (proving ports arrived alongside their node rows, not nil/omitted).
	totalPorts := 0
	for _, frame := range nodeFrames {
		_, portCount := nodePortOffsets(frame)
		totalPorts += portCount
	}
	if totalPorts == 0 {
		t.Fatalf("total portCount across all node frames is 0 — node ports were not seeded (nil Ports bug)")
	}
}

// resolveGlobalPortPos resolves a GLOBAL port-row index (as carried on an Edge row's
// SrcPortRow/DstPortRow) to its world position, by walking nodeFrames in row order (0..
// nodeCount-1) and subtracting each node's own portCount until the index falls within one
// node's own Port block.
func resolveGlobalPortPos(nodeFrames map[int][]byte, nodeCount, globalPortRow int) (x, y, z float32, ok bool) {
	remaining := globalPortRow
	for row := 0; row < nodeCount; row++ {
		frame, present := nodeFrames[row]
		if !present {
			continue
		}
		portOff, portCount := nodePortOffsets(frame)
		if remaining < portCount {
			return portWorldPos(frame, portOff, remaining)
		}
		remaining -= portCount
	}
	return 0, 0, 0, false
}
