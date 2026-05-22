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
//   - wire:"data.<key>"               reads NodeData.<Key> (e.g. data.init → Init []int)
//   - wire:"data.initialSlots.<key>"  reads NodeData.InitialSlots[key] (int)

package Wiring

import (
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

// PortBindings holds resolved channels keyed by port name.
// For PortOutMulti ports, use PortBindings.Multi(name).
type PortBindings struct {
	single map[string]chan int
	multi  map[string][]chan int
}

func newPortBindings() PortBindings {
	return PortBindings{
		single: map[string]chan int{},
		multi:  map[string][]chan int{},
	}
}

func (pb *PortBindings) SetSingle(name string, ch chan int) { pb.single[name] = ch }

func (pb *PortBindings) AppendMulti(name string, ch chan int) {
	pb.multi[name] = append(pb.multi[name], ch)
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
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortIn})
		case tOutPtr:
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortOut})
		case tOutMulti:
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortOutMulti})
		}
	}
	return ports
}

// reflectBuild wires pb into the struct pointed to by nodePtr via reflection,
// then returns it cast to Node.
func reflectBuild(name string, data *NodeData, pb PortBindings, e kindEntry, tr *T.Trace) (Node, error) {
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
			ch := pb.In(port.Name)
			f.Set(reflect.ValueOf(&In{ch: ch, node: name, port: port.Name, trace: tr}))
		case PortOut:
			ch := pb.Out(port.Name)
			f.Set(reflect.ValueOf(&Out{ch: ch, node: name, port: port.Name, trace: tr}))
		case PortOutMulti:
			chs := pb.OutSlice(port.Name)
			outs := make(OutMulti, len(chs))
			for i, c := range chs {
				outs[i] = &Out{ch: c, node: name, port: port.Name, trace: tr}
			}
			f.Set(reflect.ValueOf(outs))
		}
	}

	// Tag-driven data population: wire:"data.<key>" or wire:"data.initialSlots.<key>".
	t := reflect.TypeOf(nodePtr).Elem()
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("wire")
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
		const initSlotsPrefix = "data.initialSlots."
		if len(tag) > len(initSlotsPrefix) && tag[:len(initSlotsPrefix)] == initSlotsPrefix {
			key := tag[len(initSlotsPrefix):]
			if val, ok := data.InitialSlots[key]; ok {
				fv.Set(reflect.ValueOf(val))
			}
		} else if len(tag) > len(dataPrefix) && tag[:len(dataPrefix)] == dataPrefix {
			key := tag[len(dataPrefix):]
			switch key {
			case "init":
				if data.Init != nil {
					fv.Set(reflect.ValueOf(append([]int(nil), data.Init...)))
				}
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
	Build func(name string, data *NodeData, pb PortBindings, tr *T.Trace) (Node, error)
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
			Build: func(name string, data *NodeData, pb PortBindings, tr *T.Trace) (Node, error) {
				return reflectBuild(name, data, pb, e, tr)
			},
		}
	}
}

