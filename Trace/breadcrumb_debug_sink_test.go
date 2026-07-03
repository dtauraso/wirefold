// breadcrumb_debug_sink_test.go — the DEBUG BREADCRUMB channel: SetDebugSink wires a
// production sink (os.Stdout in main) that Breadcrumb() writes to in real time, in
// addition to the optional in-process test sink. These tests prove a breadcrumb
// round-trips as one structured {"kind":"breadcrumb",...} JSONL line, that an unwired
// breadcrumb is a no-op, and that the debug sink is independent of the event sink.
package Trace

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestBreadcrumbWritesToDebugSink(t *testing.T) {
	tr := New(0)
	defer tr.Close()
	var dbg bytes.Buffer
	tr.SetDebugSink(&dbg)

	tr.Breadcrumb("topology-loaded", "node-7", "in", "nodes=3")

	line := strings.TrimSpace(dbg.String())
	if line == "" {
		t.Fatal("debug sink got no breadcrumb line")
	}
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("expected exactly one line, got %q", line)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("breadcrumb line is not valid JSON: %v (%q)", err, line)
	}
	if rec["kind"] != "breadcrumb" {
		t.Fatalf(`expected kind="breadcrumb", got %v`, rec["kind"])
	}
	if rec["label"] != "topology-loaded" {
		t.Fatalf(`expected label="topology-loaded", got %v`, rec["label"])
	}
	if rec["node"] != "node-7" || rec["port"] != "in" || rec["value"] != "nodes=3" {
		t.Fatalf("unexpected breadcrumb fields: %v", rec)
	}
}

func TestBreadcrumbNoSinkIsNoOp(t *testing.T) {
	tr := New(0) // no sink, no debug sink
	defer tr.Close()
	// Must not panic and must record nothing observable; the assertion is simply
	// that Breadcrumb returns cheaply with neither sink wired.
	tr.Breadcrumb("unwired", "", "", "")
}

func TestBreadcrumbHitsBothSinks(t *testing.T) {
	var evSink, dbg bytes.Buffer
	tr := NewWithSink(0, &evSink)
	defer tr.Close()
	tr.SetDebugSink(&dbg)

	tr.Breadcrumb("both", "", "", "")

	if !strings.Contains(evSink.String(), `"label":"both"`) {
		t.Fatalf("event sink missing breadcrumb: %q", evSink.String())
	}
	if !strings.Contains(dbg.String(), `"label":"both"`) {
		t.Fatalf("debug sink missing breadcrumb: %q", dbg.String())
	}
}
