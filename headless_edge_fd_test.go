// headless_edge_fd_test.go — drives the REAL compiled binary headlessly and proves the
// per-edge dedicated-stream migration end-to-end, both paths of the required dual path
// (memory/feedback_no_single_writer_bridge.md, Buffer/stream_fds.go's StreamKindEdge):
//
//   - TestHeadlessEdgeFdDedicatedStream: WIREFOLD_STREAM_FDS="view:4,edge:5" wired
//     (mirroring runCommand.ts's EDGE_BASE_FD=5 spawn, one fd per edge) — every edge's
//     own combined frame (Edge fields + its wire's live beads) arrives on its OWN fd, and
//     the fd-3 scene stream's frames carry NO Bead or Edge block tag at all.
//   - TestHeadlessEdgeFdFallback: WIREFOLD_STREAM_FDS unset — the Bead and Edge blocks
//     stay on fd 3 exactly as before this migration (headless tests, non-extension
//     launches).
//
// See headless_view_fd_test.go for the spawn/fd3/cleanup pattern this reuses; NEVER run
// the sim in the foreground (memory/feedback_no_foreground_sim_runs.md).
package main

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	B "github.com/dtauraso/wirefold/Buffer"
)

// topologyEdgeCount mirrors runCommand.ts's countEdges for the tree-form topology fixture
// this repo ships (<repoRoot>/topology/edges/*.json) — one file per edge.
func topologyEdgeCount(t *testing.T, repoRoot string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repoRoot, "topology", "edges"))
	if err != nil {
		t.Fatalf("ReadDir(topology/edges): %v", err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			n++
		}
	}
	return n
}

