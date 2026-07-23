// headless_stream_helpers_test.go — shared low-level helpers + the one spawn routine every
// headless per-owner-stream test in this package uses to drive the REAL compiled binary
// with WIREFOLD_STREAM_FDS fully wired (view + one fd per edge row + one NODE/INTERIOR fd
// per node row) — MANDATORY now that Buffer.SnapshotState's central accumulator and its fd-3
// fallback were deleted (memory/feedback_no_single_writer_bridge.md's final step): there is no fallback path
// left to fall back to, so every headless test that used to exercise "fd3 only" must wire
// the real per-owner fds instead. NEVER run the sim in the foreground
// (memory/feedback_no_foreground_sim_runs.md).
package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// itoa is a trivial base-10 non-negative int formatter, sufficient for fd numbers — avoids
// importing strconv solely for this across every headless test file in this package.
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

func readU32(buf []byte, off int) uint32 { return binary.LittleEndian.Uint32(buf[off:]) }

func readF32(buf []byte, off int) float32 {
	bits := uint32(buf[off]) | uint32(buf[off+1])<<8 | uint32(buf[off+2])<<16 | uint32(buf[off+3])<<24
	return math.Float32frombits(bits)
}

// readOneRawFrame reads one [len:u32-LE][payload] frame with NO tag byte — the wire shape
// on every dedicated stream fd (the fd position identifies the stream; see
// Buffer/stream_fds.go).
func readOneRawFrame(r *bufio.Reader) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.LittleEndian.Uint32(lenBuf[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// wantNodeRowOrder is the expected spec order for the repo's real topology/nodes dir:
// lexicographic directory-name sort, per nodes/Wiring/loader_tree.go readDirNames +
// sort.Strings. Recomputed from the actual directory listing (not hardcoded) so this test
// does not silently go stale if a node is added/removed from topology/.
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

// dedicatedStreams holds every dedicated-fd reader from one spawnDedicatedAllStreams call,
// plus the node/edge id counts used to size them.
type dedicatedStreams struct {
	nodeIDs  []string
	edgeN    int
	viewRead *os.File

	edgeReads     []*os.File
	nodeReads     []*os.File
	interiorReads []*os.File

	stdinWrite *os.File
	cmd        *exec.Cmd
}

// buildHeadlessBinary compiles the real binary to a fresh temp path shared by the caller's
// test — one build per test, mirroring every existing headless test's own pattern.
func buildHeadlessBinary(t *testing.T, repoRoot, name string) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), name)
	buildCtx, buildCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer buildCancel()
	buildCmd := exec.CommandContext(buildCtx, "go", "build", "-o", binPath, ".")
	buildCmd.Dir = repoRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return binPath
}

