// Phase 7 Chunk 3 — runtime trace recorder.
//
// One Trace value is shared across all nodes; each node holds it as
// a *Trace field, injected at build time by Wiring.reflectBuild.
// Nodes call Emit at three points: on a successful channel receive
// (recv), before fanning out an emission (fire), and after each
// successful send. All events serialize through a single
// channel; a drain goroutine assigns the monotonic Step ordinal and
// appends to the slice — the order events arrive at the channel is
// the causal-enough story for replay (per trace-replay-plan.md).
//
// Wire format note: this package emits `send` events keyed by
// (Node, Port), NOT by edge ID. Edge IDs are a Wiring/spec-level
// concept the node doesn't have. Chunk 4 will add a spec-aware
// resolver that normalizes raw Go traces to the canonical edge-keyed
// form expected by chain-cascade.trace.jsonl. The on-disk Go trace
// is "raw"; the canonical form is what the parity test compares.

package Trace

import (
	"encoding/json"
	"io"
	"sync"
)

// Closed event vocabulary. Mirrors src/sim/trace.ts but keeps Port
// for both recv (input port) and send (output port). Node is always
// the emitting node — the one that received the value (recv) or sent
// it (send/fire).
const (
	KindRecv           = "recv"
	KindFire           = "fire"
	KindSend           = "send"
	KindDone           = "done"
	// KindPosition is the per-frame bead-position event (Phase 2). The wire's
	// delivery goroutine emits one every ~16 ms while a bead is in flight,
	// carrying the bead's evaluated 3-D position so the renderer plots it without
	// computing geometry itself.
	KindPosition       = "position"
)

// TraceEventKinds is the single source of truth for the closed kind
// vocabulary. gen-node-defs reads this slice to emit trace-kinds.ts;
// pump.ts exhaustiveness checks are derived from that generated file.
// Adding a kind here forces a tsc error in pump.ts until a branch is added.
var TraceEventKinds = []string{KindRecv, KindFire, KindSend, KindDone, KindPosition}

type Event struct {
	Step      int    `json:"step"`
	Kind      string `json:"kind"`
	Node      string `json:"node"`
	Port      string `json:"port,omitempty"`      // recv: input port; send: output port
	Edge      string `json:"edge,omitempty"`      // canonical send only; set by Resolve
	Value     int    `json:"value,omitempty"`     // recv/send only
	// hasValue distinguishes "value 0" from "no value" for send/recv events.
	hasValue bool
	// Wire geometry fields — populated on send events when the outgoing port
	// is backed by a PacedWire. Zero values are omitted from JSON output.
	// ArriveStep is omitted: the substrate has no global ms-per-step cadence,
	// so the TS layer derives arrival from emitTime + simLatencyMs instead.
	ArcLength    float64 `json:"arcLength,omitempty"`
	SimLatencyMs float64 `json:"simLatencyMs,omitempty"`
	// Destination slot identity — authoritative from Go. Set on send events backed
	// by a PacedWire so the TS layer never derives targetHandle from edge data.
	Target       string  `json:"target,omitempty"`
	TargetHandle string  `json:"targetHandle,omitempty"`
	// X/Y/Z carry the bead's evaluated 3-D world position on position events
	// (KindPosition). Go computes the position from the bead's curve + clock; the
	// renderer plots these directly. hasPos distinguishes a real (possibly 0,0,0)
	// position from an unset one so marshalEvent always emits all three.
	X, Y, Z float64
	hasPos  bool
}

// Trace is the shared recorder. Construct with New; injected into
// each node's Trace field by Wiring.reflectBuild. Call Close after
// all nodes have stopped to drain
// the channel and receive the final event slice via Events().
type Trace struct {
	ch     chan Event
	done   chan struct{}
	mu     sync.Mutex
	events []Event
	closed bool
	sink   io.Writer // if non-nil, each event is written as JSONL in real time
}

// New allocates a Trace with a buffered emit channel. buf controls
// how much burst the recorder absorbs before Emit blocks. 1024 is
// plenty for the current topology sizes; bump if Emit is observed
// to back-pressure node loops.
func New(buf int) *Trace {
	return NewWithSink(buf, nil)
}

// NewWithSink is like New but writes each event as JSONL to sink in
// real time (inside the drain goroutine) in addition to buffering.
// Pass nil for sink to disable streaming (identical to New).
func NewWithSink(buf int, sink io.Writer) *Trace {
	if buf <= 0 {
		buf = 1024
	}
	t := &Trace{
		ch:   make(chan Event, buf),
		done: make(chan struct{}),
		sink: sink,
	}
	go t.drain()
	return t
}

// Emit sends one event. Called from node Update loops — always check
// t != nil at the call site so untraced runs are zero-cost beyond a
// nil check. Blocks if the buffer is full (per trace-replay-plan §
// "Backpressure: buffered recorder channel; if full, log a warning
// and block briefly rather than drop"). The 1024-deep default keeps
// this rare in practice.
func (t *Trace) Emit(e Event) {
	if t == nil {
		return
	}
	t.ch <- e
}

