// headless_view_fd_test.go — drives the REAL compiled binary headlessly and proves the
// VIEW stream end-to-end (memory/feedback_no_single_writer_bridge.md, Buffer/stream_fds.go):
// with every per-owner fd wired (WIREFOLD_STREAM_FDS mandatory — the fd-3 SnapshotState
// accumulator + its fallback frame were deleted, per-owner-buffer-rows.md's final step),
// camera/overlay/scene arrive on their OWN dedicated view frame (BuildViewStreamFrame).
//
// See headless_stream_helpers_test.go for the spawn/cleanup pattern this reuses; NEVER run
// the sim in the foreground (memory/feedback_no_foreground_sim_runs.md).
package main

import (
	"os"
	"testing"

	B "github.com/dtauraso/wirefold/Buffer"
)

// TestHeadlessViewFdDedicatedStream proves the view fd (WIREFOLD_STREAM_FDS="view:4",
// mirroring runCommand.ts's spawn) carries camera+overlay+scene bytes: total frame length
// matches the fixed Camera/Overlay/Scene blocks + a trailing EVENTS section, and the Camera
// row's R column is a real, non-degenerate value (> 0) — the same "no viewpoint yet" guard
// BufferCamera.tsx uses.
func TestHeadlessViewFdDedicatedStream(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	binPath := buildHeadlessBinary(t, repoRoot, "wirefold-headless-view-fd-test")
	ds := spawnDedicatedAllStreams(t, binPath, repoRoot)

	viewFrames := readLastFrames(t, []*os.File{ds.viewRead}, "view", 200)
	viewFrame := viewFrames[0]

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
