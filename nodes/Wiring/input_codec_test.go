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
	"testing"
	"time"
)

func TestDecodeControlRecords(t *testing.T) {
	cases := []struct {
		kind byte
		want string
	}{
		{inKindResume, "play"},
		{inKindPause, "pause"},
		{inKindResend, "resend"},
		{inKindSave, "save"},
	}
	for _, c := range cases {
		msg, ok := decodeInputRecord(encodeControl(c.kind))
		if !ok || msg.Type != c.want {
			t.Fatalf("control kind %d → (%q, ok=%v), want %q", c.kind, msg.Type, ok, c.want)
		}
	}
}

func TestDecodeEditCreateDelete(t *testing.T) {
	msg, ok := decodeInputRecord(encodeEditCreateDelete(inKindEditCreate, "nodeA", "in"))
	if !ok || msg.Type != "edit" || msg.Op != "create" || msg.Target != "nodeA" || msg.TargetHandle != "in" {
		t.Fatalf("create decode = %+v ok=%v", msg, ok)
	}
	msg, ok = decodeInputRecord(encodeEditCreateDelete(inKindEditDelete, "nÖde", "port:β"))
	if !ok || msg.Op != "delete" || msg.Target != "nÖde" || msg.TargetHandle != "port:β" {
		t.Fatalf("delete decode (utf8) = %+v ok=%v", msg, ok)
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
	msg2, _ := decodeInputRecord(encodeOverlaysToggle("doubleLinks"))
	if msg2.Flag != "doubleLinks" {
		t.Fatalf("toggle doubleLinks decode flag=%q", msg2.Flag)
	}
}

func TestDecodeEditUpdateOverlaysSet(t *testing.T) {
	// Bit i (LSB-first) = inOverlayFlags[i] visibility. Set only tori(0) + overlays(8).
	bits := uint16(1<<0 | 1<<8)
	rec := encodeOverlaysSet(bits)
	if want := []byte{inKindEditUpdate, 0, inOverlayAttrSet, 0x01, 0x01}; !bytes.Equal(rec, want) {
		t.Fatalf("overlays set bytes = %v, want %v", rec, want)
	}
	msg, ok := decodeInputRecord(rec)
	if !ok || msg.Type != "edit" || msg.Op != "update" || msg.Kind != "overlays" || msg.Attr != "set" || msg.State == nil {
		t.Fatalf("overlays set decode = %+v ok=%v", msg, ok)
	}
	if !msg.State.Tori || !msg.State.Overlays || msg.State.ScenePoles || msg.State.DoubleLinks {
		t.Fatalf("overlays set bitfield mismatch: %+v", *msg.State)
	}
}

// TestOverlayFlagOrderMatchesFingerprint guards that the derived flag order equals the
// fingerprint's overlayFlags list (self-check on parseOverlayFlags).
func TestOverlayFlagOrderMatchesFingerprint(t *testing.T) {
	want := []string{"tori", "scenePoles", "nodePoles", "angleLabels", "selSpherePoles", "handholds", "labelsGlobal", "badgesGlobal", "overlays", "doubleLinks"}
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
	// A create record missing its second string must be rejected, not panic.
	rec := encodeEditCreateDelete(inKindEditCreate, "nodeA", "in")
	if _, ok := decodeInputRecord(rec[:len(rec)-3]); ok {
		t.Fatal("truncated create record should not decode")
	}
}

// TestSavePersistsCurrentOverlayState applies an overlays TOGGLE edit (flipping Go's held
// state), then a save, and asserts scene.json reflects the CURRENT (post-edit) state — not a
// stale/empty snapshot. This is the "Go persists its own current topology" guarantee.
func TestSavePersistsCurrentOverlayState(t *testing.T) {
	root := t.TempDir()
	md := newMoveDispatch(map[string]nodeGeom{}, map[string]EdgeEndpoints{}, nil)
	// overlaysVisible defaults true; toggle flips it to false.
	toggle, ok := decodeInputRecord(encodeOverlaysToggle("overlays"))
	if !ok {
		t.Fatal("decode toggle failed")
	}
	applyEdit(toggle, SlotRegistry{}, md, nil, root)
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
	md := newMoveDispatch(map[string]nodeGeom{}, map[string]EdgeEndpoints{}, nil)
	md.EnableEditPersist(root) // arms overlaysPersist so `save` can write scene.json
	go RunStdinReader(ctx, pr, SlotRegistry{}, md, nil, nil, root)

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

// TestFrameLenPrefix documents the transport frame is [len:u32-LE][record].
func TestFrameLenPrefix(t *testing.T) {
	rec := encodeControl(inKindResume)
	frame := frameRecord(rec)
	if got := binary.LittleEndian.Uint32(frame[:4]); int(got) != len(rec) {
		t.Fatalf("frame length prefix = %d, want %d", got, len(rec))
	}
	if !bytes.Equal(frame[4:], rec) {
		t.Fatal("frame body != record")
	}
}
