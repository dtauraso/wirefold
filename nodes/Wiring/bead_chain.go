package Wiring

// bead_chain.go — isolated chain-of-bead-items mechanism.
//
// A wire is modeled as a chain of bead-sized item goroutines between two fixed
// anchor items.  Items relax to a straight line (event-driven, no busy-spin)
// and are born/retired to maintain roughly constant spacing.
//
// This file is standalone — nothing in the live wire/transport path imports it yet.
// Integration into PacedWire is a future step.
//
// Bead size: PulseBead in scene-content.tsx uses sphereGeometry args=[4, 8, 8]
// → radius 4 world-units.  Go-side mirrors that value as BeadSizeWu.
//
// # Deadlock prevention
//
// Bidirectional posUpdate propagation (A notifies B, B notifies A) would deadlock
// if both ends block on channel sends simultaneously.  Each item therefore has two
// small relay channels (leftRelay, rightRelay) sized 1.  Before sending to a
// neighbor the main loop drops the message into its own relay channel (non-blocking,
// latest-wins).  A per-item relay goroutine drains the relay and performs the actual
// blocking send to the neighbor.  This decouples the main loop from neighbor
// back-pressure entirely.

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// BeadSizeWu is the bead render radius in world units.
// Source of truth: scene-content.tsx PulseBead <sphereGeometry args={[4, 8, 8]} />
// (radius = first arg = 4).
const BeadSizeWu = 4.0

// Chain spacing thresholds (multiples of BeadSizeWu).
// upperThreshold: gap > this → spawn a new item between them.
// lowerThreshold: gap < this → retire this item across that gap.
// epsilon: positional change below this is ignored (quiescence guard).
const (
	upperThreshold = BeadSizeWu * 1.5
	lowerThreshold = BeadSizeWu * 0.5
	relaxEpsilon   = BeadSizeWu * 0.001
)

// side encodes which neighbor is being referenced from THIS item's perspective.
type side int

const (
	sideLeft  side = 0
	sideRight side = 1
)

// ---- inbox message types ----

type msgKind int

const (
	kindPosUpdate   msgKind = iota // neighbor position changed
	kindSetNeighbor                // relink a neighbor channel
	kindSetPos                     // move this item's own position (anchors only)
	kindSnapshot                   // re-emit current position to observer (for test snapshots)
	kindStop                       // terminate
	kindPulse                      // a value-bead pulse arrives at this item (lights it)
	kindPulseForward               // this item's hop timer elapsed; hand pulse to the right
)

type itemMsg struct {
	kind msgKind

	// kindPosUpdate / kindSetPos / kindSetNeighbor
	s       side
	pos     vec3
	seedPos vec3

	// kindSetNeighbor
	ch chan itemMsg

	// kindPulse
	value int
}

// PulseReport is emitted to the chain's pulseObserver when an item's pulse
// highlight turns on or off. Lit=true → the value-bead just lit this item;
// Lit=false → it left this item (handed onward, or reached the end).
type PulseReport struct {
	ID    int
	Value int
	Lit   bool
}

// ItemReport is emitted to the observer on every position change or life-cycle event.
type ItemReport struct {
	ID    int
	Pos   vec3
	Alive bool // false → item retired
}

// ---- relay ----
//
// A relay forwards the latest posUpdate from an item to one neighbor without
// blocking the item's main goroutine.  Size-1 channel; old value is replaced
// (latest-wins) so the relay always delivers the most recent position.

type relay struct {
	ch      chan itemMsg // size 1, holds at most one pending message
	stop    chan struct{}
	stopOnce sync.Once
}

func newRelay() *relay {
	return &relay{ch: make(chan itemMsg, 1), stop: make(chan struct{})}
}

func (r *relay) closeStop() {
	r.stopOnce.Do(func() { close(r.stop) })
}

// enqueue puts m into the relay, replacing any pending message (latest-wins).
func (r *relay) enqueue(m itemMsg) {
	select {
	case r.ch <- m:
	default:
		// drain the stale message and replace with the newer one
		select {
		case <-r.ch:
		default:
		}
		select {
		case r.ch <- m:
		default:
		}
	}
}

