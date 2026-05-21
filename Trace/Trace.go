// Phase 7 Chunk 3 — runtime trace recorder.
//
// One Trace value is shared across all nodes via SafeWorker.Trace.
// Nodes call Emit at three points: on a successful channel receive
// (recv), before fanning out an emission (fire), and after each
// successful S.Send (send). All events serialize through a single
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
	KindRecv = "recv"
	KindFire = "fire"
	KindSend = "send"
	KindSlot = "slot"
)

type Event struct {
	Step      int    `json:"step"`
	Kind      string `json:"kind"`
	Node      string `json:"node"`
	Port      string `json:"port,omitempty"`      // recv: input port; send: output port; slot: input port
	Edge      string `json:"edge,omitempty"`      // canonical send only; set by Resolve
	SlotPhase string `json:"slotPhase,omitempty"` // slot only: "filled" | "empty"
	Value     int    `json:"value,omitempty"`     // recv/send/slot(filled) only; fire and slot(empty) omit
	// hasValue distinguishes "value 0" from "no value" for slot and send/recv events.
	hasValue bool
}

// Trace is the shared recorder. Construct with New; pass to nodes via
// SafeWorker.Trace. Call Close after all nodes have stopped to drain
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

// Slot emits a slot event recording that an input port has filled or emptied.
// When phase="filled", value is the held value. When phase="empty", hasValue
// should be false and value is ignored (omitted from JSON output).
func (t *Trace) Slot(nodeId, port, phase string, value int, hasValue bool) {
	if t == nil {
		return
	}
	t.ch <- Event{Kind: KindSlot, Node: nodeId, Port: port, SlotPhase: phase, Value: value, hasValue: hasValue}
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
	type fire struct {
		Step int    `json:"step"`
		Kind string `json:"kind"`
		Node string `json:"node"`
	}
	type slotFilled struct {
		Step   int    `json:"step"`
		Kind   string `json:"kind"`
		NodeId string `json:"nodeId"`
		Port   string `json:"port"`
		Phase  string `json:"phase"`
		Value  int    `json:"value"`
	}
	type slotEmpty struct {
		Step   int    `json:"step"`
		Kind   string `json:"kind"`
		NodeId string `json:"nodeId"`
		Port   string `json:"port"`
		Phase  string `json:"phase"`
	}
	switch e.Kind {
	case KindFire:
		return json.Marshal(fire{Step: e.Step, Kind: e.Kind, Node: e.Node})
	case KindSlot:
		if e.hasValue {
			return json.Marshal(slotFilled{Step: e.Step, Kind: e.Kind, NodeId: e.Node, Port: e.Port, Phase: e.SlotPhase, Value: e.Value})
		}
		return json.Marshal(slotEmpty{Step: e.Step, Kind: e.Kind, NodeId: e.Node, Port: e.Port, Phase: e.SlotPhase})
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
