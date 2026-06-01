package Wiring

import "testing"

// TestOutGated verifies the node-owned send rule controls Gated():
// the zero value and consumeGated are gated; fireAndForget is not.
func TestOutGated(t *testing.T) {
	cases := []struct {
		name     string
		rule     SendRule
		wantGate bool
	}{
		{"zero value defaults gated", "", true},
		{"consumeGated is gated", RuleConsumeGated, true},
		{"fireAndForget is not gated", RuleFireAndForget, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &Out{Rule: tc.rule}
			if got := o.Gated(); got != tc.wantGate {
				t.Fatalf("Out{Rule:%q}.Gated() = %v, want %v", tc.rule, got, tc.wantGate)
			}
		})
	}

	// Nil-safe: a nil Out is gated.
	var nilOut *Out
	if !nilOut.Gated() {
		t.Fatalf("(*Out)(nil).Gated() = false, want true")
	}
}