// Recv emits a recv event for `(node, port, value)`. Convenience
// wrapper so node code stays one-line.
func (t *Trace) Recv(node, port string, value int) {
	if t == nil {
		return
	}
	t.ch <- Event{Kind: KindRecv, Node: node, Port: port, Value: value, hasValue: true}
}

// Fire emits a fire event for `node`. Called once per handler
// activation that produces ≥1 emission, before the first Send.
func (t *Trace) Fire(node string) {
	if t == nil {
		return
	}
	t.ch <- Event{Kind: KindFire, Node: node}
}

// Send emits a send event for `(node, port, value)` after a
// successful S.Send on the corresponding output channel.
func (t *Trace) Send(node, port string, value int) {
	if t == nil {
		return
	}
	t.ch <- Event{Kind: KindSend, Node: node, Port: port, Value: value, hasValue: true}
}

// SendWire emits a send event like Send, additionally carrying the wire geometry
// fields (arcLength in world-units, simLatencyMs in milliseconds) and the
// authoritative destination slot identity (target node id, targetHandle port name)
// from the outgoing PacedWire. Pass zero values when the port is not backed by a PacedWire.
func (t *Trace) SendWire(node, port string, value int, arcLength, simLatencyMs float64, target, targetHandle string) {
	if t == nil {
		return
	}
	t.ch <- Event{Kind: KindSend, Node: node, Port: port, Value: value, hasValue: true, ArcLength: arcLength, SimLatencyMs: simLatencyMs, Target: target, TargetHandle: targetHandle}
}

// Done emits a done event for `(node, port)` when the receiver has finished
// using a value. The node and port identify the input port that was Done'd,
// matching the edge by target node + targetHandle in the webview.
func (t *Trace) Done(node, port string) {
	if t == nil {
		return
	}
	t.ch <- Event{Kind: KindDone, Node: node, Port: port}
}

// Position emits a per-frame bead-position event (Phase 2). node/port are the
// SOURCE node id + output port — the same identity carried by the send event, so
// the renderer routes the position to the right edge(s) by source+sourceHandle
// (fan-out). value echoes the bead value; x/y/z is the bead's evaluated 3-D world
// position on its own edge curve. The wire's delivery goroutine calls this every
// ~16 ms while the bead is in flight, and once more at t==1 just before delivery.
func (t *Trace) Position(node, port string, value int, x, y, z float64) {
	if t == nil {
		return
	}
	t.ch <- Event{Kind: KindPosition, Node: node, Port: port, Value: value, hasValue: true, X: x, Y: y, Z: z, hasPos: true}
}

// Breadcrumb writes a free-form diagnostic line directly to the sink
// (if any) in real time. It is logging-only: breadcrumbs are NOT added
// to the buffered event slice, do NOT receive a Step ordinal, and are
// outside the closed trace vocabulary used for replay/parity. The line
// shape is {"src":"go","kind":"breadcrumb","label":...,"node":...,
// "port":...,"value":...} with empty/zero fields omitted, so the TS
// relay can route it to go.jsonl alongside real trace events.
//
// Used to trace one-off control events (e.g. edge delete / wire reset)
// that have no place in the recv/fire/send/done lifecycle.
func (t *Trace) Breadcrumb(label, node, port, value string) {
	if t == nil || t.sink == nil {
		return
	}
	b, err := marshalBreadcrumb(label, node, port, value)
	if err != nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = t.sink.Write(b)
	_, _ = t.sink.Write([]byte{'\n'})
}

func marshalBreadcrumb(label, node, port, value string) ([]byte, error) {
	type breadcrumb struct {
		Kind  string `json:"kind"`
		Label string `json:"label"`
		Node  string `json:"node,omitempty"`
		Port  string `json:"port,omitempty"`
		Value string `json:"value,omitempty"`
	}
	return json.Marshal(breadcrumb{Kind: "breadcrumb", Label: label, Node: node, Port: port, Value: value})
}

// Close stops the drain goroutine. Call after every node's Update
// has returned (sync.WaitGroup.Wait in main). Idempotent.
func (t *Trace) Close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	close(t.ch)
	t.mu.Unlock()
	<-t.done
}

// Events returns a snapshot of the recorded sequence. Safe to call
// after Close; calling before Close races with the drain goroutine.
func (t *Trace) Events() []Event {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Event, len(t.events))
	copy(out, t.events)
	return out
}

// WriteJSONL serializes all recorded events as JSON-lines (one
// object per line, trailing newline) onto w. Emits raw form: send
// events carry node+port. For canonical (edge-keyed) output, run
// the events through Resolve first then call WriteCanonicalJSONL.
// Call after Close.
func (t *Trace) WriteJSONL(w io.Writer) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return writeAll(t.events, w, marshalEvent)
}