// run delivers messages from the relay to dest until stop is closed.
func (r *relay) run(dest chan itemMsg) {
	for {
		select {
		case <-r.stop:
			return
		case m := <-r.ch:
			select {
			case dest <- m:
			case <-r.stop:
				return
			}
		}
	}
}

// ---- item ----

// beadItem is the goroutine-local state for one item in the chain.
// All state mutations happen inside the item's own goroutine.
type beadItem struct {
	id    int
	fixed bool
	pos   vec3

	leftPos  vec3
	rightPos vec3

	inbox chan itemMsg
	left  chan itemMsg // inbox of left neighbor
	right chan itemMsg // inbox of right neighbor

	// relay goroutines: decouple outgoing sends from the main loop so
	// bidirectional posUpdate propagation cannot deadlock.
	leftRelay  *relay
	rightRelay *relay

	hasLeft  bool
	hasRight bool

	observer chan<- ItemReport

	chain *BeadChain

	// pulse state — mutated only inside this item's own goroutine.
	holdingPulse bool
	pulseValue   int
}

func newBeadItem(id int, pos vec3, fixed bool, obs chan<- ItemReport, chain *BeadChain) *beadItem {
	return &beadItem{
		id:         id,
		fixed:      fixed,
		pos:        pos,
		observer:   obs,
		chain:      chain,
		inbox:      make(chan itemMsg, 512),
		leftRelay:  newRelay(),
		rightRelay: newRelay(),
	}
}

// emitPulse posts a PulseReport to the chain's pulseObserver (nil-safe).
func (it *beadItem) emitPulse(lit bool) {
	if it.chain == nil || it.chain.pulseObserver == nil {
		return
	}
	it.chain.pulseObserver <- PulseReport{ID: it.id, Value: it.pulseValue, Lit: lit}
}

// isRightAnchor reports whether this item is the chain's right anchor (the
// destination end the pulse stops at).
func (it *beadItem) isRightAnchor() bool {
	return it.fixed && it.chain != nil && it.chain.rightAnchor == it
}

func (it *beadItem) report() {
	// Blocking send: items run in their own goroutines so briefly blocking here
	// is fine.  A non-blocking send would cause lost reports and stale test state.
	it.observer <- ItemReport{ID: it.id, Pos: it.pos, Alive: true}
}

// sendLeft enqueues a posUpdate for the left relay (non-blocking).
func (it *beadItem) sendLeft(m itemMsg) {
	if it.left != nil {
		it.leftRelay.enqueue(m)
	}
}

// sendRight enqueues a posUpdate for the right relay (non-blocking).
func (it *beadItem) sendRight(m itemMsg) {
	if it.right != nil {
		it.rightRelay.enqueue(m)
	}
}

// setLeft updates the left neighbor channel and rewires the left relay.
func (it *beadItem) setLeft(ch chan itemMsg, seedPos vec3) {
	it.left = ch
	it.leftPos = seedPos
	it.hasLeft = true
	// restart relay with new destination
	it.leftRelay.closeStop()
	it.leftRelay = newRelay()
	go it.leftRelay.run(ch)
}

// setRight updates the right neighbor channel and rewires the right relay.
func (it *beadItem) setRight(ch chan itemMsg, seedPos vec3) {
	it.right = ch
	it.rightPos = seedPos
	it.hasRight = true
	it.rightRelay.closeStop()
	it.rightRelay = newRelay()
	go it.rightRelay.run(ch)
}

