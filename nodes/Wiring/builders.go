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
// Paced variants (SetSinglePaced / AppendMultiPaced) take precedence over
// chan variants when both are set; in practice the loader uses only one mode.
type PortBindings struct {
	single           map[string]chan int
	multi            map[string][]chan int
	singlePaced      map[string]*PacedWire
	multiPaced       map[string][]*PacedWire
	multiPacedHandle map[string][]string // per-element source handle for multiPaced
}

func newPortBindings() PortBindings {
	return PortBindings{
		single:           map[string]chan int{},
		multi:            map[string][]chan int{},
		singlePaced:      map[string]*PacedWire{},
		multiPaced:       map[string][]*PacedWire{},
		multiPacedHandle: map[string][]string{},
	}
}

func (pb *PortBindings) SetSingle(name string, ch chan int) { pb.single[name] = ch }

func (pb *PortBindings) SetSinglePaced(name string, pw *PacedWire) { pb.singlePaced[name] = pw }

func (pb *PortBindings) AppendMulti(name string, ch chan int) {
	pb.multi[name] = append(pb.multi[name], ch)
}

func (pb *PortBindings) AppendMultiPaced(name string, pw *PacedWire) {
	pb.multiPaced[name] = append(pb.multiPaced[name], pw)
	pb.multiPacedHandle[name] = append(pb.multiPacedHandle[name], name)
}

// AppendMultiPacedWithHandle is like AppendMultiPaced but records the exact
// source handle (e.g. "ToNext0") so trace events carry the indexed name.
func (pb *PortBindings) AppendMultiPacedWithHandle(name, handle string, pw *PacedWire) {
	pb.multiPaced[name] = append(pb.multiPaced[name], pw)
	pb.multiPacedHandle[name] = append(pb.multiPacedHandle[name], handle)
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
				f.Set(reflect.ValueOf(NewOutPaced(pw, ctx, name, port.Name, tr)))
			} else {
				ch := pb.Out(port.Name)
				f.Set(reflect.ValueOf(&Out{ch: ch, node: name, port: port.Name, trace: tr}))
			}
		case PortOutMulti:
			if pws := pb.multiPaced[port.Name]; len(pws) > 0 {
				handles := pb.multiPacedHandle[port.Name]
				outs := make(OutMulti, len(pws))
				for i, pw := range pws {
					handle := port.Name
					if i < len(handles) {
						handle = handles[i]
					}
					outs[i] = NewOutPaced(pw, ctx, name, handle, tr)
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
			key := strings.ToLower(f.Name[:1]) + f.Name[1:]
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
type NodeBuilder struct {
	Ports []PortSpec
	Build func(ctx context.Context, name string, data *NodeData, pb PortBindings, tr *T.Trace) (Node, error)
}

// Registry is the loader-facing map, built once at init from kindRegistry.
var Registry map[string]NodeBuilder

func init() {
	Registry = make(map[string]NodeBuilder, len(kindRegistry))
	for kind, e := range kindRegistry {
		sample := e.newNode()
		ports := reflectPorts(sample)
		Registry[kind] = NodeBuilder{
			Ports: ports,
			Build: func(ctx context.Context, name string, data *NodeData, pb PortBindings, tr *T.Trace) (Node, error) {
				return reflectBuild(ctx, name, data, pb, e, tr)
			},
		}
	}
}

