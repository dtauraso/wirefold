// builders.go — reflection-driven port-manifest and node construction.
//
// Adding a kind: register one entry in kindRegistry. The struct fields
// determine the port manifest automatically:
//   - <-chan int  → PortIn
//   - chan<- int  → PortOut
//   - []chan<- int → PortOutMulti
//   - all other field types are ignored
//
// Non-channel fields can be populated from data.* JSON values via struct tags:
//   - wire:"data.<key>"               reads NodeData.<Key> (e.g. data.init → Init []int)
//   - wire:"data.initialSlots.<key>"  reads NodeData.InitialSlots[key] (int)

package Wiring

import (
	"fmt"
	"reflect"

	S "github.com/dtauraso/wirefold/nodes/SafeWorker"
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
	tChanIntRecv = reflect.TypeFor[<-chan int]()
	tChanIntSend = reflect.TypeFor[chan<- int]()
	tSliceSend   = reflect.TypeFor[[]chan<- int]()
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
		case tChanIntRecv:
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortIn})
		case tChanIntSend:
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortOut})
		case tSliceSend:
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortOutMulti})
		}
	}
	return ports
}

// reflectBuild wires pb into the struct pointed to by nodePtr via reflection,
// then returns it cast to S.Node.
func reflectBuild(id int, name string, data *NodeData, pb PortBindings, e kindEntry) (S.Node, error) {
	nodePtr := e.newNode()
	v := reflect.ValueOf(nodePtr).Elem()

	// Set Id and Name if the struct has them.
	if f := v.FieldByName("Id"); f.IsValid() && f.CanSet() {
		f.SetInt(int64(id))
	}
	if f := v.FieldByName("Name"); f.IsValid() && f.CanSet() {
		f.SetString(name)
	}

	// Wire channel fields.
	ports := reflectPorts(nodePtr)
	for _, port := range ports {
		f := v.FieldByName(port.Name)
		if !f.IsValid() || !f.CanSet() {
			continue
		}
		switch port.Dir {
		case PortIn:
			ch := pb.In(port.Name)
			f.Set(reflect.ValueOf(ch))
		case PortOut:
			ch := pb.Out(port.Name)
			f.Set(reflect.ValueOf(ch))
		case PortOutMulti:
			sl := pb.OutSlice(port.Name)
			f.Set(reflect.ValueOf(sl))
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

	node, ok := nodePtr.(S.Node)
	if !ok {
		return nil, fmt.Errorf("reflectBuild: %T does not implement S.Node", nodePtr)
	}
	return node, nil
}

// NodeBuilder is the public-facing type consumed by the loader.
// Ports is derived lazily from reflection; Build delegates to reflectBuild.
type NodeBuilder struct {
	Ports []PortSpec
	Build func(id int, name string, data *NodeData, pb PortBindings) (S.Node, error)
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
			Build: func(id int, name string, data *NodeData, pb PortBindings) (S.Node, error) {
				return reflectBuild(id, name, data, pb, e)
			},
		}
	}
}

