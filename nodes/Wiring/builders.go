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
	"strings"

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
	Name     string
	Dir      PortDir
	Required bool // true for PortIn ports; output ports are never required
}

// PortBindings holds resolved channels or PacedWires keyed by port name.
// For PortOutMulti ports, use AppendMulti / OutSlice.
// Paced variants (SetSinglePaced / AppendMultiPacedWithHandle) take precedence over
// chan variants when both are set; in practice the loader uses only one mode.
type PortBindings struct {
	single           map[string]chan int
	multi            map[string][]chan int
	singlePaced      map[string]*PacedWire
	singleRule       map[string]SendRule   // per-port send rule for singlePaced
	singleArc        map[string]float64    // per-edge arc length for singlePaced Out
	singleLatency    map[string]float64    // per-edge sim latency for singlePaced Out
	singleCurve      map[string]edgeCurve  // per-edge curve control points for singlePaced Out
	multiPaced       map[string][]*PacedWire
	multiPacedHandle map[string][]string // per-element source handle for multiPaced
	multiRule        map[string][]SendRule // per-element send rule for multiPaced
	multiArc         map[string][]float64  // per-element arc length for multiPaced Out
	multiLatency     map[string][]float64  // per-element sim latency for multiPaced Out
	multiCurve       map[string][]edgeCurve // per-element curve control points for multiPaced Out
	// outSink, when non-nil, collects every paced *Out built for this node keyed
	// by "node.handle" so the loader can index Outs by edge for node-move
	// travel-time updates. Render/run paths leave it nil.
	outSink map[string]*Out
}

func newPortBindings() PortBindings {
	return PortBindings{
		single:           map[string]chan int{},
		multi:            map[string][]chan int{},
		singlePaced:      map[string]*PacedWire{},
		singleRule:       map[string]SendRule{},
		singleArc:        map[string]float64{},
		singleLatency:    map[string]float64{},
		singleCurve:      map[string]edgeCurve{},
		multiPaced:       map[string][]*PacedWire{},
		multiPacedHandle: map[string][]string{},
		multiRule:        map[string][]SendRule{},
		multiArc:         map[string][]float64{},
		multiLatency:     map[string][]float64{},
		multiCurve:       map[string][]edgeCurve{},
	}
}

func (pb *PortBindings) SetSingle(name string, ch chan int) { pb.single[name] = ch }

func (pb *PortBindings) SetSinglePaced(name string, pw *PacedWire) { pb.singlePaced[name] = pw }

// SetSinglePacedRule binds a single paced output with its per-edge send rule,
// that edge's own travel-time (arc length / sim latency), and its curve control
// points (so the bead's position stream evaluates the exact drawn curve).
func (pb *PortBindings) SetSinglePacedRule(name string, pw *PacedWire, rule SendRule, arcLength, simLatencyMs float64, curve edgeCurve) {
	pb.singlePaced[name] = pw
	pb.singleRule[name] = rule
	pb.singleArc[name] = arcLength
	pb.singleLatency[name] = simLatencyMs
	pb.singleCurve[name] = curve
}

func (pb *PortBindings) AppendMulti(name string, ch chan int) {
	pb.multi[name] = append(pb.multi[name], ch)
}

// AppendMultiPacedWithHandle is like AppendMultiPaced but records the exact
// source handle (e.g. "ToNext0"), the per-edge send rule, that edge's own
// travel-time (arc length / sim latency), and its curve control points.
func (pb *PortBindings) AppendMultiPacedWithHandle(name, handle string, pw *PacedWire, rule SendRule, arcLength, simLatencyMs float64, curve edgeCurve) {
	pb.multiPaced[name] = append(pb.multiPaced[name], pw)
	pb.multiPacedHandle[name] = append(pb.multiPacedHandle[name], handle)
	pb.multiRule[name] = append(pb.multiRule[name], rule)
	pb.multiArc[name] = append(pb.multiArc[name], arcLength)
	pb.multiLatency[name] = append(pb.multiLatency[name], simLatencyMs)
	pb.multiCurve[name] = append(pb.multiCurve[name], curve)
}

