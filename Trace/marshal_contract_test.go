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
		{Step: 0, Kind: KindRecv, Node: "A", Port: "in", Value: 1},
		{Step: 1, Kind: KindFire, Node: "A"},
		{Step: 2, Kind: KindSend, Node: "A", Port: "out", Value: 1},
		{Step: 3, Kind: KindDone, Node: "A", Port: "out"},
		{Step: 4, Kind: KindPosition, Node: "A", Port: "out", Value: 1, X: 1.5, Y: 2.5, Z: 0, hasPos: true, F: 0.5},
		{Step: 5, Kind: KindGeometry, Edge: "AtoB", SX: 0, SY: 0, SZ: 0, EX: 2, EY: 0, EZ: 0},
		{Step: 6, Kind: KindPulseCancelled, Node: "A", Port: "out", Value: 1},
		{Step: 7, Kind: KindNodeGeometry, Node: "A", NX: 1.5, NY: -2.5, NZ: 0, Radius: 15,
			VRX: 0, VRY: 0, VRZ: 1, FRX: 0, FRY: 1, FRZ: 0,
			Ports: []PortGeom{
				{Name: "in", IsInput: true, PX: 1.0, PY: -2.5, PZ: 0, DX: -1, DY: 0, DZ: 0},
				{Name: "out", IsInput: false, PX: 2.0, PY: -2.5, PZ: 0, DX: 1, DY: 0, DZ: 0},
			}},
		{Step: 8, Kind: KindArrive, Node: "A", Port: "out", Value: 1},
		{Step: 9, Kind: KindNodeBead, Node: "A", Row: 1, Col: 0, Present: true, Value: 1, X: 4.5, Y: -6.5, Z: 0, hasPos: true},
		{Step: 10, Kind: KindCamera, PX: 0, PY: 0, PZ: 0, R: 30, PosTheta: 1.2, PosPhi: 0.5, UpTheta: 0, UpPhi: 0},
		{Step: 11, Kind: KindSceneTori, Visible: true},
		{Step: 12, Kind: KindScenePoles, Visible: false},
		{Step: 13, Kind: KindNodePoles, Visible: true},
		{Step: 14, Kind: KindAngleLabels, Visible: false},
		{Step: 15, Kind: KindSelSpherePoles, Visible: true},
		{Step: 16, Kind: KindHandholds, Visible: false},
		{Step: 17, Kind: KindLabelsGlobal, Visible: true},
		{Step: 18, Kind: KindBadgesGlobal, Visible: false},
		{Step: 19, Kind: KindOverlaysVis, Visible: true},
		{Step: 20, Kind: KindDoubleLinks, Visible: false},
		{Step: 21, Kind: KindSelect, Node: "A"},
		{Step: 22, Kind: KindFade, FadedNodes: []string{"A"}, FadedEdges: []string{"e1"}},
		{Step: 23, Kind: KindHover, Node: "A"},
		{Step: 24, Kind: KindRuleBuilder, RuleCenter: "C", RuleHasPending: true, RulePendingCode: 2, RuleTerms: []RuleTermPayload{{Node: "A", Code: 0}}},
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
