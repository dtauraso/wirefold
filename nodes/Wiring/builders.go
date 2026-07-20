// builders.go — reflection-driven port-manifest and node construction.
//
// Adding a kind: register one entry in kindRegistry. The struct fields
// determine the port manifest automatically:
//   - *Wiring.In       → PortIn
//   - *Wiring.Out      → PortOut
//   - Wiring.OutMulti  → PortOutMulti
//   - all other field types are ignored
//
// Non-channel fields can be populated from data.* JSON values via struct tags:
//   - wire:"data.<key>"  reads NodeData.<Key> where <Key> is <key> with its
//                        first letter uppercased. Any exported field on NodeData
//                        is reachable this way (e.g. data.init → NodeData.Init).
//                        Slice fields are copied, not aliased.
//                        Mismatched or absent fields are silently skipped.
//   - wire:"data.state"  reads NodeData.State[lowerFirst(fieldName)] (int).
//                        The map key is the struct field name with its first
//                        letter lowercased (e.g. field Held → key "held").

package Wiring

import (
	"context"
	"fmt"
	"reflect"

	T "github.com/dtauraso/wirefold/Trace"
)

// PortDir describes which direction a port flows.
type PortDir int

const (
	PortIn       PortDir = iota
	PortOut              // single output
	PortOutMulti         // slice output ([]chan<- int)
)

// PortSpec describes one port on a node kind.
type PortSpec struct {
	Name string
	Dir  PortDir
}

// PortBindings holds resolved PacedWires keyed by port name.
// For PortOutMulti ports, use AppendMultiPacedWithHandle.
// A port name with no paced binding resolves to a dead-end chan wrapper
// (deadEndIn/deadEndOut/deadEndOutSlice) that neither sends nor receives.
type PortBindings struct {
	// singlePaced holds the resolved paced binding for each single In/Out port.
	// multiPaced holds the per-element bindings for each OutMulti fan-out port.
	// Consolidating the formerly-parallel per-edge maps into one struct keeps
	// every field of a binding together and impossible to index-mismatch.
	singlePaced map[string]singleBinding
	multiPaced  map[string][]multiBinding
	// outSink, when non-nil, collects every paced *Out built for this node keyed
	// by "node.handle" so the loader can index Outs by edge for node-move
	// travel-time updates. Render/run paths leave it nil.
	outSink map[string]*Out
	// clock is the loader's monotonic clock, handed out (via inClock/wireInPort/
	// wireOutPort) to every In/Out constructed here for a ONE-TIME Copy() at
	// each owning goroutine's start (docs/planning/visual-editor/
	// per-goroutine-clock.md) — no PacedWire holds a clock of its own.
	// reflectBuild also injects it directly into nodes that need clock-paced
	// interior animation (the Input node's refill slide). Test builds without a
	// loader leave it nil, and such nodes fall back to an instant refill /
	// inertClock.
	clock Clock
}

// singleBinding is the resolved paced binding for one single port. For an INPUT
// port only pw is set (SetSinglePaced); an OUTPUT port also carries its per-edge
// send rule and own geometry (SetSinglePacedRule). The zero value (pw == nil)
// means "no paced binding — fall back to a dead-end chan".
type singleBinding struct {
	pw      *PacedWire
	rule    SendRule
	arc     float64
	latency float64
	seg     wireSegment
	label   string
}

// multiBinding is one fan-out element of an OutMulti port: its shared dest wire,
// the concrete source handle (e.g. "ToNext0"), per-edge send rule, and that
// edge's own travel-time / segment / TS label.
type multiBinding struct {
	pw      *PacedWire
	handle  string
	rule    SendRule
	arc     float64
	latency float64
	seg     wireSegment
	label   string
}

func newPortBindings() PortBindings {
	return PortBindings{
		singlePaced: map[string]singleBinding{},
		multiPaced:  map[string][]multiBinding{},
	}
}

func (pb *PortBindings) SetSinglePaced(name string, pw *PacedWire) {
	pb.singlePaced[name] = singleBinding{pw: pw}
}

// SetSinglePacedRule binds a single paced output with its per-edge send rule,
// that edge's own travel-time (arc length / sim latency), its straight-segment
// endpoints (so the bead's position stream evaluates the exact drawn segment), and
// the TS edge id (label) so the node's EmitGeometry closure can stream the segment.
func (pb *PortBindings) SetSinglePacedRule(name string, pw *PacedWire, rule SendRule, arcLength, simLatencyMs float64, seg wireSegment, label string) {
	pb.singlePaced[name] = singleBinding{pw: pw, rule: rule, arc: arcLength, latency: simLatencyMs, seg: seg, label: label}
}