func (it *beadItem) run() {
	// Start relay goroutines.
	if it.left != nil {
		go it.leftRelay.run(it.left)
	}
	if it.right != nil {
		go it.rightRelay.run(it.right)
	}

	// Emit initial position.
	it.report()

	for msg := range it.inbox {
		switch msg.kind {
		case kindStop:
			it.leftRelay.closeStop()
			it.rightRelay.closeStop()
			return

		case kindPosUpdate:
			if msg.s == sideLeft {
				it.leftPos = msg.pos
			} else {
				it.rightPos = msg.pos
			}
			if it.tryRelax() {
				it.chain.unregister(it.id)
				return
			}

		case kindSetPos:
			// Move this (fixed) anchor item to a new position and propagate.
			it.pos = msg.pos
			it.report()
			it.sendLeft(itemMsg{kind: kindPosUpdate, s: sideRight, pos: it.pos})
			it.sendRight(itemMsg{kind: kindPosUpdate, s: sideLeft, pos: it.pos})

		case kindSnapshot:
			// Re-emit current position for external state reconstruction.
			it.report()

		case kindPulse:
			// The value-bead pulse arrived at this item: light it.
			it.holdingPulse = true
			it.pulseValue = msg.value
			it.emitPulse(true)
			if it.isRightAnchor() {
				// Reached the destination end: unlight and stop (pulse done).
				it.holdingPulse = false
				it.emitPulse(false)
				break
			}
			// Spawn a DETACHED timer that waits one hop on the clock (freezes on
			// pause) then posts kindPulseForward into THIS item's own inbox. The
			// main loop must NOT block here — relaxation keeps running during the
			// hop (two timescales).
			hopDuration := it.chain.hopDuration()
			clk := it.chain.clock
			inbox := it.inbox
			go func() {
				target := clk.Now() + hopDuration
				if clk.WaitUntil(context.Background(), target) != nil {
					return
				}
				// Best-effort post; inbox is large-buffered.
				select {
				case inbox <- itemMsg{kind: kindPulseForward}:
				default:
					inbox <- itemMsg{kind: kindPulseForward}
				}
			}()

		case kindPulseForward:
			// Hop timer elapsed: if still holding, unlight and hand the pulse to the
			// CURRENT right neighbor (which may have changed since the timer started).
			if it.holdingPulse {
				it.emitPulse(false)
				it.holdingPulse = false
				if it.right != nil {
					v := it.pulseValue
					right := it.right
					go func() { right <- itemMsg{kind: kindPulse, value: v} }()
				}
			}

		case kindSetNeighbor:
			if msg.s == sideLeft {
				it.setLeft(msg.ch, msg.seedPos)
			} else {
				it.setRight(msg.ch, msg.seedPos)
			}
			if it.tryRelax() {
				it.chain.unregister(it.id)
				return
			}
		}
	}
}

// tryRelax computes the midpoint of the two neighbor positions and moves this
// item there if the delta exceeds epsilon.  Sends posUpdate to BOTH neighbors
// (via relays — non-blocking, so no deadlock).  Checks born/retire on the right gap.
// Returns true if this item retired itself (caller must return from run loop).
func (it *beadItem) tryRelax() bool {
	if it.fixed {
		// Anchors don't move but echo their position so interior items can converge.
		it.sendLeft(itemMsg{kind: kindPosUpdate, s: sideRight, pos: it.pos})
		it.sendRight(itemMsg{kind: kindPosUpdate, s: sideLeft, pos: it.pos})
		if it.hasRight {
			it.checkBornRetire() // anchors never retire; ignore return value
		}
		return false
	}
	if !it.hasLeft || !it.hasRight {
		return false
	}

	mid := it.leftPos.add(it.rightPos).scale(0.5)
	delta := mid.sub(it.pos).length()
	if delta > relaxEpsilon {
		it.pos = mid
		it.report()
		// Notify BOTH neighbors via relay (non-blocking, latest-wins coalescing).
		it.sendLeft(itemMsg{kind: kindPosUpdate, s: sideRight, pos: it.pos})
		it.sendRight(itemMsg{kind: kindPosUpdate, s: sideLeft, pos: it.pos})
	}

	return it.checkBornRetire()
}

// checkBornRetire looks at the right gap (this item exclusively owns it).
// Returns true if this item retired itself (caller must stop its run loop).
func (it *beadItem) checkBornRetire() bool {
	if !it.hasRight {
		return false
	}
	gap := it.rightPos.sub(it.pos).length()

	if gap > upperThreshold {
		it.spawnRight()
		return false
	}
	if gap < lowerThreshold && !it.fixed {
		it.retireSelf()
		return true
	}
	return false
}

