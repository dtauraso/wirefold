package Wiring

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clock_concurrency_test.go — pins RealClock.mu's actual contention claim: every
// pacing loop in the system (paced_wire.go StepOnce, input/holdnewsendold/gatecommon
// Update loops) calls Tick() continuously and concurrently with the ONE writer,
// stdin_reader.go's "speed" message handler, which calls SetSpeed. This is the
// classic many-readers/one-writer shape on speed/accScaled/lastChange.
//
// TestRealClockConcurrentTickVsSetSpeedRace drives exactly that shape — many
// goroutines calling Tick() in a tight loop against one goroutine calling
// SetSpeed in a tight loop — for long enough that `go test -race` catches an
// unsynchronized read/write if the mutex is removed. This is the guard's
// falsifiability proof: with RealClock.mu's Lock/Unlock deleted from Tick and
// SetSpeed, this test fails under -race with a WARNING: DATA RACE report
// pointing at scaledElapsedLocked's reads of c.speed/c.accScaled/c.lastChange
// racing SetSpeed's writes to the same fields (verified manually; not
// reproduced automatically here since the fix is checked in).
func TestRealClockConcurrentTickVsSetSpeedRace(t *testing.T) {
	c := NewRealClock()
	var stop atomic.Bool
	var wg sync.WaitGroup

	const readers = 16
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				_ = c.Tick()
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		speeds := []float64{0, 1, 2, 0.5, 3}
		i := 0
		for !stop.Load() {
			c.SetSpeed(speeds[i%len(speeds)])
			i++
		}
	}()

	time.Sleep(200 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
}

// TestRealClockConcurrentMonotonic asserts the correctness claim (not just the
// data-race claim): each of many concurrent Tick()-reading goroutines, racing a
// single goroutine that flips SetSpeed continuously, must never observe its OWN
// sequential Tick() reads go backward. Each reader keeps its own "last seen"
// value — this is the per-reader monotonicity RealClock.Tick's doc comment
// promises ("monotonic non-decreasing for the process life") under concurrent
// speed changes, which is stronger than the existing single-goroutine
// clock_speed_test.go coverage (that file never has a second goroutine racing
// SetSpeed while Tick is read).
func TestRealClockConcurrentMonotonic(t *testing.T) {
	c := NewRealClock()
	var stop atomic.Bool
	var wg sync.WaitGroup
	failed := make(chan string, 1)

	const readers = 8
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			last := c.Tick()
			for !stop.Load() {
				cur := c.Tick()
				if cur < last {
					select {
					case failed <- (func() string {
						return "tick went backward"
					})():
					default:
					}
					return
				}
				last = cur
			}
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		speeds := []float64{0, 1, 2, 0.5, 3, 0}
		i := 0
		for !stop.Load() {
			c.SetSpeed(speeds[i%len(speeds)])
			i++
		}
	}()

	time.Sleep(300 * time.Millisecond)
	stop.Store(true)
	wg.Wait()

	select {
	case msg := <-failed:
		t.Fatalf("%s: a reader observed Tick() decrease under concurrent SetSpeed", msg)
	default:
	}
}