// AppendMultiPacedWithHandle is like AppendMultiPaced but records the exact
// source handle (e.g. "ToNext0"), the per-edge send rule, that edge's own
// travel-time (arc length / sim latency), its straight-segment endpoints, and
// the TS edge id (label) so the node's EmitGeometry closure can stream the segment.
func (pb *PortBindings) AppendMultiPacedWithHandle(name, handle string, pw *PacedWire, rule SendRule, arcLength, simLatencyMs float64, seg wireSegment, label string) {
	pb.multiPaced[name] = append(pb.multiPaced[name], multiBinding{
		pw: pw, handle: handle, rule: rule, arc: arcLength, latency: simLatencyMs, seg: seg, label: label,
	})
}

// deadEndIn returns a fresh unbuffered-in-effect receive-only chan for a port
// name with no paced binding. It is never fed a value; it exists only so an
// unwired In field has a non-nil channel to hold.
func (pb *PortBindings) deadEndIn(name string) <-chan int {
	return make(chan int, 1) // chan-name-ok: dead-end placeholder; wire identity is the port `name` (map key)
}

// deadEndOut is deadEndIn's send-only counterpart for an unwired Out field.
func (pb *PortBindings) deadEndOut(name string) chan<- int {
	return make(chan int, 1) // chan-name-ok: dead-end placeholder; wire identity is the port `name` (map key)
}

// deadEndOutSlice is deadEndOut's counterpart for an unwired OutMulti field:
// there is no fan-out recorded for this port name, so it resolves to an empty
// slice of dead-end sends.
func (pb *PortBindings) deadEndOutSlice(name string) []chan<- int {
	return nil
}

var (
	tInPtr              = reflect.TypeFor[*In]()
	tOutPtr             = reflect.TypeFor[*Out]()
	tOutMulti           = reflect.TypeFor[OutMulti]()
	tFireFunc           = reflect.TypeFor[func()]()
	tEmitBeadsFunc      = reflect.TypeFor[func(working, backup []int)]()
	tEmitHeldFunc       = reflect.TypeFor[func(held int)]()
	tEmitInputBeadsFunc = reflect.TypeFor[func(left, right int)]()
	tRefillSlideFunc    = reflect.TypeFor[func(beads []int)]()
	tTickFunc           = reflect.TypeFor[func() int64]()
)

// reflectPorts walks the exported fields of the struct pointed to by sample
// and returns a PortSpec for each channel field that carries int.
// Chan-of-chan fields and non-channel fields are silently skipped.
// Anonymous (embedded) struct fields are recursed so port fields promoted
// from an embedded struct (e.g. gatecommon.GateNode) are discovered.
func reflectPorts(sample any) []PortSpec {
	t := reflect.TypeOf(sample).Elem()
	return collectPorts(t)
}

func collectPorts(t reflect.Type) []PortSpec {
	var ports []PortSpec
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			ft := f.Type
			if ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				ports = append(ports, collectPorts(ft)...)
			}
			continue
		}
		switch f.Type {
		case tInPtr:
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortIn})
		case tOutPtr:
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortOut})
		case tOutMulti:
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortOutMulti})
		}
	}
	return ports
}

// injectFunc sets the named func-typed field on v to fn, but only when the field
// exists, is settable, and has exactly the expected type `want`. This is the one
// shape every closure injection in reflectBuild shares (Fire, EmitGeometry, the
// Emit* bead closures, Tick); structs lacking the field are left
// untouched. Returns whether the field was set.
func injectFunc(v reflect.Value, name string, want reflect.Type, fn any) bool {
	f := v.FieldByName(name)
	if !f.IsValid() || !f.CanSet() || f.Type() != want {
		return false
	}
	f.Set(reflect.ValueOf(fn))
	return true
}

