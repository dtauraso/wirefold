// testutil_test.go — shared test helpers for package main's headless tests.
package main

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	W "github.com/dtauraso/wirefold/nodes/Wiring"
)

// writeTopo writes body to a temp topo.json file and returns its path.
func writeTopo(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write topo: %v", err)
	}
	return path
}

// captureSink is a concurrent-safe io.Writer that accumulates the trace JSONL
// stream so a test can poll for an event while the net runs.
type captureSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *captureSink) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *captureSink) contains(sub string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return bytes.Contains(c.buf.Bytes(), []byte(sub))
}

func (c *captureSink) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// hopTicks converts a per-edge in-flight time (ms) to the tick step that clears
// one hop, plus a small margin: ticksToCross = simLatencyMs / MsPerTick.
func hopTicks(simLatencyMs float64) int64 {
	return int64(math.Ceil(simLatencyMs/float64(W.MsPerTick))) + 2
}

// stepUntilSeen advances clk by stepTicks up to maxSteps times, polling sink for
// want between each advance. Returns true when want appears in sink, false if exhausted.
func stepUntilSeen(clk *W.FakeClock, sink *captureSink, stepTicks int64, want string) bool {
	const maxSteps = 200
	for i := 0; i < maxSteps; i++ {
		clk.AdvanceTicks(stepTicks)
		deadline := time.Now().Add(50 * time.Millisecond)
		for time.Now().Before(deadline) {
			if sink.contains(want) {
				return true
			}
			time.Sleep(time.Millisecond)
		}
	}
	return false
}
