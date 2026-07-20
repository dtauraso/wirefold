// input_codec_test.go — decode/round-trip tests for the binary editor→Go input records.

package Wiring

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestDecodeControlRecords(t *testing.T) {
	cases := []struct {
		kind byte
		want string
	}{
		{inKindSave, "save"},
	}
	for _, c := range cases {
		msg, ok := decodeInputRecord(encodeControl(c.kind))
		if !ok || msg.Type != c.want {
			t.Fatalf("control kind %d → (%q, ok=%v), want %q", c.kind, msg.Type, ok, c.want)
		}
	}
}

func TestDecodeEditUpdateOverlaysToggle(t *testing.T) {
	// Exact bytes: [22][entityKind=0][attr=toggle=0][flagId(tori)=0].
	rec := encodeOverlaysToggle("tori")
	if want := []byte{inKindEditUpdate, 0, inOverlayAttrToggle, 0}; !bytes.Equal(rec, want) {
		t.Fatalf("overlays toggle bytes = %v, want %v", rec, want)
	}
	msg, ok := decodeInputRecord(rec)
	if !ok || msg.Type != "edit" || msg.Op != "update" || msg.Kind != "overlays" || msg.Attr != "toggle" || msg.Flag != "tori" {
		t.Fatalf("overlays toggle decode = %+v ok=%v", msg, ok)
	}
	// A non-tori flag maps by index.
	msg2, _ := decodeInputRecord(encodeOverlaysToggle("overlays"))
	if msg2.Flag != "overlays" {
		t.Fatalf("toggle overlays decode flag=%q", msg2.Flag)
	}
}

// TestOverlayFlagOrderMatchesFingerprint guards that the derived flag order equals the
// fingerprint's overlayFlags list (self-check on parseOverlayFlags).
func TestOverlayFlagOrderMatchesFingerprint(t *testing.T) {
	want := []string{"tori", "scenePoles", "nodePoles", "selSpherePoles", "handholds", "labelsGlobal", "overlays", "doubleLinks"}
	if !reflect.DeepEqual(inOverlayFlags, want) {
		t.Fatalf("inOverlayFlags = %v, want %v", inOverlayFlags, want)
	}
}

func TestDecodeRawInputRoundTrip(t *testing.T) {
	in := rawInputMsg{
		Kind: "wheel", X: 12.5, Y: -3.25, RectLeft: 1, RectTop: 2, RectWidth: 800, RectHeight: 600,
		Button: -1, Ctrl: true, Shift: false, Alt: true, Meta: false,
		DeltaX: 4, DeltaY: -8, Fov: 50,
		Hit: rawHit{Kind: "node", IsInput: true, NodeRow: 7, PortRow: -1, EdgeRow: -1, X: 1.5, Y: 2.5, Z: 3.5},
	}
	msg, ok := decodeInputRecord(encodeRawInput(in))
	if !ok || msg.Type != "raw-input" || msg.Event == nil {
		t.Fatalf("raw-input decode failed: ok=%v msg=%+v", ok, msg)
	}
	if !reflect.DeepEqual(*msg.Event, in) {
		t.Fatalf("raw-input round-trip mismatch:\n got  %+v\n want %+v", *msg.Event, in)
	}
}

func TestDecodeTruncatedAndUnknown(t *testing.T) {
	if _, ok := decodeInputRecord(nil); ok {
		t.Fatal("empty record should not decode")
	}
	if _, ok := decodeInputRecord([]byte{99}); ok {
		t.Fatal("unknown kind byte should not decode")
	}
	// A truncated overlays-toggle record must be rejected, not panic.
	rec := encodeOverlaysToggle("tori")
	if _, ok := decodeInputRecord(rec[:len(rec)-1]); ok {
		t.Fatal("truncated update record should not decode")
	}
}