// reflectBuild wires pb into the struct pointed to by nodePtr via reflection,
// then returns it cast to Node. ctx is required when pb contains PacedWire
// bindings (paced mode); it is passed into the In/Out wrappers.
//
// The three concerns are split into named helpers, each called in the same
// order the original monolithic function performed them (behavior unchanged):
//   - injectClosures: Fire/EmitGeometry/EmitNodeBeads/EmitHeldBead/EmitInputBeads/
//     EmitRefillSlide/Tick closure injection.
//   - wirePorts: tag-driven (struct-shape-driven) port wiring — In/Out/OutMulti
//     fields set from pb's resolved bindings.
//   - populateData: wire:"data.<key>" / wire:"data.state" tag-driven data
//     population.
func reflectBuild(ctx context.Context, name string, data *NodeData, pb PortBindings, e kindEntry, tr *T.Trace, geom nodeGeom, partnerCenter partnerCenterFn) (Node, error) {
	nodePtr := e.newNode()
	v := reflect.ValueOf(nodePtr).Elem()

	var sourceOuts []*Out
	injectClosures(ctx, v, name, pb, tr, geom, &sourceOuts, partnerCenter)
	wirePorts(ctx, v, nodePtr, name, pb, tr, &sourceOuts)
	populateData(v, nodePtr, data)

	node, ok := nodePtr.(Node)
	if !ok {
		return nil, fmt.Errorf("reflectBuild: %T does not implement Node", nodePtr)
	}
	return node, nil
}

// injectClosures injects every func-typed closure field reflectBuild supports
// (Fire, EmitGeometry, the Emit* interior-bead closures, and — when a shared
// clock is present — EmitRefillSlide/Tick). Each injection is a no-op
// when the struct lacks the matching field (injectFunc's contract). Returns the
// sourceOuts slice that EmitGeometry's closure reads for per-edge segments;
// wirePorts appends to it as it resolves each Out/OutMulti binding, and the
// closure (which fires later, at node startup) sees the completed slice.
// sourceOuts is owned by the caller (reflectBuild) and shared with wirePorts,
// which appends to it as it resolves each Out/OutMulti binding; the EmitGeometry
// closure reads through the same pointer so it sees the completed slice.
func injectClosures(ctx context.Context, v reflect.Value, name string, pb PortBindings, tr *T.Trace, geom nodeGeom, sourceOuts *[]*Out, partnerCenter partnerCenterFn) {
	// Inject Fire closure if the struct has a `Fire func()` field. The closure
	// captures the node name so the node calls n.Fire() with no arguments and
	// cannot mis-name itself in the trace.
	injectFunc(v, "Fire", tFireFunc, func() { tr.Fire(name) })

	// Inject EmitGeometry closure if the struct has an `EmitGeometry func()` field.
	// The closure emits the node's authoritative center + per-port world
	// positions/dirs as a node-geometry event (port_geometry.go helpers, no
	// duplicated math), then each outgoing edge's segment. Each node's goroutine
	// calls it once on startup, so the node owns its geometry emission. sourceOuts
	// is populated during port wiring by wirePorts; the closure fires later (at node
	// startup), so it sees the completed slice.
	injectFunc(v, "EmitGeometry", tFireFunc, func() {
		emitNodeGeometryLocked(tr, name, geom, partnerCenter)
		for _, o := range *sourceOuts {
			if o != nil && o.EdgeLabel != "" {
				g := o.Geom()
				dst := ""
				if o.pw != nil {
					dst = o.pw.Target
				}
				tr.Geometry(o.EdgeLabel, name, dst,
					g.Start.X, g.Start.Y, g.Start.Z,
					g.End.X, g.End.Y, g.End.Z)
			}
		}
	})

	// Inject EmitNodeBeads closure if the struct has an `EmitNodeBeads
	// func(working, backup []int)` field (node 1's interior buffer). Emits one
	// node-bead event per present interior bead. The node's Update calls it with the
	// LIVE working/backup contents whenever the arrays change.
	injectFunc(v, "EmitNodeBeads", tEmitBeadsFunc, func(working, backup []int) {
		emitNodeBeads(tr, name, working, backup)
	})

	// Inject EmitHeldBead closure if the struct has an `EmitHeldBead func(held int)`
	// field (HoldNewSendOld's interior held-value bead): a SINGLE centered node-bead
	// (slot 0,0 at offset 0,0,0) colored by the held value; held == -1 →
	// present=false (empty interior).
	injectFunc(v, "EmitHeldBead", tEmitHeldFunc, func(held int) {
		emitHeldBead(tr, name, held)
	})

	// Inject EmitInputBeads closure if the struct has an `EmitInputBeads
	// func(left, right int)` field (a gate's two-sided held-input beads): LEFT input
	// on the left of the node, RIGHT on the right; -1 = not held → present=false.
	injectFunc(v, "EmitInputBeads", tEmitInputBeadsFunc, func(left, right int) {
		emitInputBeads(tr, name, left, right)
	})

	// The remaining injections require the loader's shared clock; a test build
	// without a loader leaves pb.clock nil and these fields stay nil (each node
	// falls back to its wall-clock / instant behavior).
	if pb.clock != nil {
		clk := pb.clock
		// EmitRefillSlide func(beads []int): the clock-paced refill slide (the OLD
		// backup beads slide DOWN from row 0 into row 1 at wire-bead speed; a paused
		// clock freezes it).
		injectFunc(v, "EmitRefillSlide", tRefillSlideFunc, func(beads []int) {
			emitRefillSlide(ctx, tr, name, clk, beads)
		})
		// Tick func() int64: current tick (pause-aware) off the shared human-speed
		// clock, so a node timing a window/dwell in ticks freezes on pause.
		injectFunc(v, "Tick", tTickFunc, func() int64 { return clk.Tick() })
		// Clock Wiring.Clock: the shared node-level clock, injected directly so a
		// node's paced Update loop does not have to derive its clock from a
		// specific wired output port (fragile — the port that happens to carry
		// the clock varies by topology). Only fields typed exactly Wiring.Clock
		// (e.g. input.Node.Clock) receive this; other nodes are unaffected.
		tClockType := reflect.TypeFor[Clock]()
		injectFunc(v, "Clock", tClockType, clk)
	}
}

