// input_codec.go — BINARY decode of the editor→Go input stream.
//
// The TS→Go bridge is a purely BINARY buffer, symmetric with the Go→TS content
// buffer streamed on fd 3. The webview builds a binary RECORD per message and the
// extension host writes each record FRAMED as [len:u32-LE][record] to Go's stdin.
// This file decodes one record (kind byte + fixed numeric fields + length-prefixed
// UTF-8 sections) back into the SAME stdinMsg the old newline-JSON path produced,
// so applyEdit / HandleRawInput dispatch is UNCHANGED — only the wire
// decode differs.
//
// The record layout is defined ONCE here and mirrored in
// tools/topology-vscode/src/schema/input-layout.ts. The two sides carry an identical
// InputLayoutFingerprint, enforced by tools/check-input-layout-parity.sh.
//
// Numbers are little-endian (matching the fd-3 content buffer). Enum discriminators
// (event kind, hit kind, update entity kind, update attr, overlay flag) are u8 indices
// into the shared orderings. There is NO JSON on the wire: every record is fully numeric.
// The live editor→Go traffic is raw-input, overlays toggle (numeric flag-id), and
// the bare `save` COMMAND (Go persists its OWN authoritative scene state).
// create/delete/edit-update record kinds stay defined (the
// 3-op create/update/delete concept), though the gesture FSM now produces edge create/delete
// in-process from raw-input.
//
// Kind 3 was inKindResend (removed: the ext host now caches the last fd3 snapshot and
// replays it on webview "ready" instead of asking Go to re-emit geometry — see
// runCommand.ts's BuildAndRunRunner.lastSnapshot/getLastSnapshot). Left as an intentional
// GAP rather than renumbered, so no other kind's wire value moves.

package Wiring

import (
	"encoding/binary"
	"errors"
	"math"
	"strings"
)

// InputLayoutFingerprint pins the binary input-record layout. It MUST be byte-identical
// to INPUT_LAYOUT_FINGERPRINT in input-layout.ts (guarded by check-input-layout-parity.sh).
// Bump on both sides whenever any record kind, field, or enum ordering changes.
//
// INPUT_LAYOUT_FINGERPRINT: v16 kinds=save:4,raw-input:10,edit-create:20,edit-delete:21,edit-update:22 eventKinds=pointerdown,pointermove,pointerup,wheel,home hitKinds=port,handhold,node,edge,torus,empty updateKinds=overlays updateAttrs=toggle overlayFlags=tori,scenePoles,nodePoles,selSpherePoles,handholds,labelsGlobal,overlays,doubleLinks
const InputLayoutFingerprint = "v16 kinds=save:4,raw-input:10,edit-create:20,edit-delete:21,edit-update:22 eventKinds=pointerdown,pointermove,pointerup,wheel,home hitKinds=port,handhold,node,edge,torus,empty updateKinds=overlays updateAttrs=toggle overlayFlags=tori,scenePoles,nodePoles,selSpherePoles,handholds,labelsGlobal,overlays,doubleLinks"

// Record kind bytes (first byte of every record).
const (
	// Kinds 1 (resume) and 2 (pause) removed — the play/pause clock gate was deleted
	// end-to-end. Intentional gaps, per house style (never renumber a live wire value).
	// Kind 3 (inKindResend) removed — intentional gap, see comment above.
	inKindSave = 4 // save  — Go persists its OWN scene state (bare command)
	// Kind 5 (inKindFadeToggle) removed — the fade feature was deleted end-to-end.
	inKindRawInput   = 10 // raw pointer/wheel/home event
	inKindEditCreate = 20 // edit op=create (2 strings)
	inKindEditDelete = 21 // edit op=delete (2 strings)
	inKindEditUpdate = 22 // edit op=update (entity byte + attr byte + numeric payload)
)

// Update attr indices (must match IN_UPDATE_ATTRS ordering in input-layout.ts).
const (
	inOverlayAttrToggle = 0
)

// Enum orderings (u8 index → string), shared with input-layout.ts. Every one is DERIVED
// from its token in the fingerprint, so a Go-side ordering CANNOT drift from the pinned
// layout: there is no second array to reorder. The chain that keeps both languages in
// lockstep, per enum:
//
//	TS array (the source)  →[vitest pins the token to the array]→  TS fingerprint
//	   →[check-input-layout-parity.sh diffs the two strings]→  Go fingerprint
//	   →[parseFPList, here]→  Go array
//
// Every link is checked, so the loop is closed. These orderings are WIRE INDICES (a u8
// index is all that crosses the bridge), so an unchecked reorder is a silent
// mis-dispatch — a raycast hit on a node decoding as an edge — with nothing to fail.
// overlayFlags derived this way already; the other three were hand-synced literals whose
// ends of the chain dangled.
var (
	inEventKinds   = parseFPList(InputLayoutFingerprint, "eventKinds=")
	inHitKinds     = parseFPList(InputLayoutFingerprint, "hitKinds=")
	inUpdateKinds  = parseFPList(InputLayoutFingerprint, "updateKinds=")
	inOverlayFlags = parseFPList(InputLayoutFingerprint, "overlayFlags=")
)

