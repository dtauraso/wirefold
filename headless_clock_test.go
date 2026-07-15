package main

// headless_clock_test.go — drives the REAL compiled binary headlessly (per
// memory/feedback_headless_repro_verifies_persistence.md: green unit tests have hidden
// live bridge failures 3x in this repo; the lesson is to drive the real binary and read
// the real bytes, not to trust an in-process mock). It builds ./wirefold, runs it against
// the repo's real `topology/` dir with fd 3 wired to a pipe, writes FRAMED binary
// `pause`/`play` stdin records (the same [len:u32-LE][kind byte] shape stdin_reader.go
// decodes — see input_codec.go inKindResume=1/inKindPause=2), and decodes the fd-3
// snapshot stream's Clock block (Buffer/snapshot.go writeClockBlock) to assert the
// Halted byte actually flips: main.go halts the clock before load, so the FIRST frame
// must show Halted=1; a `play` record must flip it to 0; a `pause` record must flip it
// back to 1.
//
// NEVER run the sim in the foreground (memory/feedback_no_foreground_sim_runs.md) — every
// blocking step here is bounded by an explicit context timeout, and the subprocess is
// force-killed in a deferred cleanup no matter how the test exits.

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// inKindResume/inKindPause mirror nodes/Wiring/input_codec.go's record-kind bytes for the
// bare play/pause commands (no payload — the record body is the single kind byte).
const (
	hcInKindResume = 1
	hcInKindPause  = 2
)

// writeFramedRecord writes one [len:u32-LE][record bytes] frame, the shape
// nodes/Wiring/stdin_reader.go's RunStdinReader decodes off stdin.
func writeFramedRecord(w io.Writer, kind byte) error {
	rec := []byte{kind}
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(rec)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.Write(rec)
	return err
}

// readOneSnapshotFrame reads one [len:u32-LE][snapshot bytes] frame off the fd-3 stream
// (the same framing Buffer/snapshot.go's emitSnapshot writes).
func readOneSnapshotFrame(r *bufio.Reader) ([]byte, error) {
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

// clockHaltedByte decodes the Clock block's Halted column out of one raw snapshot buffer,
// mirroring Buffer/snapshot.go's block order (header, bead, node, interior, edge, layout
// link, port, camera, overlay, scene, clock) and Buffer/buffer_layout_gen.go's strides.
// Duplicated here (rather than importing Buffer) to keep this headless test decoding the
// bytes independently of the package that produced them — an import-and-trust decode
// would not catch a Buffer/TS layout-mirroring bug the way raw byte math does.
func clockHaltedByte(snap []byte) byte {
	const (
		headerSize    = 40 // Buffer/buffer_layout_gen.go BufHeaderSize
		beadStride    = 17 // BufBeadStride
		nodeStride    = 62 // BufNodeStride
		interior      = 17 // BufInteriorStride
		interiorSlot  = 4  // BufInteriorSlotsPerNode
		edgeStride    = 42 // BufEdgeStride
		linkStride    = 12 // BufLayoutLinkStride
		portStride    = 38 // BufPortStride
		cameraStride  = 32 // BufCameraStride
		overlayStride = 10 // BufOverlayStride
		sceneStride   = 16 // BufSceneStride
	)
	readU32 := func(off int) uint32 { return binary.LittleEndian.Uint32(snap[off:]) }
	beadCount := readU32(4)
	nodeCount := readU32(8)
	edgeCount := readU32(12)
	portCount := readU32(16)
	layoutLinkCount := readU32(36)
	off := headerSize +
		int(beadCount)*beadStride +
		int(nodeCount)*nodeStride +
		int(nodeCount)*interiorSlot*interior +
		int(edgeCount)*edgeStride +
		int(layoutLinkCount)*linkStride +
		int(portCount)*portStride +
		cameraStride +
		overlayStride +
		sceneStride
	return snap[off]
}

// TestHeadlessClockHaltedFlipsOnPlayPause spawns the real ./wirefold binary against the
// repo's real topology/ dir, drives play/pause over framed stdin, and asserts the Clock
// block's Halted byte in the fd-3 snapshot stream actually flips.
func TestHeadlessClockHaltedFlipsOnPlayPause(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	binPath := filepath.Join(t.TempDir(), "wirefold-headless-test")

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

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	fd3Read, fd3Write, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	cmd.ExtraFiles = []*os.File{fd3Write} // becomes fd 3 in the child

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// fd3Write's child-side duplicate keeps it open in the child; the parent's copy must be
	// closed here so reads on fd3Read see EOF once the child exits, and so this process
	// doesn't itself hold a stray write end open.
	fd3Write.Close()

	// Bounded, forceful cleanup no matter how the test exits (never leave the sim running
	// per memory/feedback_no_foreground_sim_runs.md).
	t.Cleanup(func() {
		runCancel()
		_ = stdin.Close()
		_ = fd3Read.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	br := bufio.NewReader(fd3Read)

	readHalted := func(label string) byte {
		snap, err := readOneSnapshotFrame(br)
		if err != nil {
			t.Fatalf("%s: readOneSnapshotFrame: %v", label, err)
		}
		return clockHaltedByte(snap)
	}

	// main.go halts the clock BEFORE load, so the very first emitted frame (from
	// LoadTopology's geometry emits) must already show Halted=1.
	if got := readHalted("first frame"); got != 1 {
		t.Fatalf("first frame: Halted = %d, want 1 (main.go halts before load)", got)
	}

	// play → Halted must flip to 0. Keep sending frames forward until we observe the
	// flip or the context deadline stops us (Update only emits on state-change points,
	// so intervening frames unrelated to the clock are possible before the KindHalted
	// frame arrives).
	if err := writeFramedRecord(stdin, hcInKindResume); err != nil {
		t.Fatalf("write play record: %v", err)
	}
	haltedAfterPlay := waitForHalted(t, br, 0)
	if haltedAfterPlay != 0 {
		t.Fatalf("after play: Halted = %d, want 0 (running)", haltedAfterPlay)
	}

	// pause → Halted must flip back to 1.
	if err := writeFramedRecord(stdin, hcInKindPause); err != nil {
		t.Fatalf("write pause record: %v", err)
	}
	haltedAfterPause := waitForHalted(t, br, 1)
	if haltedAfterPause != 1 {
		t.Fatalf("after pause: Halted = %d, want 1 (halted)", haltedAfterPause)
	}
}

// waitForHalted reads snapshot frames until one shows Halted == want (proving the
// production hook actually delivered the transition), or fails the test if none arrives
// within a bounded number of frames.
func waitForHalted(t *testing.T, br *bufio.Reader, want byte) byte {
	t.Helper()
	const maxFrames = 500
	var last byte
	for i := 0; i < maxFrames; i++ {
		snap, err := readOneSnapshotFrame(br)
		if err != nil {
			t.Fatalf("waitForHalted: readOneSnapshotFrame: %v", err)
		}
		last = clockHaltedByte(snap)
		if last == want {
			return last
		}
	}
	return last
}