// WriteCanonicalJSONL emits the chunk-1 canonical wire format: send
// events use the `edge` field instead of node+port. Caller must have
// run events through Resolve first; send events without an Edge will
// produce malformed output. Standalone function (not a method)
// because the canonical events are typically the *result* of
// Resolve, not the trace's own buffer.
func WriteCanonicalJSONL(events []Event, w io.Writer) error {
	return writeAll(events, w, marshalCanonicalEvent)
}

func writeAll(events []Event, w io.Writer, marshal func(Event) ([]byte, error)) error {
	for _, e := range events {
		b, err := marshal(e)
		if err != nil {
			return err
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
		if _, err := w.Write([]byte{'\n'}); err != nil {
			return err
		}
	}
	return nil
}

func (t *Trace) drain() {
	for ev := range t.ch {
		t.mu.Lock()
		ev.Step = len(t.events)
		t.events = append(t.events, ev)
		t.mu.Unlock()
		if t.sink != nil {
			if b, err := marshalEvent(ev); err == nil {
				_, _ = t.sink.Write(b)
				_, _ = t.sink.Write([]byte{'\n'})
			}
		}
	}
	close(t.done)
}

// marshalEvent emits the closed-vocabulary shape:
//
//	{"step":N,"kind":"recv","node":"X","port":"Y","value":V}
//	{"step":N,"kind":"fire","node":"X"}
//	{"step":N,"kind":"send","node":"X","port":"Y","value":V}
//
// json.Marshal with `omitempty` would drop value=0 and port="";
// neither is correct (value 0 is a valid signal in this codebase, and
// a missing port on recv/send is a bug worth surfacing). Hand-roll
// to keep the shape stable.
func marshalEvent(e Event) ([]byte, error) {
	type recvOrSend struct {
		Step  int    `json:"step"`
		Kind  string `json:"kind"`
		Node  string `json:"node"`
		Port  string `json:"port"`
		Value int    `json:"value"`
	}
	type sendWire struct {
		Step         int     `json:"step"`
		Kind         string  `json:"kind"`
		Node         string  `json:"node"`
		Port         string  `json:"port"`
		Value        int     `json:"value"`
		ArcLength    float64 `json:"arcLength,omitempty"`
		SimLatencyMs float64 `json:"simLatencyMs,omitempty"`
		Target       string  `json:"target,omitempty"`
		TargetHandle string  `json:"targetHandle,omitempty"`
	}
	type fire struct {
		Step int    `json:"step"`
		Kind string `json:"kind"`
		Node string `json:"node"`
	}
	type doneEvent struct {
		Step int    `json:"step"`
		Kind string `json:"kind"`
		Node string `json:"node"`
		Port string `json:"port"`
	}
	type position struct {
		Step  int     `json:"step"`
		Kind  string  `json:"kind"`
		Node  string  `json:"node"`
		Port  string  `json:"port"`
		Value int     `json:"value"`
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
		Z     float64 `json:"z"`
	}
	switch e.Kind {
	case KindFire:
		return json.Marshal(fire{Step: e.Step, Kind: e.Kind, Node: e.Node})
	case KindSend:
		if e.ArcLength != 0 || e.SimLatencyMs != 0 {
			return json.Marshal(sendWire{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port, Value: e.Value, ArcLength: e.ArcLength, SimLatencyMs: e.SimLatencyMs, Target: e.Target, TargetHandle: e.TargetHandle})
		}
		return json.Marshal(recvOrSend{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port, Value: e.Value})
	case KindDone:
		return json.Marshal(doneEvent{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port})
	case KindPosition:
		// All three coordinates always emitted (0,0,0 is a valid position).
		return json.Marshal(position{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port, Value: e.Value, X: e.X, Y: e.Y, Z: e.Z})
	default:
		return json.Marshal(recvOrSend{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port, Value: e.Value})
	}
}

// marshalCanonicalEvent emits the chunk-1 wire-format shape:
//
//	{"step":N,"kind":"recv","node":"X","port":"Y","value":V}
//	{"step":N,"kind":"fire","node":"X"}
//	{"step":N,"kind":"send","edge":"E","value":V}
//
// Send events carry `edge` (set by Resolve) and drop `node`/`port`.
// recv and fire are identical to the raw form.
func marshalCanonicalEvent(e Event) ([]byte, error) {
	type recv struct {
		Step  int    `json:"step"`
		Kind  string `json:"kind"`
		Node  string `json:"node"`
		Port  string `json:"port"`
		Value int    `json:"value"`
	}
	type fire struct {
		Step int    `json:"step"`
		Kind string `json:"kind"`
		Node string `json:"node"`
	}
	type send struct {
		Step  int    `json:"step"`
		Kind  string `json:"kind"`
		Edge  string `json:"edge"`
		Value int    `json:"value"`
	}
	switch e.Kind {
	case KindRecv:
		return json.Marshal(recv{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port, Value: e.Value})
	case KindFire:
		return json.Marshal(fire{Step: e.Step, Kind: e.Kind, Node: e.Node})
	case KindSend:
		return json.Marshal(send{Step: e.Step, Kind: e.Kind, Edge: e.Edge, Value: e.Value})
	}
	return json.Marshal(e)
}
