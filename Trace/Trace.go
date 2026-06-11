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
	// KindGeometry carries an edge's authoritative straight-segment endpoints
	// (Phase 3). Go owns node positions + per-edge segments; it emits one geometry
	// event per edge on load and again whenever a node-move re-derives that edge's
	// segment, so the renderer draws the wire tube from Go's endpoints and
	// computes no geometry of its own. Keyed by edge label (== the TS edge id).
	KindGeometry       = "geometry"
	// KindPulseCancelled tells the renderer to remove an in-flight bead's sprite
	// (Phase 3). Go emits it when a wire drops a bead mid-flight (edge deleted while
	// the bead was traversing). Keyed by the bead's SOURCE node+port — the same
	// routing key as send/position — so the renderer drops the sprite by source.
	KindPulseCancelled = "pulse-cancelled"
	// KindNodeGeometry carries one node's authoritative center + per-port world
	// positions/directions (item 1). Each node's goroutine emits this once on
	// startup via its injected EmitGeometry closure — the node owns its own
	// geometry emission (wires still own bead-position emission). Keyed by node id.
	KindNodeGeometry = "node-geometry"
	// KindChainBead carries one chain bead-item's world position (the chain-of-beads
	// wire model). The wire's geometry-drain goroutine emits these at a render-cadence
	// throttle while items relax/are born/retired. Keyed by edge label + bead id.
	KindChainBead = "chain-bead"
	// KindPulseLit carries a chain bead's pulse highlight turning on or off as the
	// value-bead pulse hops item-to-item. Keyed by edge label + bead id; value echoes
	// the transported value, lit is the highlight state.
	KindPulseLit = "pulse-lit"
)

// TraceEventKinds is the single source of truth for the closed kind
// vocabulary. gen-node-defs reads this slice to emit trace-kinds.ts;
// pump.ts exhaustiveness checks are derived from that generated file.
// Adding a kind here forces a tsc error in pump.ts until a branch is added.
var TraceEventKinds = []string{KindRecv, KindFire, KindSend, KindDone, KindPosition, KindGeometry, KindPulseCancelled, KindNodeGeometry, KindChainBead, KindPulseLit}

// PortGeom is one port's authoritative world geometry on a node-geometry event:
// its name, whether it is an input, its sphere-surface world position (PX/PY/PZ),
// and the unit direction from node center toward the port (DX/DY/DZ).
type PortGeom struct {
	Name    string
	IsInput bool
	PX, PY, PZ float64
	DX, DY, DZ float64
}

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
	// ArriveStep is omitted: Go has no global ms-per-step cadence,
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
	// SX/SY/SZ and EX/EY/EZ carry an edge's authoritative straight-segment endpoints
	// on geometry events (KindGeometry): Start = source OUT-port world pos,
	// End = dest IN-port world pos. Go owns these; the renderer draws the wire tube
	// from them as a LineCurve3. Set on geometry events only (keyed by Edge).
	SX, SY, SZ float64
	EX, EY, EZ float64
	// NX/NY/NZ carry the node's center world position on node-geometry events
	// (KindNodeGeometry), and Ports carries that node's per-port world geometry.
	// Keyed by Node (the node id). Set on node-geometry events only.
	NX, NY, NZ float64
	Ports      []PortGeom
	// Bead is the chain bead-item id on chain-bead / pulse-lit events (keyed by
	// Edge + Bead). Lit is the pulse highlight state on pulse-lit events. chain-bead
	// reuses X/Y/Z for the bead's world position; pulse-lit reuses Value.
	Bead int
	Lit  bool
}

// Trace is the shared recorder. Construct with New; injected into
// each node's Trace field by Wiring.reflectBuild. Call Close after
// all nodes have stopped to drain
// the channel and receive the final event slice via Events().
type Trace struct {
	ch     chan Event
	done   chan struct{}
	// stop is closed at the START of Close (before ch is closed) so background
	// emitters (e.g. the bead-chain geometry/pulse drains, which outlive the node
	// run) can stop selecting on it and never send on the closed ch. Closing()
	// exposes it.
	stop   chan struct{}
	mu     sync.Mutex
	events []Event
	closed bool
	sink   io.Writer // if non-nil, each event is written as JSONL in real time
}