func (pb *PortBindings) In(name string) <-chan int {
	ch := pb.single[name]
	if ch == nil {
		ch = make(chan int, 1)
	}
	return ch
}

func (pb *PortBindings) Out(name string) chan<- int {
	ch := pb.single[name]
	if ch == nil {
		ch = make(chan int, 1)
	}
	return ch
}

func (pb *PortBindings) OutSlice(name string) []chan<- int {
	chs := pb.multi[name]
	result := make([]chan<- int, len(chs))
	for i, c := range chs {
		result[i] = c
	}
	return result
}

var (
	tInPtr    = reflect.TypeFor[*In]()
	tOutPtr   = reflect.TypeFor[*Out]()
	tOutMulti = reflect.TypeFor[OutMulti]()
	tFireFunc = reflect.TypeFor[func()]()
)

// lowerFirst returns s with its first byte lowercased.
// Used for wire:"data.state" key derivation (field Held → key "held").
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// reflectStateKeys returns the data.state map keys required by sample's
// wire:"data.state" struct tags.
func reflectStateKeys(sample any) []string {
	t := reflect.TypeOf(sample).Elem()
	var keys []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Tag.Get("wire") == "data.state" {
			keys = append(keys, lowerFirst(f.Name))
		}
	}
	return keys
}

// reflectPorts walks the exported fields of the struct pointed to by sample
// and returns a PortSpec for each channel field that carries int.
// Chan-of-chan fields and non-channel fields are silently skipped.
func reflectPorts(sample any) []PortSpec {
	t := reflect.TypeOf(sample).Elem()
	var ports []PortSpec
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		switch f.Type {
		case tInPtr:
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortIn, Required: true})
		case tOutPtr:
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortOut})
		case tOutMulti:
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortOutMulti})
		}
	}
	return ports
}