// spawnRight inserts a new item B between this item and its current right neighbor.
func (it *beadItem) spawnRight() {
	mid := it.pos.add(it.rightPos).scale(0.5)
	id := it.chain.nextID()
	b := newBeadItem(id, mid, false, it.observer, it.chain)

	b.left = it.inbox
	b.leftPos = it.pos
	b.hasLeft = true
	b.right = it.right
	b.rightPos = it.rightPos
	b.hasRight = true

	// Tell old right neighbor: new left is B, seeded at mid.
	// Dispatched in a background goroutine to avoid blocking (retireSelf may be
	// called by an adjacent item simultaneously, causing a circular send deadlock).
	if it.right != nil {
		oldRight := it.right
		bInbox := b.inbox
		go func() { oldRight <- itemMsg{kind: kindSetNeighbor, s: sideLeft, ch: bInbox, seedPos: mid} }()
	}

	// Update this item's right to B.
	it.right = b.inbox
	it.rightPos = mid
	// Rewire rightRelay to deliver to B's inbox.
	it.rightRelay.closeStop()
	it.rightRelay = newRelay()
	go it.rightRelay.run(b.inbox)

	// Register B so Stop() can reach it.
	it.chain.register(b)

	// Report B born BEFORE starting B's goroutine (avoids a race on b.pos).
	it.observer <- ItemReport{ID: b.id, Pos: mid, Alive: true}

	// Start B last.
	go b.run()
}

// retireSelf removes this item from the chain by relinking neighbors, then stops.
func (it *beadItem) retireSelf() {
	// Tell neighbors about each other via background goroutines.
	// Synchronous sends would deadlock if both neighbors call retireSelf simultaneously.
	leftCh, rightCh := it.left, it.right
	rightPos, leftPos := it.rightPos, it.leftPos
	// If carrying the pulse, hand it to the right BEFORE unsplicing so the value
	// is never lost to machine-speed churn (MODEL.md).
	if it.holdingPulse && rightCh != nil {
		it.emitPulse(false)
		it.holdingPulse = false
		v := it.pulseValue
		go func() { rightCh <- itemMsg{kind: kindPulse, value: v} }()
	}
	if leftCh != nil {
		go func() { leftCh <- itemMsg{kind: kindSetNeighbor, s: sideRight, ch: rightCh, seedPos: rightPos} }()
	}
	if rightCh != nil {
		go func() { rightCh <- itemMsg{kind: kindSetNeighbor, s: sideLeft, ch: leftCh, seedPos: leftPos} }()
	}

	it.observer <- ItemReport{ID: it.id, Pos: it.pos, Alive: false}

	// Stop relay goroutines.
	it.leftRelay.closeStop()
	it.rightRelay.closeStop()
}

// ---- BeadChain ----

// BeadChain is the chain of bead items between two fixed anchors.
type BeadChain struct {
	leftAnchor  *beadItem
	rightAnchor *beadItem
	observer    chan ItemReport

	// clock paces the value-bead pulse hop (BeadSizeWu / PulseSpeedWuPerMs per hop),
	// freezing on global pause. Geometry relaxation is unpaced (machine-speed); only
	// the pulse reads the clock — the two-timescale split from MODEL.md.
	clock Clock
	// pulseObserver, when non-nil, receives a PulseReport on every pulse light/unlight.
	pulseObserver chan PulseReport

	mu    sync.Mutex
	items map[int]*beadItem

	idCounter atomic.Int64
}

// hopDuration is the per-item pulse hop time: one bead-width at the uniform pulse
// speed. Read on the chain's clock so it freezes during global pause.
func (bc *BeadChain) hopDuration() time.Duration {
	ms := BeadSizeWu / PulseSpeedWuPerMs
	return time.Duration(ms * float64(time.Millisecond))
}