// init fails the process at STARTUP if any enum token is missing from the fingerprint.
// Without this a typo'd/renamed token yields a nil list, enumAt returns "", and every
// record carrying that enum is rejected at runtime — a live input bridge that silently
// does nothing. A malformed layout is a build/boot error, not a quiet degradation
// (mirrors gen-node-defs: a malformed wire prop tag is an error, not a silent skip).
func init() {
	for _, e := range []struct {
		marker string
		list   []string
	}{
		{"eventKinds=", inEventKinds},
		{"hitKinds=", inHitKinds},
		{"updateKinds=", inUpdateKinds},
		{"overlayFlags=", inOverlayFlags},
	} {
		if len(e.list) == 0 {
			panic("input_codec: INPUT_LAYOUT_FINGERPRINT is missing the " + e.marker + " token — the wire enum orderings derive from it")
		}
	}
}

// parseFPList extracts one space-delimited, comma-separated enum token from the
// fingerprint (e.g. marker "hitKinds=" → ["port","handhold",...]). Returns nil if the
// marker is absent; init() above turns that into a startup panic.
func parseFPList(fp, marker string) []string {
	i := strings.Index(fp, marker)
	if i < 0 {
		return nil
	}
	rest := fp[i+len(marker):]
	if sp := strings.IndexByte(rest, ' '); sp >= 0 {
		rest = rest[:sp]
	}
	return strings.Split(rest, ",")
}

var errShortRecord = errors.New("input record truncated")

// recReader is a little-endian cursor over one deframed record body.
type recReader struct {
	b   []byte
	pos int
}

func (r *recReader) u8() (byte, error) {
	if r.pos+1 > len(r.b) {
		return 0, errShortRecord
	}
	v := r.b[r.pos]
	r.pos++
	return v, nil
}

func (r *recReader) i32() (int32, error) {
	if r.pos+4 > len(r.b) {
		return 0, errShortRecord
	}
	v := int32(binary.LittleEndian.Uint32(r.b[r.pos:]))
	r.pos += 4
	return v, nil
}

func (r *recReader) u32() (uint32, error) {
	if r.pos+4 > len(r.b) {
		return 0, errShortRecord
	}
	v := binary.LittleEndian.Uint32(r.b[r.pos:])
	r.pos += 4
	return v, nil
}

func (r *recReader) f64() (float64, error) {
	if r.pos+8 > len(r.b) {
		return 0, errShortRecord
	}
	v := math.Float64frombits(binary.LittleEndian.Uint64(r.b[r.pos:]))
	r.pos += 8
	return v, nil
}

func (r *recReader) str() (string, error) {
	n, err := r.u32()
	if err != nil {
		return "", err
	}
	if r.pos+int(n) > len(r.b) {
		return "", errShortRecord
	}
	s := string(r.b[r.pos : r.pos+int(n)])
	r.pos += int(n)
	return s, nil
}

func (r *recReader) boolByte() (bool, error) {
	v, err := r.u8()
	return v != 0, err
}

func enumAt(list []string, i byte) string {
	if int(i) < len(list) {
		return list[i]
	}
	return ""
}

// decodeInputRecord decodes one deframed record body (WITHOUT the [len] frame) into a
// stdinMsg. ok=false means the record was malformed/unknown and must be ignored
// (forward-compatible; mirrors the old json.Unmarshal-error `continue`).
func decodeInputRecord(rec []byte) (stdinMsg, bool) {
	if len(rec) == 0 {
		return stdinMsg{}, false
	}
	r := &recReader{b: rec, pos: 1}
	switch rec[0] {
	case inKindSave:
		return stdinMsg{Type: "save"}, true
	case inKindRawInput:
		ev, ok := decodeRawInput(r)
		if !ok {
			return stdinMsg{}, false
		}
		return stdinMsg{Type: "raw-input", Event: &ev}, true
	case inKindEditCreate, inKindEditDelete:
		target, err1 := r.str()
		handle, err2 := r.str()
		if err1 != nil || err2 != nil {
			return stdinMsg{}, false
		}
		op := "create"
		if rec[0] == inKindEditDelete {
			op = "delete"
		}
		return stdinMsg{
			Type:             "edit",
			Op:               op,
			stdinCRUDPayload: stdinCRUDPayload{Target: target, TargetHandle: handle},
		}, true
	case inKindEditUpdate:
		// [entityKind][attr][numeric payload]. entity="overlays" (attr toggle, u8 flag-id).
		kindByte, err1 := r.u8()
		if err1 != nil {
			return stdinMsg{}, false
		}
		entity := enumAt(inUpdateKinds, kindByte)
		if entity != "overlays" {
			return stdinMsg{}, false
		}
		attr, err2 := r.u8()
		if err2 != nil {
			return stdinMsg{}, false
		}
		msg := stdinMsg{Type: "edit", Op: "update", Kind: "overlays"}
		switch attr {
		case inOverlayAttrToggle:
			flagID, err := r.u8()
			if err != nil || int(flagID) >= len(inOverlayFlags) {
				return stdinMsg{}, false
			}
			msg.Attr = "toggle"
			msg.Flag = inOverlayFlags[flagID]
			return msg, true
		}
		return stdinMsg{}, false
	}
	return stdinMsg{}, false
}