// reflectBuild wires pb into the struct pointed to by nodePtr via reflection,
// then returns it cast to Node. ctx is required when pb contains PacedWire
// bindings (paced mode); it is passed into the In/Out wrappers.
func reflectBuild(ctx context.Context, name string, data *NodeData, pb PortBindings, e kindEntry, tr *T.Trace) (Node, error) {
	nodePtr := e.newNode()
	v := reflect.ValueOf(nodePtr).Elem()

	// Inject Fire closure if the struct has a `Fire func()` field.
	// The closure captures the node name so the node calls n.Fire()
	// with no arguments and cannot mis-name itself in the trace.
	if f := v.FieldByName("Fire"); f.IsValid() && f.CanSet() && f.Type() == tFireFunc {
		nodeName := name
		f.Set(reflect.ValueOf(func() { tr.Fire(nodeName) }))
	}

	// Wire port fields with traced wrappers.
	ports := reflectPorts(nodePtr)
	for _, port := range ports {
		f := v.FieldByName(port.Name)
		if !f.IsValid() || !f.CanSet() {
			continue
		}
		switch port.Dir {
		case PortIn:
			if pw := pb.singlePaced[port.Name]; pw != nil {
				f.Set(reflect.ValueOf(NewInPaced(pw, ctx, name, port.Name, tr)))
			} else {
				ch := pb.In(port.Name)
				f.Set(reflect.ValueOf(&In{ch: ch, node: name, port: port.Name, trace: tr}))
			}
		case PortOut:
			if pw := pb.singlePaced[port.Name]; pw != nil {
				o := NewOutPaced(pw, ctx, name, port.Name, tr, pb.singleRule[port.Name], pb.singleArc[port.Name], pb.singleLatency[port.Name], pb.singleCurve[port.Name])
				if pb.outSink != nil {
					pb.outSink[name+"."+port.Name] = o
				}
				f.Set(reflect.ValueOf(o))
			} else {
				ch := pb.Out(port.Name)
				f.Set(reflect.ValueOf(&Out{ch: ch, node: name, port: port.Name, trace: tr}))
			}
		case PortOutMulti:
			if pws := pb.multiPaced[port.Name]; len(pws) > 0 {
				handles := pb.multiPacedHandle[port.Name]
				rules := pb.multiRule[port.Name]
				arcs := pb.multiArc[port.Name]
				lats := pb.multiLatency[port.Name]
				curves := pb.multiCurve[port.Name]
				outs := make(OutMulti, len(pws))
				for i, pw := range pws {
					handle := port.Name
					if i < len(handles) {
						handle = handles[i]
					}
					var rule SendRule
					if i < len(rules) {
						rule = rules[i]
					}
					var arc, lat float64
					if i < len(arcs) {
						arc = arcs[i]
					}
					if i < len(lats) {
						lat = lats[i]
					}
					var curve edgeCurve
					if i < len(curves) {
						curve = curves[i]
					}
					outs[i] = NewOutPaced(pw, ctx, name, handle, tr, rule, arc, lat, curve)
					if pb.outSink != nil {
						pb.outSink[name+"."+handle] = outs[i]
					}
				}
				f.Set(reflect.ValueOf(outs))
			} else {
				chs := pb.OutSlice(port.Name)
				outs := make(OutMulti, len(chs))
				for i, c := range chs {
					outs[i] = &Out{ch: c, node: name, port: port.Name, trace: tr}
				}
				f.Set(reflect.ValueOf(outs))
			}
		}
	}

	// Tag-driven data population: wire:"data.<key>" or wire:"data.state".
	t := reflect.TypeOf(nodePtr).Elem()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("wire")
		if tag == "" {
			continue
		}
		if data == nil {
			continue
		}
		fv := v.Field(i)
		if !fv.CanSet() {
			continue
		}
		const dataPrefix = "data."
		const stateTag = "data.state"
		if tag == stateTag {
			// key is field name with first letter lowercased
			key := lowerFirst(f.Name)
			if data.State == nil {
				return nil, fmt.Errorf("reflectBuild: node %q (kind %q): wire:\"data.state\" field %s requires data.state[%q] in topology JSON", name, reflect.TypeOf(nodePtr).Elem().Name(), f.Name, key)
			}
			val, ok := data.State[key]
			if !ok {
				return nil, fmt.Errorf("reflectBuild: node %q (kind %q): wire:\"data.state\" field %s requires data.state[%q] in topology JSON", name, reflect.TypeOf(nodePtr).Elem().Name(), f.Name, key)
			}
			fv.Set(reflect.ValueOf(val))
		} else if len(tag) > len(dataPrefix) && tag[:len(dataPrefix)] == dataPrefix {
			key := tag[len(dataPrefix):]
			if len(key) == 0 {
				continue
			}
			exportedKey := strings.ToUpper(key[:1]) + key[1:]
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

	node, ok := nodePtr.(Node)
	if !ok {
		return nil, fmt.Errorf("reflectBuild: %T does not implement Node", nodePtr)
	}
	return node, nil
}

// NodeBuilder is the public-facing type consumed by the loader.
// Ports is derived lazily from reflection; Build delegates to reflectBuild.
// StateKeys lists the data.state map keys required by this kind's
// wire:"data.state" struct fields; used by validateSpec for parse-time checks.
type NodeBuilder struct {
	Ports     []PortSpec
	StateKeys []string // required keys in NodeData.State; nil means none required
	Build     func(ctx context.Context, name string, data *NodeData, pb PortBindings, tr *T.Trace) (Node, error)
}

// Registry is the loader-facing map, built once at init from kindRegistry.
var Registry map[string]NodeBuilder

func init() {
	Registry = make(map[string]NodeBuilder, len(kindRegistry))
	for kind, e := range kindRegistry {
		sample := e.newNode()
		ports := reflectPorts(sample)
		stateKeys := reflectStateKeys(sample)
		Registry[kind] = NodeBuilder{
			Ports:     ports,
			StateKeys: stateKeys,
			Build: func(ctx context.Context, name string, data *NodeData, pb PortBindings, tr *T.Trace) (Node, error) {
				return reflectBuild(ctx, name, data, pb, e, tr)
			},
		}
	}
}

