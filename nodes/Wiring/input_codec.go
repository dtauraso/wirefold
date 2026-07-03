// input_codec.go — BINARY decode of the editor→Go input stream.
//
// The TS→Go bridge is a purely BINARY buffer, symmetric with the Go→TS content
// buffer streamed on fd 3. The webview builds a binary RECORD per message and the
// extension host writes each record FRAMED as [len:u32-LE][record] to Go's stdin.
// This file decodes one record (kind byte + fixed numeric fields + length-prefixed
// UTF-8 sections) back into the SAME stdinMsg the old newline-JSON path produced,
// so applyEdit / HandleRawInput / play-pause dispatch is UNCHANGED — only the wire
// decode differs.
//
// The record layout is defined ONCE here and mirrored in
// tools/topology-vscode/src/schema/input-layout.ts. The two sides carry an identical
// InputLayoutFingerprint, enforced by tools/check-input-layout-parity.sh.
//
// Numbers are little-endian (matching the fd-3 content buffer). Enum discriminators
// (event kind, hit kind, update entity kind) are u8 indices into the shared orderings.
// Irreducibly-structural edit payloads (node-move entries, edge-faded map, port-anchor
// keys, overlay state, scene blob) ride as a length-prefixed UTF-8 JSON section — the
// message ENVELOPE/transport is binary; only that leaf CONTENT stays JSON (allowed by
// CLAUDE.md's bridge-surface note for spec-TEXT payloads).

package Wiring

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
)

// InputLayoutFingerprint pins the binary input-record layout. It MUST be byte-identical
// to INPUT_LAYOUT_FINGERPRINT in input-layout.ts (guarded by check-input-layout-parity.sh).
// Bump on both sides whenever any record kind, field, or enum ordering changes.
//
// INPUT_LAYOUT_FINGERPRINT: v1 kinds=resume:1,pause:2,resend:3,raw-input:10,edit-create:20,edit-delete:21,edit-update:22 eventKinds=pointerdown,pointermove,pointerup,wheel,home hitKinds=port,handhold,node,edge,empty updateKinds=node,edge,camera,overlays,scene
const InputLayoutFingerprint = "v1 kinds=resume:1,pause:2,resend:3,raw-input:10,edit-create:20,edit-delete:21,edit-update:22 eventKinds=pointerdown,pointermove,pointerup,wheel,home hitKinds=port,handhold,node,edge,empty updateKinds=node,edge,camera,overlays,scene"

// Record kind bytes (first byte of every record).
const (
	inKindResume     = 1  // play  — resume the clock gate
	inKindPause      = 2  // pause — halt the clock gate
	inKindResend     = 3  // resend — re-emit full geometry
	inKindRawInput   = 10 // raw pointer/wheel/home event
	inKindEditCreate = 20 // edit op=create (2 strings)
	inKindEditDelete = 21 // edit op=delete (2 strings)
	inKindEditUpdate = 22 // edit op=update (kind byte + JSON leaf)
)

// Enum orderings (u8 index → string), shared with input-layout.ts.
var inEventKinds = []string{"pointerdown", "pointermove", "pointerup", "wheel", "home"}
var inHitKinds = []string{"port", "handhold", "node", "edge", "empty"}
var inUpdateKinds = []string{"node", "edge", "camera", "overlays", "scene"}

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
	case inKindResume:
		return stdinMsg{Type: "play"}, true
	case inKindPause:
		return stdinMsg{Type: "pause"}, true
	case inKindResend:
		return stdinMsg{Type: "resend"}, true
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
		kindByte, err := r.u8()
		if err != nil {
			return stdinMsg{}, false
		}
		payload, err := r.str()
		if err != nil {
			return stdinMsg{}, false
		}
		msg := stdinMsg{}
		// The JSON leaf carries the full typed update object (kind/attr + payload
		// fields). Unmarshal it into the union struct, then force Type/Op and the
		// entity Kind from the binary discriminators (authoritative on the wire).
		if err := json.Unmarshal([]byte(payload), &msg); err != nil {
			return stdinMsg{}, false
		}
		msg.Type = "edit"
		msg.Op = "update"
		msg.Kind = enumAt(inUpdateKinds, kindByte)
		return msg, true
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

// encodeControl builds a payload-less control record (play/pause/resend).
func encodeControl(kind byte) []byte { return []byte{kind} }

// encodeEditCreateDelete builds an edit create/delete record.
func encodeEditCreateDelete(kind byte, target, handle string) []byte {
	w := &recWriter{}
	w.u8(kind)
	w.str(target)
	w.str(handle)
	return w.b
}

// encodeEditUpdate builds an edit update record: entity-kind byte + JSON payload leaf.
func encodeEditUpdate(entityKind string, payloadJSON string) []byte {
	w := &recWriter{}
	w.u8(inKindEditUpdate)
	w.u8(enumIndex(inUpdateKinds, entityKind))
	w.str(payloadJSON)
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
	w.f64(ev.Hit.X)
	w.f64(ev.Hit.Y)
	w.f64(ev.Hit.Z)
	return w.b
}

// frameRecord wraps a record body with the [len:u32-LE] transport frame.
func frameRecord(rec []byte) []byte {
	return append(binary.LittleEndian.AppendUint32(nil, uint32(len(rec))), rec...)
}
