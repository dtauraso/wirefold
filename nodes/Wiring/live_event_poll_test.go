// live_event_poll_test.go — shared helper for tests that need to POLL a live
// Trace's events while node goroutines are still running (i.e. before Close()).
//
// Trace.Events() is documented as drain-only: safe to read once the drain
// goroutine has exited (Close()), racy before that (Trace/drain.go). That used
// to be masked by Trace.mu; with the lock removed (docs/planning/visual-editor/
// close-everything.md), a live read of Events() is a real, race-detector-visible
// bug. Tests that must observe events while the trace is still live (waiting for
// an async condition, e.g. "has node X fired yet") need their own
// thread-safe collector instead — built here on Trace's onEvent hook
// (NewWithSinkHook), which the drain goroutine already calls for every event on
// its own goroutine. This collector adds its own mutex around a local slice, so
// polling it from the test goroutine is safe without touching Trace's internals.
package Wiring_test

import (
	"sync"

	T "github.com/dtauraso/wirefold/Trace"
)

// liveEventCollector is a thread-safe sink for events recorded before Close().
type liveEventCollector struct {
	mu     sync.Mutex
	events []T.Event
}

func (c *liveEventCollector) record(e T.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

// snapshot returns a copy of every event recorded so far. Safe to call
// concurrently with record (the drain goroutine) at any time, including before
// the trace is closed.
func (c *liveEventCollector) snapshot() []T.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]T.Event, len(c.events))
	copy(out, c.events)
	return out
}

// newTraceWithLiveEvents builds a Trace (buf-sized channel) whose events are
// ALSO mirrored into a liveEventCollector as they're recorded, so a test
// goroutine can poll snapshot() while node goroutines are still running,
// without racing Trace's own drain-only Events()/events field.
func newTraceWithLiveEvents(buf int) (*T.Trace, *liveEventCollector) {
	c := &liveEventCollector{}
	tr := T.NewWithSinkHook(buf, nil, c.record)
	return tr, c
}
