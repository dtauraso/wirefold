package main

import (
	"strings"
	"testing"
)

// TestBufTypeTableConsistency is the guard against the real risk: goWriterCall,
// goParamType, tsDataViewGetter, and tsDataViewLE used to be four independent
// switches over the same bufType set. If any one of them disagreed with the
// others about a type's byte width, signedness, or endianness, the Go-emitted
// buffer_layout_gen.go and the TS-emitted buffer-layout.ts would still carry the
// SAME BUF_LAYOUT_FINGERPRINT (it is derived from bufType names/offsets only,
// not from width/getter semantics) while reading DIFFERENT bytes at runtime —
// nothing in the guard suite catches that.
//
// Now all four derive from one bufTypeTable entry, so they cannot disagree by
// construction. This test additionally checks each entry for INTERNAL
// self-consistency: the declared byte size must match both the Go type's known
// width and the TS DataView getter's known width, and the little-endian flag
// must be set iff the type is multi-byte. This catches someone hand-editing one
// field of a bufTypeTable entry (e.g. changing tsGetter to a narrower accessor)
// without updating the others.
func TestBufTypeTableConsistency(t *testing.T) {
	// Known byte widths for every accessor/type name this generator may ever use.
	// Extend these maps, not the switches, when a new width is introduced.
	getterWidth := map[string]int{
		"getFloat32": 4,
		"getInt32":   4,
		"getUint32":  4,
		"getUint8":   1,
		"getInt16":   2,
		"getUint16":  2,
		"getFloat64": 8,
		"getInt8":    1,
	}
	goTypeWidth := map[string]int{
		"float32": 4,
		"int32":   4,
		"uint32":  4,
		"uint8":   1,
		"int8":    1,
		"int16":   2,
		"uint16":  2,
		"float64": 8,
		"byte":    1,
	}

	if len(bufTypeTable) == 0 {
		t.Fatal("bufTypeTable is empty")
	}

	for bufType, info := range bufTypeTable {
		gw, ok := goTypeWidth[info.goType]
		if !ok {
			t.Errorf("bufType %q: goType %q has no known width in this test's goTypeWidth map — add it", bufType, info.goType)
			continue
		}
		if gw != info.size {
			t.Errorf("bufType %q: declared size %d does not match goType %q's known width %d", bufType, info.size, info.goType, gw)
		}

		tw, ok := getterWidth[info.tsGetter]
		if !ok {
			t.Errorf("bufType %q: tsGetter %q has no known width in this test's getterWidth map — add it", bufType, info.tsGetter)
			continue
		}
		if tw != info.size {
			t.Errorf("bufType %q: declared size %d does not match tsGetter %q's known width %d (Go and TS would read different byte counts)", bufType, info.size, info.tsGetter, tw)
		}

		// Endianness only matters for multi-byte values: a single-byte type must
		// not request the LE flag, and a multi-byte type must request it.
		wantLE := info.size > 1
		if info.tsLE != wantLE {
			t.Errorf("bufType %q: tsLE=%v but size=%d (want tsLE=%v)", bufType, info.tsLE, info.size, wantLE)
		}

		if info.goWrite == nil {
			t.Errorf("bufType %q: goWrite is nil", bufType)
			continue
		}
		// Sanity: the emitted Go write statement should mention the value
		// expression it was handed (catches a goWrite closure that ignores its
		// "val" argument entirely).
		stmt := info.goWrite("OFFSET_MARKER", "VAL_MARKER")
		if !strings.Contains(stmt, "OFFSET_MARKER") || !strings.Contains(stmt, "VAL_MARKER") {
			t.Errorf("bufType %q: goWrite(%q, %q) = %q — does not reference both arguments", bufType, "OFFSET_MARKER", "VAL_MARKER", stmt)
		}
	}
}

// TestLookupBufTypeKnowsEveryTableEntry pins that lookupBufType (used by every
// emitter) returns the exact same struct value that's in bufTypeTable — i.e.
// there is truly one path, not a copy that could drift.
func TestLookupBufTypeKnowsEveryTableEntry(t *testing.T) {
	for bufType := range bufTypeTable {
		got := lookupBufType(bufType)
		want := bufTypeTable[bufType]
		if got.size != want.size || got.goType != want.goType || got.tsGetter != want.tsGetter || got.tsLE != want.tsLE {
			t.Errorf("lookupBufType(%q) diverged from bufTypeTable entry", bufType)
		}
	}
}