// TestSavePersistsCurrentOverlayState applies an overlays TOGGLE edit (flipping Go's held
// state), then a save, and asserts scene.json reflects the CURRENT (post-edit) state — not a
// stale/empty snapshot. This is the "Go persists its own current topology" guarantee.
func TestSavePersistsCurrentOverlayState(t *testing.T) {
	root := t.TempDir()
	md := newMoveDispatch(map[string]nodeGeom{}, map[string]EdgeEndpoints{}, nil, nil, nil, NewRealClock(), nil)
	// overlaysVisible defaults true; toggle flips it to false.
	toggle, ok := decodeInputRecord(encodeOverlaysToggle("overlays"))
	if !ok {
		t.Fatal("decode toggle failed")
	}
	applyEdit(toggle, md, nil, nil)
	if err := writeSceneOverlays(sceneCameraPath(root), md.ov); err != nil {
		t.Fatalf("writeSceneOverlays: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "view", "scene.json"))
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("scene.json invalid: %v", err)
	}
	if string(obj["overlaysActive"]) != "false" {
		t.Fatalf("overlaysActive=%s want false (toggled-off state should persist)", obj["overlaysActive"])
	}
}

// TestFramedPartialReads feeds a framed record ONE BYTE AT A TIME through a pipe and
// asserts the reader reassembles the frame and applies its side effect (a save writes
// scene.json).
func TestFramedPartialReads(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, pw := io.Pipe()
	// A real (empty) dispatch so the `save` command has an overlay snapshot to persist.
	md := newMoveDispatch(map[string]nodeGeom{}, map[string]EdgeEndpoints{}, nil, nil, nil, NewRealClock(), nil)
	md.EnableEditPersist(root) // arms overlaysPersist so `save` can write scene.json
	readerDone := make(chan struct{})
	go func() {
		RunStdinReader(ctx, pr, SlotRegistry{}, md, nil, nil)
		close(readerDone)
	}()
	// RunStdinReader now flushes pending debounced persisters (writes under root) on its
	// own clean-shutdown return path. Wait for that goroutine to actually finish before this
	// test's t.TempDir() cleanup removes root, or the flush can race the RemoveAll.
	defer func() { <-readerDone }()

	frame := frameRecord(encodeControl(inKindSave))
	go func() {
		for _, b := range frame {
			pw.Write([]byte{b})
			time.Sleep(100 * time.Microsecond)
		}
	}()
	scenePath := filepath.Join(root, "view", "scene.json")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(scenePath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("partial-read frame never dispatched (scene.json not written)")
		}
		time.Sleep(2 * time.Millisecond)
	}
	pw.Close()
}

// TestStdinReaderCancelWithoutEOF asserts the background frame-reader goroutine unwinds
// on ctx-cancel even when the pipe write end stays open (no EOF/close from the writer
// side). Before the close-on-cancel fix, the reader goroutine stayed parked in
// io.ReadFull forever once RunStdinReader itself returned via <-done — a goroutine leak
// for any in-process caller that cancels without closing r (production relies on process
// exit to reclaim it, so this went unnoticed there).
func TestStdinReaderCancelWithoutEOF(t *testing.T) {
	// Let any goroutines from earlier tests/GC settle so the baseline below is stable.
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	base := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	defer pw.Close()

	readerDone := make(chan struct{})
	go func() {
		RunStdinReader(ctx, pr, SlotRegistry{}, nil, nil, nil)
		close(readerDone)
	}()

	cancel()

	// RunStdinReader's own select loop exits promptly on cancel; wait for that first so
	// this test's timing budget below is spent solely on the background frame-reader
	// goroutine's unwind, which is what the fix targets.
	select {
	case <-readerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("RunStdinReader did not return on ctx cancel")
	}

	// The background frame-reader goroutine has no direct completion signal exposed by
	// RunStdinReader, so drive it out indirectly: it exits by unblocking io.ReadFull once
	// r is closed. Detect completion via runtime.NumGoroutine returning to (at most) the
	// pre-test baseline, bounded by a short deadline — this times out before the fix
	// (goroutine stays parked forever) and passes after.
	deadline := time.Now().Add(500 * time.Millisecond)
	settled := false
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= base {
			settled = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !settled {
		t.Fatalf("background frame-reader goroutine still parked after ctx cancel (goroutine leak); NumGoroutine=%d base=%d", runtime.NumGoroutine(), base)
	}
}

// TestFrameLenPrefix documents the transport frame is [len:u32-LE][record].
func TestFrameLenPrefix(t *testing.T) {
	rec := encodeControl(inKindSave)
	frame := frameRecord(rec)
	if got := binary.LittleEndian.Uint32(frame[:4]); int(got) != len(rec) {
		t.Fatalf("frame length prefix = %d, want %d", got, len(rec))
	}
	if !bytes.Equal(frame[4:], rec) {
		t.Fatal("frame body != record")
	}
}