// runHeadlessEdgeFdCase builds the real binary and spawns it with fd3 always wired.
// dedicated=true additionally wires fd 4 as the VIEW stream and one fd per edge starting
// at fd 5 (WIREFOLD_STREAM_FDS="view:4,edge:5"), mirroring runCommand.ts's spawn shape.
// dedicated=false leaves WIREFOLD_STREAM_FDS unset (the fallback). Returns the LAST
// fd-3 SCENE frame seen (tag byte stripped), whether ANY fd-3 frame carried the Bead or
// Edge tag, and (when dedicated) the first non-empty combined frame read off each edge's
// own fd, keyed by edge row.
func runHeadlessEdgeFdCase(t *testing.T, dedicated bool) (sceneFrame []byte, sawBeadTag, sawEdgeTag bool, edgeFrames map[int][]byte) {
	t.Helper()
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	edgeCount := topologyEdgeCount(t, repoRoot)
	binPath := filepath.Join(t.TempDir(), "wirefold-headless-edge-fd-test")

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
	// Hold stdin open for the process's whole lifetime: RunStdinReader (nodes/Wiring/
	// stdin_reader.go) treats stdin EOF as "editor disconnected" and cancels ctx,
	// shutting every goroutine (including every edgeMover) down almost immediately —
	// exec.Command leaves Stdin unset otherwise, which connects the child's stdin to
	// /dev/null (an immediate EOF). Keeping the write end open (never written to, never
	// closed until t.Cleanup) matches the extension host's real long-lived stdin pipe and
	// lets this test read a real, sustained stream of edge frames instead of racing a
	// near-instant shutdown.
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

	var edgeReads []*os.File
	if dedicated {
		// fd4 = view (required alongside fd3 in cmd.ExtraFiles positional layout: fd 3
		// is ExtraFiles[0], fd 4 is ExtraFiles[1], fd 5 is ExtraFiles[2], …).
		_, viewWrite, err := os.Pipe()
		if err != nil {
			t.Fatalf("Pipe (view): %v", err)
		}
		cmd.ExtraFiles = append(cmd.ExtraFiles, viewWrite) // fd4
		for i := 0; i < edgeCount; i++ {
			er, ew, err := os.Pipe()
			if err != nil {
				t.Fatalf("Pipe (edge %d): %v", i, err)
			}
			cmd.ExtraFiles = append(cmd.ExtraFiles, ew) // fd 5+i
			edgeReads = append(edgeReads, er)
		}
		cmd.Env = append(os.Environ(), "WIREFOLD_STREAM_FDS=view:4,edge:5")
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// The parent's copy of the read end (cmd.Stdin already duped it into the child) is not
	// needed here; only stdinWrite must stay open for the duration.
	_ = stdinRead.Close()
	fd3Write.Close()
	// Close every write end this test process holds a copy of (the child has its own).
	for _, f := range cmd.ExtraFiles[1:] {
		_ = f.Close()
	}

	t.Cleanup(func() {
		runCancel()
		_ = stdinWrite.Close()
		_ = fd3Read.Close()
		for _, er := range edgeReads {
			_ = er.Close()
		}
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	br := bufio.NewReader(fd3Read)
	const maxFrames = 120
	for i := 0; i < maxFrames; i++ {
		buf, err := readOneTaggedFrame(br)
		if err != nil {
			t.Fatalf("frame %d: readOneTaggedFrame: %v", i, err)
		}
		switch buf[0] {
		case B.BufBlockTagScene:
			sceneFrame = buf[1:]
		case B.BufBlockTagBead:
			sawBeadTag = true
		case B.BufBlockTagEdge:
			sawEdgeTag = true
		}
	}
	if sceneFrame == nil {
		t.Fatalf("no scene frame seen within %d frames", maxFrames)
	}

	if dedicated {
		edgeFrames = make(map[int][]byte, len(edgeReads))
		for row, er := range edgeReads {
			r := bufio.NewReader(er)
			// Read up to a bounded number of frames, keep the LAST — mirrors the view-fd
			// test's "use the most recent frame" reasoning (an edge's own combined frame
			// carries beads that churn every cycle; the assertions below only need geometry
			// fields, which stabilize quickly after startup).
			var last []byte
			for i := 0; i < maxFrames; i++ {
				buf, err := readOneRawFrame(r)
				if err != nil {
					if i == 0 {
						t.Fatalf("readOneRawFrame (edge row %d), frame %d: %v", row, i, err)
					}
					break
				}
				last = buf
			}
			if last == nil {
				t.Fatalf("no frame seen on edge row %d's dedicated fd", row)
			}
			edgeFrames[row] = last
		}
	}

	return sceneFrame, sawBeadTag, sawEdgeTag, edgeFrames
}

// TestHeadlessEdgeFdDedicatedStream proves the PROOF half of the dual path: with every
// edge's own fd wired (WIREFOLD_STREAM_FDS="view:4,edge:5", mirroring runCommand.ts's
// spawn), each edge's combined frame (Edge fields + its wire's own live beads) arrives on
// its OWN fd with resolvable geometry, and the fd-3 stream never carries a Bead or Edge
// tagged frame — i.e. they are NOT double-sourced from both places.
func TestHeadlessEdgeFdDedicatedStream(t *testing.T) {
	_, sawBeadTag, sawEdgeTag, edgeFrames := runHeadlessEdgeFdCase(t, true)

	if sawBeadTag {
		t.Fatalf("fd-3 stream carried a Bead-tagged frame while the dedicated edge fds were active — the fd-3 Bead block was not excluded")
	}
	if sawEdgeTag {
		t.Fatalf("fd-3 stream carried an Edge-tagged frame while the dedicated edge fds were active — the fd-3 Edge block was not excluded")
	}

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

// TestHeadlessEdgeFdFallback proves the FALLBACK half of the dual path
// (feedback_headless_repro_verifies_persistence: real binary, on-wire bytes, strong
// assertions): with WIREFOLD_STREAM_FDS unset (no dedicated edge fds — the shape every
// existing headless launch already gets), the Bead and Edge blocks keep shipping on fd 3
// exactly as before this migration.
func TestHeadlessEdgeFdFallback(t *testing.T) {
	_, sawBeadTag, sawEdgeTag, _ := runHeadlessEdgeFdCase(t, false)

	if !sawBeadTag {
		t.Fatalf("fd-3 stream never carried a Bead-tagged frame with the dedicated edge fds inactive — the fallback path is broken")
	}
	if !sawEdgeTag {
		t.Fatalf("fd-3 stream never carried an Edge-tagged frame with the dedicated edge fds inactive — the fallback path is broken")
	}
}
