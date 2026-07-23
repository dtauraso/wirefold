// headless_view_fd_test.go — drives the REAL compiled binary headlessly and proves the
// VIEW-stream dedicated-fd migration end-to-end, both paths of the required dual path
// (memory/feedback_no_single_writer_bridge.md, Buffer/stream_fds.go):
//
//   - TestHeadlessViewFdDedicatedStream: WIREFOLD_STREAM_FDS="view:4" wired (mirroring
//     runCommand.ts's VIEW_FD=4 spawn) — camera/overlay/scene arrive on their OWN fd-4
//     frame (buildViewFrame) and are EXCLUDED from the fd-3 scene frame.
//   - TestHeadlessViewFdFallback: WIREFOLD_STREAM_FDS unset — camera/overlay/scene stay
//     embedded in the fd-3 scene frame, exactly as before this migration (headless tests,
//     non-extension launches).
//
// See headless_first_frame_geometry_test.go for the spawn/fd3/cleanup pattern this reuses;
// NEVER run the sim in the foreground (memory/feedback_no_foreground_sim_runs.md).
package main

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

	B "github.com/dtauraso/wirefold/Buffer"
)

// sceneFrameViewLens computes the two candidate scene-frame payload lengths (see
// Buffer/pack.go's buildSnapshot either/or): withoutViewLen when camera/overlay/scene are
// EXCLUDED (dedicated view fd active) and withViewLen when they are embedded (fallback).
// Mirrors buffer-decode.ts's decodeSnapshotUncached byte-length discriminator.
func sceneFrameViewLens(scene []byte) (withoutViewLen, withViewLen int) {
	layoutLinkCount := int(readU32(scene, 4))
	withoutViewLen = B.BufHeaderSize + layoutLinkCount*B.BufLayoutLinkStride
	withViewLen = withoutViewLen + B.BufCameraStride + B.BufOverlayStride + B.BufSceneStride
	return
}

// readOneRawFrame reads one [len:u32-LE][payload] frame with NO tag byte — the wire shape
// on a dedicated stream fd (the fd position identifies the stream; see
// Buffer/stream_fds.go). Mirrors readOneTaggedFrame (headless_node_row_order_test.go)
// minus the tag-byte convention.
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