// wirePorts wires every port field (In/Out/OutMulti) discovered by reflectPorts
// with traced wrappers, resolving each from pb's paced bindings when present and
// falling back to a dead-end chan/slice otherwise. sourceOuts accumulates every
// paced Out built (for EmitGeometry's closure, injected by injectClosures) and
// pb.outSink (when non-nil) is populated so the loader can index Outs by edge.
func wirePorts(ctx context.Context, v reflect.Value, nodePtr any, name string, pb PortBindings, tr *T.Trace, sourceOuts *[]*Out) {
	ports := reflectPorts(nodePtr)
	for _, port := range ports {
		f := v.FieldByName(port.Name)
		if !f.IsValid() || !f.CanSet() {
			continue
		}
		switch port.Dir {
		case PortIn:
			wireInPort(f, port.Name, ctx, name, pb, tr)
		case PortOut:
			wireOutPort(f, port.Name, ctx, name, pb, tr, sourceOuts)
		case PortOutMulti:
			wireOutMultiPort(f, port.Name, ctx, name, pb, tr, sourceOuts)
		}
	}
}

// wireInPort resolves a single PortIn field: a paced binding (NewInPaced) when
// pb has one for this port name, otherwise a dead-end chan wrapper.
//
// The dead-end In gets the shared clock as well as the placeholder channel: node pacing
// loops call In.Clock().SleepCycle unguarded, so an unwired In holding no clock is a
// panic (see In.Clock). With the real clock it paces exactly like a wired node and stays
// inert by polling a port that never delivers — the precondition-gating validate.go
// promises.
func wireInPort(f reflect.Value, portName string, ctx context.Context, name string, pb PortBindings, tr *T.Trace) {
	if b := pb.singlePaced[portName]; b.pw != nil {
		f.Set(reflect.ValueOf(NewInPaced(b.pw, ctx, name, portName, tr, pb.inClock())))
	} else {
		ch := pb.deadEndIn(portName)
		f.Set(reflect.ValueOf(&In{ch: ch, node: name, port: portName, trace: tr, clock: pb.inClock()}))
	}
}

// inClock is the Clock an unwired In should hold: the loader's shared clock when there is
// one (always, in production — loader.go sets pb.clock), else the inert placeholder for a
// test build with no loader. Never nil.
func (pb *PortBindings) inClock() Clock {
	if pb.clock != nil {
		return pb.clock
	}
	return inertClock{}
}

// wireOutPort resolves a single PortOut field: a paced binding
// (NewOutPaced, with the edge's own send rule/arc/latency/segment/label) when pb
// has one for this port name, otherwise a dead-end chan wrapper. The resolved
// paced Out is appended to sourceOuts and (when pb.outSink is non-nil) recorded
// under "node.port" for the loader's node-move travel-time updates.
func wireOutPort(f reflect.Value, portName string, ctx context.Context, name string, pb PortBindings, tr *T.Trace, sourceOuts *[]*Out) {
	if b := pb.singlePaced[portName]; b.pw != nil {
		o := NewOutPaced(b.pw, ctx, name, portName, tr, b.rule, b.arc, b.latency, b.seg, b.label, pb.inClock())
		*sourceOuts = append(*sourceOuts, o)
		if pb.outSink != nil {
			pb.outSink[name+"."+portName] = o
		}
		f.Set(reflect.ValueOf(o))
	} else {
		ch := pb.deadEndOut(portName)
		f.Set(reflect.ValueOf(&Out{ch: ch, node: name, port: portName, trace: tr}))
	}
}

