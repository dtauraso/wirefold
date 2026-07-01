package Wiring

import (
	"testing"
)

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

// TestParseSendRule verifies ParseSendRule accepts valid inputs and rejects typos.
func TestParseSendRule(t *testing.T) {
	okCases := []struct {
		input string
		want  SendRule
	}{
		{"", RuleConsumeGated},
		{"consumeGated", RuleConsumeGated},
		{"fireAndForget", RuleFireAndForget},
	}
	for _, tc := range okCases {
		got, err := ParseSendRule(tc.input)
		if err != nil {
			t.Errorf("ParseSendRule(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseSendRule(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}

	badCases := []string{"consumegated", "fireandforget", "typo", "ConsumeGated", "FireAndForget"}
	for _, raw := range badCases {
		if _, err := ParseSendRule(raw); err == nil {
			t.Errorf("ParseSendRule(%q): expected error, got nil", raw)
		}
	}
}

// TestValidateSpecSendRule verifies that validateSpec rejects invalid sendRules
// values and accepts valid/absent ones.
func TestValidateSpecSendRule(t *testing.T) {
	// helper: minimal valid spec with one node of a known kind.
	// We'll use the "Input" kind which exists in all topologies.
	// Instead of depending on a real kind, build a spec that only exercises
	// the sendRules check (Check 4); other checks may emit their own errors
	// but we only care about the sendRules error being present/absent.

	// Invalid sendRule value: expect an error containing the bad value.
	badSpec := &topoSpec{
		Nodes: []specNode{
			{
				ID:   "n1",
				Type: "", // unknown kind — Check 1 fires but we still reach Check 4
				Data: &NodeData{
					SendRules: map[string]string{
						"out": "typo_value",
					},
				},
			},
		},
	}
	err := validateSpec(badSpec)
	if err == nil {
		t.Fatal("validateSpec with bad sendRule: expected error, got nil")
	}
	if !containsStr(err.Error(), "typo_value") {
		t.Errorf("error should name the bad value; got: %v", err)
	}

	// Valid sendRule value: error should NOT mention the sendRules key.
	goodSpec := &topoSpec{
		Nodes: []specNode{
			{
				ID:   "n1",
				Type: "",
				Data: &NodeData{
					SendRules: map[string]string{
						"out": "fireAndForget",
					},
				},
			},
		},
	}
	err = validateSpec(goodSpec)
	// May have errors for unknown kind etc., but NOT for sendRules.
	if err != nil && containsStr(err.Error(), "sendRule") {
		t.Errorf("valid sendRule should not produce a sendRule error; got: %v", err)
	}

	// Missing sendRules map: no sendRules error.
	emptySpec := &topoSpec{
		Nodes: []specNode{
			{ID: "n1", Type: "", Data: &NodeData{}},
		},
	}
	err = validateSpec(emptySpec)
	if err != nil && containsStr(err.Error(), "sendRule") {
		t.Errorf("absent sendRules should not produce a sendRule error; got: %v", err)
	}
}

// TestValidateSpecDuplicateNodeID verifies validateSpec rejects two nodes sharing
// an id (which would otherwise silently last-wins the kind map).
func TestValidateSpecDuplicateNodeID(t *testing.T) {
	dup := &topoSpec{
		Nodes: []specNode{
			{ID: "n1", Type: "", Data: &NodeData{}},
			{ID: "n1", Type: "", Data: &NodeData{}},
		},
	}
	err := validateSpec(dup)
	if err == nil {
		t.Fatal("validateSpec with duplicate node id: expected error, got nil")
	}
	if !containsStr(err.Error(), "duplicate node id") {
		t.Errorf("error should flag the duplicate id; got: %v", err)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
