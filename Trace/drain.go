package Trace

import (
	"bytes"
	"encoding/json"
	"io"
)

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
	// Signal senders to stop. t.ch is NEVER closed, so an in-flight emit
	// selects the stopped case and drops rather than panicking.
	close(t.stopped)
	t.mu.Unlock()
	// drain() observes t.stopped, flushes any buffered events, then closes done.
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
// events carry node+port. Call after Close.
func (t *Trace) WriteJSONL(w io.Writer) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return writeAll(t.events, w, marshalEvent)
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
	// Reused across every event in this single drain goroutine — no per-event
	// destination allocation. json.Encoder defaults to SetEscapeHTML(true), matching
	// json.Marshal exactly, and Encode appends the trailing '\n' the sink line needs,
	// so buf.Bytes() is byte-identical to the previous marshalEvent(ev)+'\n' pair.
	encBuf := &bytes.Buffer{}
	enc := json.NewEncoder(encBuf)
	record := func(ev Event) {
		// The JSON event serialization here is an OPTIONAL in-process observation sink (used by
		// headless model tests that poll a bytes.Buffer). PRODUCTION passes sink=nil (main.go),
		// so NO trace-event JSON is written to stdout — the JSON-trace-on-stdout emitter is
		// removed at the wiring. The .probe log is now the DECODE of the fd3 binary content
		// buffer's EVENT block (onEvent → SnapshotState → ext-host buffer-log.ts), the spec's
		// "one representation including logs". Hold the lock across the sink write so a
		// concurrent Breadcrumb()/Close() write cannot fuse two JSON objects onto one line.
		t.mu.Lock()
		ev.Step = len(t.events)
		t.events = append(t.events, ev)
		if t.sink != nil {
			if v, err := eventValue(ev); err == nil {
				encBuf.Reset()
				if enc.Encode(v) == nil {
					_, _ = t.sink.Write(encBuf.Bytes())
				}
			}
		}
		t.mu.Unlock()
		// Binary snapshot hook: called outside the lock (pure state mutation, no
		// channel sends). The drain goroutine is the sole caller, so the hook
		// never races with itself or with the lock-protected sink writes above.
		if t.onEvent != nil {
			t.onEvent(ev)
		}
	}
	// dispatch routes one event off t.ch: kindBreadcrumb bypasses record entirely — no
	// Step, no append to t.events, no onEvent call (breadcrumbs are outside the closed
	// TraceEventKinds vocabulary) — everything else goes through record as before.
	dispatch := func(ev Event) {
		if ev.Kind == kindBreadcrumb {
			writeBreadcrumb(t, ev)
			return
		}
		record(ev)
	}
	for {
		select {
		case ev := <-t.ch:
			dispatch(ev)
		case <-t.stopped:
			// Close() signalled. Drain any events already buffered in t.ch
			// (senders may have enqueued before observing stopped), then finish.
			for {
				select {
				case ev := <-t.ch:
					dispatch(ev)
				default:
					close(t.done)
					return
				}
			}
		}
	}
}