// runHeadlessViewFdCase builds the real binary once (shared across the two subtests via
// TestMain-less reuse — each test builds its own temp binary, mirroring the existing
// headless tests' pattern of one build per test) and spawns it with fd3 always wired.
// dedicated=true additionally wires fd 4 as the VIEW stream (WIREFOLD_STREAM_FDS=view:4);
// dedicated=false leaves WIREFOLD_STREAM_FDS unset (the fallback). Returns the first
// SCENE frame payload (tag byte stripped) and, when dedicated, the first VIEW frame
// payload (nil when dedicated=false, since nothing is wired to fd 4 in that case).
func runHeadlessViewFdCase(t *testing.T, dedicated bool) (sceneFrame, viewFrame []byte) {
	t.Helper()
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	binPath := filepath.Join(t.TempDir(), "wirefold-headless-view-fd-test")

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

	fd3Read, fd3Write, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe (fd3): %v", err)
	}
	cmd.ExtraFiles = []*os.File{fd3Write} // fd3

	var viewRead, viewWrite *os.File
	if dedicated {
		viewRead, viewWrite, err = os.Pipe()
		if err != nil {
			t.Fatalf("Pipe (view): %v", err)
		}
		cmd.ExtraFiles = append(cmd.ExtraFiles, viewWrite) // fd4
		cmd.Env = append(os.Environ(), "WIREFOLD_STREAM_FDS=view:4")
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	fd3Write.Close()
	if dedicated {
		viewWrite.Close()
	}

	t.Cleanup(func() {
		runCancel()
		_ = fd3Read.Close()
		if viewRead != nil {
			_ = viewRead.Close()
		}
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	br := bufio.NewReader(fd3Read)
	const maxFrames = 200
	// Keep the LAST scene frame seen, not the first: startup emits several scene frames
	// before SeedInitialViewpoint installs a real (non-degenerate) camera pose (main.go's
	// node/edge row-seed loop runs first), so an early frame can legitimately still carry
	// the zero-value camera row. The fallback case's r>0 assertion needs a frame from
	// after that point; the dedicated case doesn't need r>0 from THIS stream at all (its
	// camera lives on the view fd), but taking the last frame is harmless there too.
	for i := 0; i < maxFrames; i++ {
		buf, err := readOneTaggedFrame(br)
		if err != nil {
			t.Fatalf("frame %d: readOneTaggedFrame: %v", i, err)
		}
		if buf[0] == B.BufBlockTagScene {
			sceneFrame = buf[1:]
		}
	}
	if sceneFrame == nil {
		t.Fatalf("no scene frame seen within %d frames", maxFrames)
	}

	if dedicated {
		vr := bufio.NewReader(viewRead)
		// Same reasoning as the scene-frame loop above: take the LAST view frame within a
		// bounded read, so the assertion isn't racing SeedInitialViewpoint's real pose.
		for i := 0; i < maxFrames; i++ {
			buf, err := readOneRawFrame(vr)
			if err != nil {
				if i == 0 {
					t.Fatalf("readOneRawFrame (view), frame %d: %v", i, err)
				}
				break // pipe drained (process still running, no more buffered frames) — use what we have.
			}
			viewFrame = buf
		}
	}

	return sceneFrame, viewFrame
}

// TestHeadlessViewFdDedicatedStream proves the PROOF half of the dual path: with the
// view fd wired (WIREFOLD_STREAM_FDS="view:4", mirroring runCommand.ts's spawn), the
// camera+overlay+scene bytes arrive on their OWN dedicated fd-4 frame, and the fd-3
// scene frame's payload length matches the WITHOUT-camera/overlay/scene shape — i.e.
// they are NOT double-sourced from both places.
func TestHeadlessViewFdDedicatedStream(t *testing.T) {
	sceneFrame, viewFrame := runHeadlessViewFdCase(t, true)

	withoutViewLen, withViewLen := sceneFrameViewLens(sceneFrame)
	if len(sceneFrame) != withoutViewLen {
		t.Fatalf("scene frame payload length %d != expected without-view length %d (withViewLen would be %d) — camera/overlay/scene were NOT excluded from the fd-3 scene frame while the dedicated view fd is active", len(sceneFrame), withoutViewLen, withViewLen)
	}

	// The view frame is [tick:u32][Camera][Overlay][Scene][EVENTS section] — assert its
	// total length (accounting for the trailing EVENTS section's own count) and that the
	// Camera row's R column (radius, offset 12 within CAMERA block: px,py,pz,r are float32
	// at 0,4,8,12) is a real non-degenerate value (> 0), the same "no viewpoint yet" guard
	// BufferCamera.tsx uses.
	fixedLen := B.BufViewFrameHeaderSize + B.BufCameraStride + B.BufOverlayStride + B.BufSceneStride
	if len(viewFrame) < fixedLen+4 {
		t.Fatalf("view frame length %d, want >= %d (fixed view blocks + EVENTS count)", len(viewFrame), fixedLen+4)
	}
	eventCount := readU32(viewFrame, fixedLen)
	wantLen := fixedLen + 4 + int(eventCount)*B.BufEventStride
	if len(viewFrame) != wantLen {
		t.Fatalf("view frame length %d != expected %d (%d events)", len(viewFrame), wantLen, eventCount)
	}
	cameraOff := B.BufViewFrameHeaderSize
	r := readF32(viewFrame, cameraOff+12) // CAMERA block's R column offset
	if !(r > 0) {
		t.Fatalf("view frame's camera R = %v, want > 0 (a real seeded viewpoint)", r)
	}
}

// TestHeadlessViewFdFallback proves the FALLBACK half of the dual path
// (feedback_headless_repro_verifies_persistence: real binary, on-wire bytes, strong
// assertions): with WIREFOLD_STREAM_FDS unset (no dedicated view fd — the shape every
// existing headless launch already gets), camera/overlay/scene stay embedded in the
// fd-3 scene frame exactly as before this migration, and it still renders a real
// (non-degenerate) camera pose.
func TestHeadlessViewFdFallback(t *testing.T) {
	sceneFrame, _ := runHeadlessViewFdCase(t, false)

	withoutViewLen, withViewLen := sceneFrameViewLens(sceneFrame)
	if len(sceneFrame) != withViewLen {
		t.Fatalf("scene frame payload length %d != expected with-view (embedded) length %d (withoutViewLen would be %d) — camera/overlay/scene are missing from the fd-3 scene frame despite no dedicated view fd being wired", len(sceneFrame), withViewLen, withoutViewLen)
	}

	// Camera block sits right after the header + LayoutLink block in the embedded
	// layout (see Buffer/pack.go buildSnapshot / snapshot.go's layout comment).
	layoutLinkCount := int(readU32(sceneFrame, 4))
	cameraOff := B.BufHeaderSize + layoutLinkCount*B.BufLayoutLinkStride
	r := readF32(sceneFrame, cameraOff+12)
	if !(r > 0) {
		t.Fatalf("fallback scene frame's embedded camera R = %v, want > 0 (a real seeded viewpoint)", r)
	}
}