// InjectPulse lights the value-bead pulse at the start of the chain. It posts a
// kindPulse to the first interior item (the one just right of the left anchor),
// or to the right anchor if there are no interior items.
func (bc *BeadChain) InjectPulse(value int) {
	bc.mu.Lock()
	first := bc.leftAnchor.right
	bc.mu.Unlock()
	if first == nil {
		first = bc.rightAnchor.inbox
	}
	first <- itemMsg{kind: kindPulse, value: value}
}

func (bc *BeadChain) nextID() int {
	return int(bc.idCounter.Add(1))
}

func (bc *BeadChain) register(it *beadItem) {
	bc.mu.Lock()
	bc.items[it.id] = it
	bc.mu.Unlock()
}

func (bc *BeadChain) unregister(id int) {
	bc.mu.Lock()
	delete(bc.items, id)
	bc.mu.Unlock()
}

// NewBeadChain constructs a fully-wired chain between start and end, starts all
// goroutines, and returns the chain.
func NewBeadChain(start, end vec3, observer chan ItemReport) *BeadChain {
	return NewBeadChainWithPulse(start, end, observer, nil, nil)
}

// NewBeadChainWithPulse is NewBeadChain plus a Clock (paces the pulse hop; defaults
// to a fresh RealClock when nil) and an optional pulseObserver (nil-safe) that
// receives PulseReports as the value-bead pulse lights and unlights each item.
func NewBeadChainWithPulse(start, end vec3, observer chan ItemReport, clock Clock, pulseObserver chan PulseReport) *BeadChain {
	if clock == nil {
		clock = NewRealClock()
	}
	bc := &BeadChain{
		observer:      observer,
		clock:         clock,
		pulseObserver: pulseObserver,
		items:         make(map[int]*beadItem),
	}

	dist := end.sub(start).length()
	n := int(math.Round(dist / BeadSizeWu))
	if n < 0 {
		n = 0
	}

	total := n + 2
	all := make([]*beadItem, total)

	for i := 0; i < total; i++ {
		var pos vec3
		if total == 1 {
			pos = start
		} else {
			t := float64(i) / float64(total-1)
			pos = lerp(start, end, t)
		}
		fixed := (i == 0 || i == total-1)
		id := bc.nextID()
		all[i] = newBeadItem(id, pos, fixed, observer, bc)
		bc.items[id] = all[i]
	}

	bc.leftAnchor = all[0]
	bc.rightAnchor = all[total-1]

	// Wire neighbor channels.
	for i := 0; i < total; i++ {
		it := all[i]
		if i > 0 {
			it.left = all[i-1].inbox
			it.leftPos = all[i-1].pos
			it.hasLeft = true
		}
		if i < total-1 {
			it.right = all[i+1].inbox
			it.rightPos = all[i+1].pos
			it.hasRight = true
		}
	}

	// Start goroutines.
	for _, it := range all {
		go it.run()
	}

	return bc
}

// Snapshot asks every registered item to re-emit its current position to the
// observer.  Use after drainAndSettle to ensure the final state is complete even
// if some items' last convergence step was not captured by the observer.
// The caller should drain the observer after calling Snapshot to collect reports.
func (bc *BeadChain) Snapshot() {
	bc.mu.Lock()
	items := make([]*beadItem, 0, len(bc.items))
	for _, it := range bc.items {
		items = append(items, it)
	}
	bc.mu.Unlock()

	for _, it := range items {
		it.inbox <- itemMsg{kind: kindSnapshot}
	}
}

// MoveAnchor repositions one anchor safely inside its own goroutine.
func (bc *BeadChain) MoveAnchor(which side, newPos vec3) {
	if which == sideLeft {
		bc.leftAnchor.inbox <- itemMsg{kind: kindSetPos, pos: newPos}
	} else {
		bc.rightAnchor.inbox <- itemMsg{kind: kindSetPos, pos: newPos}
	}
}

// Stop sends a stop message to every item goroutine.
func (bc *BeadChain) Stop() {
	bc.mu.Lock()
	items := make([]*beadItem, 0, len(bc.items))
	for _, it := range bc.items {
		items = append(items, it)
	}
	bc.mu.Unlock()

	for _, it := range items {
		it.inbox <- itemMsg{kind: kindStop}
	}
}
