// Buffer/node_kind_id_test.go — NodeKindID mapping test. Split off from the deleted
// Buffer/snapshot_test.go (which exercised it through the now-deleted SnapshotState
// accumulator/buildSnapshot path — memory/feedback_no_single_writer_bridge.md's final step): NodeKindID
// itself is a generated, standalone lookup (node_kind_id_gen.go) with no accumulator
// dependency, so it keeps direct coverage here.

package Buffer

import "testing"

func TestNodeKindIDRoundTrip(t *testing.T) {
	// Verify the index produced by NodeKindID matches the known alphabetical order
	// (NODE_DEFS_ARRAY order in node-defs.ts).
	want := map[string]uint8{
		"Hold":                      0,
		"HoldFlip":                  1,
		"HoldNewSendOld":            2,
		"Input":                     3,
		"Pacer":                     4,
		"Pulse":                     5,
		"WindowAndInhibitLeftGate":  6,
		"WindowAndInhibitRightGate": 7,
	}
	for kind, wantID := range want {
		if got := NodeKindID(kind); got != wantID {
			t.Errorf("NodeKindID(%q) = %d, want %d", kind, got, wantID)
		}
	}
	if got := NodeKindID("UnknownKind"); got != KindIDUnknown {
		t.Errorf("NodeKindID(%q) = %d, want KindIDUnknown (%d)", "UnknownKind", got, KindIDUnknown)
	}
}
