// headless_node_fd_test.go — drives the REAL compiled binary headlessly and proves the
// per-node dedicated-stream migration end-to-end, both paths of the required dual path
// (memory/feedback_no_single_writer_bridge.md, Buffer/stream_fds.go's StreamKindNode/
// StreamKindInterior):
//
//   - TestHeadlessNodeFdDedicatedStream: WIREFOLD_STREAM_FDS="view:4,node:<base>,
//     interior:<base>" wired (one fd per node row for each of the two kinds) — every
//     node's own combined frame (Node fields + ports + label) arrives on its OWN "node"
//     fd, every node's own interior-bead frame arrives on its OWN "interior" fd, and the
//     fd-3 stream's frames carry NO Node block tag at all.
//   - TestHeadlessNodeFdFallback: WIREFOLD_STREAM_FDS unset — the Node/Interior/Port/
//     Label/PortName frame stays on fd 3 exactly as before this migration.
//
// See headless_edge_fd_test.go for the spawn/fd3/cleanup pattern this reuses; NEVER run
// the sim in the foreground (memory/feedback_no_foreground_sim_runs.md).
package main

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	B "github.com/dtauraso/wirefold/Buffer"
)

// runHeadlessNodeFdCase builds the real binary and spawns it with fd3 always wired.
// dedicated=true additionally wires fd4 as the VIEW stream and one fd per node row for
// EACH of "node" and "interior" (WIREFOLD_STREAM_FDS="view:4,node:5,interior:5+N").
// dedicated=false leaves WIREFOLD_STREAM_FDS unset (the fallback). Returns whether ANY
// fd-3 frame carried the Node tag, plus (when dedicated) the first non-empty frame read
// off each node row's own "node" and "interior" fds.
// runHeadlessNodeFdCase also returns the LAST fd-3 scene frame's header layoutLinkCount
// (see Buffer/pack.go's newSnapshotBuild) so callers can assert the LayoutLink block's
// either/or with the per-node streams (gated by nodeStreamActive).
func runHeadlessNodeFdCase(t *testing.T, dedicated bool) (sawNodeTag bool, nodeFrames, interiorFrames map[int][]byte, lastSceneLayoutLinks uint32) {
	t.Helper()
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	nodeIDs := wantNodeRowOrder(t, repoRoot)
	nodeCount := len(nodeIDs)
	binPath := filepath.Join(t.TempDir(), "wirefold-headless-node-fd-test")

	buildCtx, buildCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer buildCancel()
	buildCmd := exec.CommandContext(buildCtx, "go", "build", "-o", binPath, ".")
	buildCmd.Dir = repoRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	runCtx, runCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer runCancel()

	cmd := exec.CommandContext(runCtx, binPath, "-topology", filepath.Join(repoRoot, "topology"))
	cmd.Dir = repoRoot
	cmd.Stderr = os.Stderr

	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe (stdin): %v", err)
	}
	cmd.Stdin = stdinRead

	fd3Read, fd3Write, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe (fd3): %v", err)
	}
	cmd.ExtraFiles = []*os.File{fd3Write} // fd3

	var nodeReads, interiorReads []*os.File
	if dedicated {
		_, viewWrite, err := os.Pipe()
		if err != nil {
			t.Fatalf("Pipe (view): %v", err)
		}
		cmd.ExtraFiles = append(cmd.ExtraFiles, viewWrite) // fd4
		nodeBase := 5
		interiorBase := nodeBase + nodeCount
		for i := 0; i < nodeCount; i++ {
			nr, nw, err := os.Pipe()
			if err != nil {
				t.Fatalf("Pipe (node %d): %v", i, err)
			}
			cmd.ExtraFiles = append(cmd.ExtraFiles, nw) // fd nodeBase+i
			nodeReads = append(nodeReads, nr)
		}
		for i := 0; i < nodeCount; i++ {
			ir, iw, err := os.Pipe()
			if err != nil {
				t.Fatalf("Pipe (interior %d): %v", i, err)
			}
			cmd.ExtraFiles = append(cmd.ExtraFiles, iw) // fd interiorBase+i
			interiorReads = append(interiorReads, ir)
		}
		cmd.Env = append(os.Environ(),
			"WIREFOLD_STREAM_FDS=view:4,node:5,interior:"+itoa(interiorBase))
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = stdinRead.Close()
	fd3Write.Close()
	for _, f := range cmd.ExtraFiles[1:] {
		_ = f.Close()
	}

	t.Cleanup(func() {
		runCancel()
		_ = stdinWrite.Close()
		_ = fd3Read.Close()
		for _, r := range nodeReads {
			_ = r.Close()
		}
		for _, r := range interiorReads {
			_ = r.Close()
		}
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	br := bufio.NewReader(fd3Read)
	const maxFrames = 120
	sawScene := false
	for i := 0; i < maxFrames; i++ {
		buf, err := readOneTaggedFrame(br)
		if err != nil {
			t.Fatalf("frame %d: readOneTaggedFrame: %v", i, err)
		}
		switch buf[0] {
		case B.BufBlockTagScene:
			sawScene = true
			// Scene block payload (buf[1:]) header: [tick:u32][eventCount:u32]
			// [layoutLinkCount:u32] (Buffer/pack.go's writeHeader) — capture the LAST
			// one seen so the caller can assert the required either/or with the
			// per-node streams.
			if len(buf) >= 1+12 {
				lastSceneLayoutLinks = readU32(buf[1:], 8)
			}
		case B.BufBlockTagNode:
			sawNodeTag = true
		}
	}
	if !sawScene {
		t.Fatalf("no scene frame seen within %d frames", maxFrames)
	}

	readLast := func(reads []*os.File, kind string) map[int][]byte {
		out := make(map[int][]byte, len(reads))
		for row, f := range reads {
			r := bufio.NewReader(f)
			var last []byte
			for i := 0; i < maxFrames; i++ {
				buf, err := readOneRawFrame(r)
				if err != nil {
					if i == 0 {
						t.Fatalf("readOneRawFrame (%s row %d), frame %d: %v", kind, row, i, err)
					}
					break
				}
				last = buf
			}
			if last == nil {
				t.Fatalf("no frame seen on %s row %d's dedicated fd", kind, row)
			}
			out[row] = last
		}
		return out
	}

	if dedicated {
		nodeFrames = readLast(nodeReads, "node")
		interiorFrames = readLast(interiorReads, "interior")
	}

	return sawNodeTag, nodeFrames, interiorFrames, lastSceneLayoutLinks
}

// itoa avoids importing strconv twice across test files in this package; trivial base-10
// non-negative int formatter, sufficient for fd numbers.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestHeadlessNodeFdDedicatedStream proves the PROOF half of the dual path: with every
// node row's own "node"+"interior" fds wired, each node's combined geometry+ports+label
// frame arrives on its OWN "node" fd with resolvable content, each node's interior frame
// arrives on its OWN "interior" fd, and the fd-3 stream never carries a Node-tagged frame
// (i.e. it is NOT double-sourced from both places).
func TestHeadlessNodeFdDedicatedStream(t *testing.T) {
	sawNodeTag, nodeFrames, interiorFrames, sceneLayoutLinks := runHeadlessNodeFdCase(t, true)

	if sawNodeTag {
		t.Fatalf("fd-3 stream carried a Node-tagged frame while the dedicated node fds were active — the fd-3 Node block was not excluded")
	}
	if sceneLayoutLinks != 0 {
		t.Fatalf("fd-3 scene frame's LayoutLink block carried %d rows while the dedicated node fds were active — it must be empty (each node's own layout-links now stream on its own node fd)", sceneLayoutLinks)
	}

	repoRoot, _ := os.Getwd()
	wantLabels := wantNodeRowOrder(t, repoRoot)
	sort.Strings(wantLabels) // wantNodeRowOrder already returns sorted spec order

	totalLayoutLinks := 0
	for row, frame := range nodeFrames {
		// Combined frame layout (Buffer.BuildNodeStreamFrame): [tick:u32][portCount:u32]
		// [labelLen:u32][portNameBytesCount:u32][layoutLinkCount:u32] + Node row + label
		// bytes + Port rows + port-name bytes + LayoutLink rows
		// ([DstNodeRow:i32][EdgeRow:i32] each, BufNodeStreamLayoutLinkStride bytes).
		const hdrSize = 20
		if len(frame) < hdrSize+B.BufNodeStride {
			t.Fatalf("node row %d: frame too short (%d bytes) to hold header+Node row", row, len(frame))
		}
		portCount := readU32(frame, 4)
		labelLen := readU32(frame, 8)
		portNameBytesCount := readU32(frame, 12)
		layoutLinkCount := readU32(frame, 16)
		nodeOff := hdrSize
		labelOff := nodeOff + B.BufNodeStride
		if labelOff+int(labelLen) > len(frame) {
			t.Fatalf("node row %d: label overruns frame (labelLen=%d, frameLen=%d)", row, labelLen, len(frame))
		}
		label := string(frame[labelOff : labelOff+int(labelLen)])
		if label == "" {
			t.Fatalf("node row %d: empty inline label", row)
		}
		if row < len(wantLabels) && label != wantLabels[row] {
			t.Fatalf("node row %d: label = %q, want %q (falls back to node id)", row, label, wantLabels[row])
		}
		portsOff := labelOff + int(labelLen)
		portNamesOff := portsOff + int(portCount)*B.BufPortStride
		layoutLinksOff := portNamesOff + int(portNameBytesCount)
		wantLen := layoutLinksOff + int(layoutLinkCount)*B.BufNodeStreamLayoutLinkStride
		if wantLen != len(frame) {
			t.Fatalf("node row %d: frame length %d does not match computed layout end %d",
				row, len(frame), wantLen)
		}
		// Every Port row's NodeRow column must equal this frame's own node row (the
		// tear-free cross-stream contract: EdgeTube resolves a port row to (nodeRow,
		// portIndex) and looks up coordinates in THIS node's own cell).
		for p := 0; p < int(portCount); p++ {
			portRowOff := portsOff + p*B.BufPortStride
			gotNodeRow := int32(readU32(frame, portRowOff))
			if int(gotNodeRow) != row {
				t.Fatalf("node row %d: port %d's NodeRow column = %d, want %d", row, p, gotNodeRow, row)
			}
		}
		// Every LayoutLink row's DstNodeRow must be a valid, DIFFERENT node row (never
		// this node's own row — a node is never its own layout-link partner). EdgeRow is
		// either -1 (no bead edge connects the pair — the node-centers overlay fallback)
		// or a non-negative resolved edge row; either is valid, so only the sign is
		// checked here (its exact value is cross-checked against the Edge block by the
		// TS-side EdgeTube/node-stream-blocks tests, not this Go-side frame-shape test).
		for l := 0; l < int(layoutLinkCount); l++ {
			rowOff := layoutLinksOff + l*B.BufNodeStreamLayoutLinkStride
			dstNodeRow := int32(readU32(frame, rowOff))
			edgeRow := int32(readU32(frame, rowOff+4))
			if dstNodeRow < 0 || int(dstNodeRow) >= len(nodeFrames) {
				t.Fatalf("node row %d: layout-link %d DstNodeRow=%d out of range [0,%d)", row, l, dstNodeRow, len(nodeFrames))
			}
			if int(dstNodeRow) == row {
				t.Fatalf("node row %d: layout-link %d DstNodeRow equals its own row (self-link)", row, l)
			}
			if edgeRow < -1 {
				t.Fatalf("node row %d: layout-link %d EdgeRow=%d invalid (must be -1 or >= 0)", row, l, edgeRow)
			}
		}
		totalLayoutLinks += int(layoutLinkCount)
	}
	// This topology's local-polars data (topology/nodes/*/local-polars.json) declares real
	// double-link pairs — the per-node streams must actually carry SOME layout-links, not
	// silently zero every row (a real regression: e.g. layoutLinkTos never wired, or every
	// dst id failing to resolve a node row).
	if totalLayoutLinks == 0 {
		t.Fatalf("no node row streamed any layout-link — expected this topology's local-polars pairs to appear on their source node's own fd")
	}

	for row, frame := range interiorFrames {
		// Fixed-slot frame (Buffer.BuildInteriorStreamFrame): [tick:u32] + 4 Interior rows.
		want := 4 + 4*B.BufInteriorStride
		if len(frame) != want {
			t.Fatalf("interior row %d: frame length %d, want %d (fixed 4-slot layout)", row, len(frame), want)
		}
	}
}

// TestHeadlessNodeFdFallback proves the FALLBACK half of the dual path
// (feedback_headless_repro_verifies_persistence: real binary, on-wire bytes, strong
// assertions): with WIREFOLD_STREAM_FDS unset (no dedicated node/interior fds — the shape
// every existing headless launch already gets), the Node/Interior/Port/Label/PortName
// frame keeps shipping on fd 3 exactly as before this migration.
func TestHeadlessNodeFdFallback(t *testing.T) {
	sawNodeTag, _, _, sceneLayoutLinks := runHeadlessNodeFdCase(t, false)

	if !sawNodeTag {
		t.Fatalf("fd-3 stream never carried a Node-tagged frame with the dedicated node fds inactive — the fallback path is broken")
	}
	if sceneLayoutLinks == 0 {
		t.Fatalf("fd-3 scene frame's LayoutLink block carried 0 rows with the dedicated node fds inactive — the fallback must keep emitting the shared LayoutLink block (this topology's local-polars data declares real pairs)")
	}
}