func decodeRawInput(r *recReader) (rawInputMsg, bool) {
	var ev rawInputMsg
	var e error
	f := func() float64 {
		v, err := r.f64()
		if err != nil && e == nil {
			e = err
		}
		return v
	}
	i := func() int {
		v, err := r.i32()
		if err != nil && e == nil {
			e = err
		}
		return int(v)
	}
	b := func() bool {
		v, err := r.boolByte()
		if err != nil && e == nil {
			e = err
		}
		return v
	}
	u := func() byte {
		v, err := r.u8()
		if err != nil && e == nil {
			e = err
		}
		return v
	}

	ev.Kind = enumAt(inEventKinds, u())
	ev.X = f()
	ev.Y = f()
	ev.RectLeft = f()
	ev.RectTop = f()
	ev.RectWidth = f()
	ev.RectHeight = f()
	ev.Button = i()
	ev.Ctrl = b()
	ev.Shift = b()
	ev.Alt = b()
	ev.Meta = b()
	ev.DeltaX = f()
	ev.DeltaY = f()
	ev.Fov = f()
	ev.Hit.Kind = enumAt(inHitKinds, u())
	ev.Hit.IsInput = b()
	ev.Hit.NodeRow = i()
	ev.Hit.PortRow = i()
	ev.Hit.EdgeRow = i()
	ev.Hit.HandholdTerm = i()
	ev.Hit.X = f()
	ev.Hit.Y = f()
	ev.Hit.Z = f()
	if e != nil || ev.Kind == "" || ev.Hit.Kind == "" {
		return ev, false
	}
	return ev, true
}

// --- Encoder (used by Go unit tests; the production encoder is input-layout.ts) ------

type recWriter struct{ b []byte }

func (w *recWriter) u8(v byte)     { w.b = append(w.b, v) }
func (w *recWriter) i32(v int32)   { w.b = binary.LittleEndian.AppendUint32(w.b, uint32(v)) }
func (w *recWriter) f64(v float64) { w.b = binary.LittleEndian.AppendUint64(w.b, math.Float64bits(v)) }
func (w *recWriter) str(s string) {
	w.b = binary.LittleEndian.AppendUint32(w.b, uint32(len(s)))
	w.b = append(w.b, s...)
}
func (w *recWriter) boolByte(v bool) {
	if v {
		w.u8(1)
	} else {
		w.u8(0)
	}
}

func enumIndex(list []string, s string) byte {
	for i, v := range list {
		if v == s {
			return byte(i)
		}
	}
	return 0
}

// encodeControl builds a payload-less control record (save).
func encodeControl(kind byte) []byte { return []byte{kind} }

// encodeEditCreateDelete builds an edit create/delete record.
func encodeEditCreateDelete(kind byte, target, handle string) []byte {
	w := &recWriter{}
	w.u8(kind)
	w.str(target)
	w.str(handle)
	return w.b
}

// encodeOverlaysToggle builds an overlays TOGGLE record (test helper).
func encodeOverlaysToggle(flag string) []byte {
	w := &recWriter{}
	w.u8(inKindEditUpdate)
	w.u8(enumIndex(inUpdateKinds, "overlays"))
	w.u8(inOverlayAttrToggle)
	w.u8(enumIndex(inOverlayFlags, flag))
	return w.b
}

// encodeRawInput builds a raw-input record from a rawInputMsg (test helper).
func encodeRawInput(ev rawInputMsg) []byte {
	w := &recWriter{}
	w.u8(inKindRawInput)
	w.u8(enumIndex(inEventKinds, ev.Kind))
	w.f64(ev.X)
	w.f64(ev.Y)
	w.f64(ev.RectLeft)
	w.f64(ev.RectTop)
	w.f64(ev.RectWidth)
	w.f64(ev.RectHeight)
	w.i32(int32(ev.Button))
	w.boolByte(ev.Ctrl)
	w.boolByte(ev.Shift)
	w.boolByte(ev.Alt)
	w.boolByte(ev.Meta)
	w.f64(ev.DeltaX)
	w.f64(ev.DeltaY)
	w.f64(ev.Fov)
	w.u8(enumIndex(inHitKinds, ev.Hit.Kind))
	w.boolByte(ev.Hit.IsInput)
	w.i32(int32(ev.Hit.NodeRow))
	w.i32(int32(ev.Hit.PortRow))
	w.i32(int32(ev.Hit.EdgeRow))
	w.i32(int32(ev.Hit.HandholdTerm))
	w.f64(ev.Hit.X)
	w.f64(ev.Hit.Y)
	w.f64(ev.Hit.Z)
	return w.b
}

// frameRecord wraps a record body with the [len:u32-LE] transport frame.
func frameRecord(rec []byte) []byte {
	return append(binary.LittleEndian.AppendUint32(nil, uint32(len(rec))), rec...)
}