// writeBreadcrumb writes one Breadcrumb() line to sink/debugSink, exactly as
// Breadcrumb used to do inline under t.mu. Called only from the drain goroutine
// (dispatch, above), which is now the SOLE writer of sink/debugSink, so no lock
// is needed here.
func writeBreadcrumb(t *Trace, ev Event) {
	b, err := marshalBreadcrumb(ev.BreadcrumbLabel, ev.Node, ev.Port, ev.BreadcrumbValue)
	if err != nil {
		return
	}
	if t.sink != nil {
		_, _ = t.sink.Write(b)
		_, _ = t.sink.Write([]byte{'\n'})
	}
	if t.debugSink != nil {
		_, _ = t.debugSink.Write(b)
		_, _ = t.debugSink.Write([]byte{'\n'})
	}
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
//
// json.Marshal(eventValue(e)) is byte-identical to encoding the same struct via
// a json.Encoder (both default to SetEscapeHTML(true)); the only Encoder addition
// is a trailing '\n', which the drain loop already appends anyway. The drain
// goroutine therefore encodes eventValue into a reused bytes.Buffer to avoid the
// per-event destination allocation on the high-volume KindPosition stream, while
// this wrapper preserves the []byte API for WriteJSONL's batch replay path.
func marshalEvent(e Event) ([]byte, error) {
	v, err := eventValue(e)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// eventValue returns the closed-vocabulary struct value for e (the thing to
// json-encode). Kept separate from marshalEvent so the drain loop can encode it
// straight into a reused buffer without allocating a fresh []byte per event.
func eventValue(e Event) (any, error) {
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
	type position struct {
		Step  int     `json:"step"`
		Kind  string  `json:"kind"`
		Node  string  `json:"node"`
		Port  string  `json:"port"`
		Value int     `json:"value"`
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
		Z     float64 `json:"z"`
		F     float64 `json:"f"`
		Bead  uint64  `json:"bead,omitempty"`
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
	type arriveShape struct {
		Step  int    `json:"step"`
		Kind  string `json:"kind"`
		Node  string `json:"node"`
		Port  string `json:"port"`
		Value int    `json:"value"`
		Bead  uint64 `json:"bead,omitempty"`
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
		Step     int            `json:"step"`
		Kind     string         `json:"kind"`
		Node     string         `json:"node"`
		Label    string         `json:"label,omitempty"`
		NodeKind string         `json:"nodeKind,omitempty"`
		NX       float64        `json:"nx"`
		NY       float64        `json:"ny"`
		NZ       float64        `json:"nz"`
		Radius   float64        `json:"radius"`
		SphereR  float64        `json:"sphereR,omitempty"`
		VRX      float64        `json:"vrx"`
		VRY      float64        `json:"vry"`
		VRZ      float64        `json:"vrz"`
		FRX      float64        `json:"frx"`
		FRY      float64        `json:"fry"`
		FRZ      float64        `json:"frz"`
		Ports    []portGeomJSON `json:"ports"`
	}
	type nodeBead struct {
		Step    int     `json:"step"`
		Kind    string  `json:"kind"`
		Node    string  `json:"node"`
		Row     int     `json:"row"`
		Col     int     `json:"col"`
		Present bool    `json:"present"`
		Value   int     `json:"value"`
		X       float64 `json:"x"`
		Y       float64 `json:"y"`
		Z       float64 `json:"z"`
	}
	switch e.Kind {
	case KindFire:
		return fire{Step: e.Step, Kind: e.Kind, Node: e.Node}, nil
	case KindSend:
		if e.ArcLength != 0 || e.SimLatencyMs != 0 {
			return sendWire{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port, Value: e.Value, ArcLength: e.ArcLength, SimLatencyMs: e.SimLatencyMs, Target: e.Target, TargetHandle: e.TargetHandle}, nil
		}
		return recvOrSend{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port, Value: e.Value}, nil
	case KindPosition:
		// All three coordinates always emitted (0,0,0 is a valid position).
		return position{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port, Value: e.Value, X: e.X, Y: e.Y, Z: e.Z, F: e.F, Bead: e.Bead}, nil
	case KindGeometry:
		// All six segment-endpoint coordinates always emitted (0 is valid).
		return geometry{Step: e.Step, Kind: e.Kind, Edge: e.Edge,
			SX: e.SX, SY: e.SY, SZ: e.SZ,
			EX: e.EX, EY: e.EY, EZ: e.EZ}, nil
	case KindArrive:
		return arriveShape{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port, Value: e.Value, Bead: e.Bead}, nil
	case KindNodeGeometry:
		ports := make([]portGeomJSON, len(e.Ports))
		for i, p := range e.Ports {
			ports[i] = portGeomJSON(p)
		}
		return nodeGeometry{Step: e.Step, Kind: e.Kind, Node: e.Node, Label: e.Label, NodeKind: e.NodeKind, NX: e.NX, NY: e.NY, NZ: e.NZ, Radius: e.Radius, SphereR: e.SphereR,
			VRX: e.VRX, VRY: e.VRY, VRZ: e.VRZ, FRX: e.FRX, FRY: e.FRY, FRZ: e.FRZ, Ports: ports}, nil
	case KindNodeBead:
		// row/col/present/value/position always emitted (0/false is valid for each).
		return nodeBead{Step: e.Step, Kind: e.Kind, Node: e.Node, Row: e.Row, Col: e.Col, Present: e.Present, Value: e.Value, X: e.X, Y: e.Y, Z: e.Z}, nil
	case KindCamera:
		// All camera fields always emitted (0 is valid for any angle or position).
		type camera struct {
			Step     int     `json:"step"`
			Kind     string  `json:"kind"`
			PX       float64 `json:"px"`
			PY       float64 `json:"py"`
			PZ       float64 `json:"pz"`
			R        float64 `json:"r"`
			PosTheta float64 `json:"posTheta"`
			PosPhi   float64 `json:"posPhi"`
			UpTheta  float64 `json:"upTheta"`
			UpPhi    float64 `json:"upPhi"`
		}
		return camera{Step: e.Step, Kind: e.Kind, PX: e.PX, PY: e.PY, PZ: e.PZ, R: e.R, PosTheta: e.PosTheta, PosPhi: e.PosPhi, UpTheta: e.UpTheta, UpPhi: e.UpPhi}, nil
	case KindSceneSphere:
		// Center (cx,cy,cz) + radius; reuses the PX/PY/PZ/R fields (see SceneSphere above).
		type sceneSphere struct {
			Step   int     `json:"step"`
			Kind   string  `json:"kind"`
			CX     float64 `json:"cx"`
			CY     float64 `json:"cy"`
			CZ     float64 `json:"cz"`
			Radius float64 `json:"radius"`
		}
		return sceneSphere{Step: e.Step, Kind: e.Kind, CX: e.PX, CY: e.PY, CZ: e.PZ, Radius: e.R}, nil
	case KindSceneTori, KindScenePoles, KindNodePoles, KindSelSpherePoles, KindHandholds, KindLabelsGlobal, KindOverlaysVis, KindDoubleLinks:
		// Visibility toggles: all carry just the Visible flag.
		type visToggle struct {
			Step    int    `json:"step"`
			Kind    string `json:"kind"`
			Visible bool   `json:"visible"`
		}
		return visToggle{Step: e.Step, Kind: e.Kind, Visible: e.Visible}, nil
	case KindLayoutLink:
		type layoutLink struct {
			Step   int    `json:"step"`
			Kind   string `json:"kind"`
			Node   string `json:"node"`
			Target string `json:"target"`
		}
		return layoutLink{Step: e.Step, Kind: e.Kind, Node: e.Node, Target: e.Target}, nil
	default:
		return recvOrSend{Step: e.Step, Kind: e.Kind, Node: e.Node, Port: e.Port, Value: e.Value}, nil
	}
}