// wireOutMultiPort resolves a PortOutMulti field: one paced Out per fan-out
// element recorded in pb.multiPaced (each with its own handle/rule/arc/
// latency/segment/label) when present, otherwise a dead-end chan slice. Each
// resolved paced Out is appended to sourceOuts and (when pb.outSink is
// non-nil) recorded under "node.handle".
func wireOutMultiPort(f reflect.Value, portName string, ctx context.Context, name string, pb PortBindings, tr *T.Trace, sourceOuts *[]*Out) {
	if bs := pb.multiPaced[portName]; len(bs) > 0 {
		outs := make(OutMulti, len(bs))
		for i, b := range bs {
			outs[i] = NewOutPaced(b.pw, ctx, name, b.handle, tr, b.rule, b.arc, b.latency, b.seg, b.label, pb.inClock())
			*sourceOuts = append(*sourceOuts, outs[i])
			if pb.outSink != nil {
				pb.outSink[name+"."+b.handle] = outs[i]
			}
		}
		f.Set(reflect.ValueOf(outs))
	} else {
		chs := pb.deadEndOutSlice(portName)
		outs := make(OutMulti, len(chs))
		for i, c := range chs {
			outs[i] = &Out{ch: c, node: name, port: portName, trace: tr}
		}
		f.Set(reflect.ValueOf(outs))
	}
}

// populateData performs tag-driven data population: wire:"data.<key>" or
// wire:"data.state" struct tags on nodePtr's fields, read from data (a nil
// data leaves every tagged field untouched, matching the original guard).
func populateData(v reflect.Value, nodePtr any, data *NodeData) {
	if data == nil {
		return
	}
	t := reflect.TypeOf(nodePtr).Elem()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("wire")
		if tag == "" {
			continue
		}
		fv := v.Field(i)
		if !fv.CanSet() {
			continue
		}
		const dataPrefix = "data."
		const stateTag = "data.state"
		if tag == stateTag {
			// key is field name with first letter lowercased. The seed is
			// OPTIONAL: an absent key leaves the constructor default untouched
			// (the empty sentinel for held-bearing kinds), so "unset" can never
			// collide with a legitimately-held 0. Only a present key — a real
			// authored starting value — overrides the default.
			key := lowerFirst(f.Name)
			if val, ok := data.State[key]; ok {
				fv.Set(reflect.ValueOf(val))
			}
		} else if len(tag) > len(dataPrefix) && tag[:len(dataPrefix)] == dataPrefix {
			key := tag[len(dataPrefix):]
			if len(key) == 0 {
				continue
			}
			exportedKey := exportedFieldName(key)
			src := reflect.ValueOf(data).Elem().FieldByName(exportedKey)
			if !src.IsValid() || src.Type() != fv.Type() {
				continue
			}
			if src.Kind() == reflect.Slice {
				if src.IsNil() {
					continue
				}
				cp := reflect.MakeSlice(src.Type(), src.Len(), src.Len())
				reflect.Copy(cp, src)
				fv.Set(cp)
			} else {
				fv.Set(src)
			}
		}
	}
}

// verticalRingNormal and flatRingNormal are the two great-circle ring normals
// streamed on every node-geometry event so TS never hardcodes ring orientation.
// vertical: ring stands upright (normal points along +Z world axis).
// flat: ring lies flat (normal points along +Y world axis, Three y-up convention).
const (
	verticalRingNormalX, verticalRingNormalY, verticalRingNormalZ = 0.0, 0.0, 1.0
	flatRingNormalX, flatRingNormalY, flatRingNormalZ             = 0.0, 1.0, 0.0
)

// NodeBuilder is the public-facing type consumed by the loader.
// Ports is derived lazily from reflection; Build delegates to reflectBuild.
type NodeBuilder struct {
	Ports []PortSpec
	Build func(ctx context.Context, name string, data *NodeData, pb PortBindings, tr *T.Trace, geom nodeGeom, partnerCenter partnerCenterFn) (Node, error)
}

// Registry is the loader-facing map, populated one kind at a time by
// Register (registry.go) as each node package's init() runs.
var Registry map[string]NodeBuilder

func init() {
	// Register needs a non-nil map to write into; kindRegistry is always
	// empty at this point because package Wiring's init runs before the
	// importing packages' inits populate it via Register.
	Registry = make(map[string]NodeBuilder)
}