// spawnDedicatedAllStreams spawns binPath against repoRoot's real topology/ dir with EVERY
// per-owner stream fd wired — view (fd 4), one fd per edge row (fd 5..5+edgeCount-1), then
// one NODE fd and one INTERIOR fd per node row — mirroring runCommand.ts's BuildAndRunRunner
// spawn shape exactly (memory/feedback_no_single_writer_bridge.md). This is now the ONLY
// wiring: WIREFOLD_STREAM_FDS is mandatory (no fd-3 fallback left to fall back to). Keeps
// stdin open for the process's whole lifetime (RunStdinReader treats stdin EOF as "editor
// disconnected" and cancels ctx) so callers can read a sustained stream, not a near-instant
// shutdown.
func spawnDedicatedAllStreams(t *testing.T, binPath, repoRoot string) *dedicatedStreams {
	t.Helper()
	nodeIDs := wantNodeRowOrder(t, repoRoot)
	nodeCount := len(nodeIDs)
	edgeCount := topologyEdgeCount(t, repoRoot)

	runCtx, runCancel := context.WithTimeout(context.Background(), 20*time.Second)
	cmd := exec.CommandContext(runCtx, binPath, "-topology", filepath.Join(repoRoot, "topology"))
	cmd.Dir = repoRoot
	cmd.Stderr = os.Stderr

	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe (stdin): %v", err)
	}
	cmd.Stdin = stdinRead

	// fd3 stays a reserved, unused pipe slot (nothing writes to it anymore) so the
	// remaining fd numbering (view=4, edge base=5, …) matches runCommand.ts's fixed
	// layout exactly — see Buffer/stream_fds.go / runCommand.ts's fd-allocation comment.
	_, fd3Write, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe (fd3): %v", err)
	}
	cmd.ExtraFiles = []*os.File{fd3Write} // fd3

	viewRead, viewWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe (view): %v", err)
	}
	cmd.ExtraFiles = append(cmd.ExtraFiles, viewWrite) // fd4

	edgeBase := 5
	edgeReads := make([]*os.File, 0, edgeCount)
	for i := 0; i < edgeCount; i++ {
		er, ew, err := os.Pipe()
		if err != nil {
			t.Fatalf("Pipe (edge %d): %v", i, err)
		}
		cmd.ExtraFiles = append(cmd.ExtraFiles, ew) // fd edgeBase+i
		edgeReads = append(edgeReads, er)
	}

	nodeBase := edgeBase + edgeCount
	interiorBase := nodeBase + nodeCount
	nodeReads := make([]*os.File, 0, nodeCount)
	for i := 0; i < nodeCount; i++ {
		nr, nw, err := os.Pipe()
		if err != nil {
			t.Fatalf("Pipe (node %d): %v", i, err)
		}
		cmd.ExtraFiles = append(cmd.ExtraFiles, nw) // fd nodeBase+i
		nodeReads = append(nodeReads, nr)
	}
	interiorReads := make([]*os.File, 0, nodeCount)
	for i := 0; i < nodeCount; i++ {
		ir, iw, err := os.Pipe()
		if err != nil {
			t.Fatalf("Pipe (interior %d): %v", i, err)
		}
		cmd.ExtraFiles = append(cmd.ExtraFiles, iw) // fd interiorBase+i
		interiorReads = append(interiorReads, ir)
	}

	streamFDsEnv := "view:4"
	if edgeCount > 0 {
		streamFDsEnv += ",edge:" + itoa(edgeBase)
	}
	if nodeCount > 0 {
		streamFDsEnv += ",node:" + itoa(nodeBase) + ",interior:" + itoa(interiorBase)
	}
	cmd.Env = append(os.Environ(), "WIREFOLD_STREAM_FDS="+streamFDsEnv)

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = stdinRead.Close()
	_ = fd3Write.Close()
	for _, f := range cmd.ExtraFiles[1:] {
		_ = f.Close()
	}

	ds := &dedicatedStreams{
		nodeIDs: nodeIDs, edgeN: edgeCount,
		viewRead: viewRead, edgeReads: edgeReads, nodeReads: nodeReads, interiorReads: interiorReads,
		stdinWrite: stdinWrite, cmd: cmd,
	}
	t.Cleanup(func() {
		runCancel()
		_ = ds.stdinWrite.Close()
		_ = ds.viewRead.Close()
		for _, r := range ds.edgeReads {
			_ = r.Close()
		}
		for _, r := range ds.nodeReads {
			_ = r.Close()
		}
		for _, r := range ds.interiorReads {
			_ = r.Close()
		}
		if ds.cmd.Process != nil {
			_ = ds.cmd.Process.Kill()
		}
		_ = ds.cmd.Wait()
	})
	return ds
}

// readLastFrames reads up to maxFrames from each of reads (bounded, non-blocking-in-effect
// since production keeps emitting on a change/tick cadence) and returns the LAST complete
// frame seen per row — the current, settled state, not the very first (possibly still-
// degenerate) one.
func readLastFrames(t *testing.T, reads []*os.File, kind string, maxFrames int) map[int][]byte {
	t.Helper()
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
