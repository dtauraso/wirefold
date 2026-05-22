// marshal_contract_test.go — verifies marshalEvent output matches the
// shared fixture used by the TS contract test. A failing test here
// means a JSON key or type changed; fix marshalEvent AND regenerate
// tools/topology-vscode/test/fixtures/trace-events.jsonl together.
package Trace

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fixtureLines(t *testing.T) []string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(here), "..")
	path := filepath.Join(repoRoot, "tools/topology-vscode/test/fixtures/trace-events.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			lines = append(lines, l)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	return lines
}

func TestMarshalEventMatchesFixture(t *testing.T) {
	events := []Event{
		{Step: 0, Kind: KindRecv, Node: "A", Port: "in", Value: 1, hasValue: true},
		{Step: 1, Kind: KindFire, Node: "A"},
		{Step: 2, Kind: KindSend, Node: "A", Port: "out", Value: 1, hasValue: true},
		{Step: 3, Kind: KindSlot, Node: "B", Port: "in", SlotPhase: "filled", Value: 1, hasValue: true},
		{Step: 4, Kind: KindSlot, Node: "B", Port: "in", SlotPhase: "empty", hasValue: false},
	}

	fixture := fixtureLines(t)
	if len(fixture) != len(events) {
		t.Fatalf("fixture has %d lines, want %d", len(fixture), len(events))
	}

	for i, e := range events {
		got, err := marshalEvent(e)
		if err != nil {
			t.Fatalf("event %d: marshalEvent error: %v", i, err)
		}
		if string(got) != fixture[i] {
			t.Errorf("event %d mismatch\n got:  %s\n want: %s", i, got, fixture[i])
		}
	}
}