// Closing returns a channel closed when Close begins (before the emit channel
// closes). Background emitters that outlive node execution should select on it and
// stop emitting once it is closed, avoiding a send-on-closed-channel panic.
func (t *Trace) Closing() <-chan struct{} {
	if t == nil {
		// A nil trace never closes; return a never-closed channel so a select on it
		// is inert (and ChainBead/etc. are already no-ops on nil).
		return make(chan struct{})
	}
	return t.stop
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
		stop: make(chan struct{}),
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

// Geometry emits an edge's authoritative straight-segment endpoints (Phase 3),
// keyed by edge label (== the TS edge id). (sx,sy,sz) is the source OUT-port world
// pos (Start), (ex,ey,ez) is the dest IN-port world pos (End). Go emits this on load
// and on each node-move; the renderer draws the wire tube as a LineCurve3 from these.
func (t *Trace) Geometry(edge string, sx, sy, sz, ex, ey, ez float64) {
	if t == nil {
		return
	}
	t.ch <- Event{
		Kind: KindGeometry, Edge: edge,
		SX: sx, SY: sy, SZ: sz,
		EX: ex, EY: ey, EZ: ez,
	}
}

// NodeGeometry emits one node's authoritative center + per-port world geometry
// (item 1), keyed by node id. cx/cy/cz is the node center world position; ports
// carries each port's world position + direction. Each node's goroutine calls this
// once on startup via its injected EmitGeometry closure (the node owns its geometry
// emission; wires still own bead-position emission).
func (t *Trace) NodeGeometry(nodeID string, cx, cy, cz float64, ports []PortGeom) {
	if t == nil {
		return
	}
	t.ch <- Event{Kind: KindNodeGeometry, Node: nodeID, NX: cx, NY: cy, NZ: cz, Ports: ports}
}

// ChainBead emits one chain bead-item's world position (chain-of-beads wire model),
// keyed by edge label + bead id. The geometry-drain goroutine calls this at a render
// cadence while items relax / are born / retired; the renderer plots the bead chain.
func (t *Trace) ChainBead(edge string, bead int, x, y, z float64) {
	if t == nil {
		return
	}
	t.emitFromBackground(Event{Kind: KindChainBead, Edge: edge, Bead: bead, X: x, Y: y, Z: z, hasPos: true})
}

// emitFromBackground sends an event from a goroutine that may outlive Close (the
// bead-chain drains). It guards the send with the closed flag under the mutex so a
// post-Close emit is dropped instead of panicking on the closed channel. The drain
// goroutine receives without taking the mutex, so holding it across the buffered
// send does not deadlock (the channel is large-buffered; a full buffer is drained).
func (t *Trace) emitFromBackground(e Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.ch <- e
}

// PulseLit emits a chain bead's pulse highlight turning on/off as the value-bead
// pulse hops along the chain, keyed by edge label + bead id. value echoes the
// transported value; lit is the highlight state.
func (t *Trace) PulseLit(edge string, bead, value int, lit bool) {
	if t == nil {
		return
	}
	t.emitFromBackground(Event{Kind: KindPulseLit, Edge: edge, Bead: bead, Value: value, hasValue: true, Lit: lit})
}

// PulseCancelled tells the renderer to drop an in-flight bead's sprite (Phase 3),
// keyed by the bead's SOURCE node+port (the same routing key as send/position). Go
// emits it when a wire drops a bead mid-flight (edge deleted during traversal).
func (t *Trace) PulseCancelled(node, port string, value int) {
	if t == nil {
		return
	}
	t.ch <- Event{Kind: KindPulseCancelled, Node: node, Port: port, Value: value, hasValue: true}
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
	close(t.stop)
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
	type geometry struct {
		Step int     `json:"step"`
		Kind string  `json:"kind"`
		Edge string  `json:"edge"`
		SX   float64 `json:"sx"`
		SY   float64 `json:"sy"`
		SZ   float64 `json:"sz"`
		EX   float64 `json:"ex"`
		EY   float64 `json:"ey"`
		EZ   float64 `json:"ez"`
	}
	type pulseCancelled struct {
		Step  int    `json:"step"`
		Kind  string `json:"kind"`
		Node  string `json:"node"`
		Port  string `json:"port"`
		Value int    `json:"value"`
	}
	type portGeomJSON struct {
		Name    string  `json:"name"`
		IsInput bool    `json:"isInput"`
		PX      float64 `json:"px"`
		PY      float64 `json:"py"`
		PZ      float64 `json:"pz"`
		DX      float64 `json:"dx"`
		DY      float64 `json:"dy"`
		DZ      float64 `json:"dz"`
	}
	type nodeGeometry struct {
		Step  int            `json:"step"`
		Kind  string         `json:"kind"`
		Node  string         `json:"node"`
		NX    float64        `json:"nx"`
		NY    float64        `json:"ny"`
		NZ    float64        `json:"nz"`
		Ports []portGeomJSON `json:"ports"`
	}
	type chainBead struct {
		Step int     `json:"step"`
		Kind string  `json:"kind"`
		Edge string  `json:"edge"`
		Bead int     `json:"bead"`
		X    float64 `json:"x"`
		Y    float64 `json:"y"`
		Z    float64 `json:"z"`
	}
	type pulseLit struct {
		Step  int    `json:"step"`
		Kind  string `json:"kind"`
		Edge  string `json:"edge"`
		Bead  int    `json:"bead"`
		Value int    `json:"value"`
		Lit   bool   `json:"lit"`
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
	case KindGeometry:
		// All six segment-endpoint coordinates always emitted (0 is valid).
		return json.Marshal(geometry{Step: e.Step, Kind: e.Kind, Edge: e.Edge,
			SX: e.SX, SY: e.SY, SZ: e.SZ,
			EX: e.EX, EY: e.EY, EZ: e.EZ})
	case KindPulseCancelled:
		return json.Marshal(pulseCancelled{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port, Value: e.Value})
	case KindNodeGeometry:
		ports := make([]portGeomJSON, len(e.Ports))
		for i, p := range e.Ports {
			ports[i] = portGeomJSON{Name: p.Name, IsInput: p.IsInput, PX: p.PX, PY: p.PY, PZ: p.PZ, DX: p.DX, DY: p.DY, DZ: p.DZ}
		}
		return json.Marshal(nodeGeometry{Step: e.Step, Kind: e.Kind, Node: e.Node, NX: e.NX, NY: e.NY, NZ: e.NZ, Ports: ports})
	case KindChainBead:
		return json.Marshal(chainBead{Step: e.Step, Kind: e.Kind, Edge: e.Edge, Bead: e.Bead, X: e.X, Y: e.Y, Z: e.Z})
	case KindPulseLit:
		return json.Marshal(pulseLit{Step: e.Step, Kind: e.Kind, Edge: e.Edge, Bead: e.Bead, Value: e.Value, Lit: e.Lit})
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
