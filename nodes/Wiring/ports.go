// ports.go — typed port wrappers that bake tracing into send/recv.
//
// Nodes hold In / Out / OutMulti fields instead of raw channels.
// TryRecv / TrySend emit the corresponding trace event on success,
// so a node cannot forget to trace, nor can it mis-type a port name
// string — the port name lives in the wrapper and is set by
// reflectBuild from the struct field name.

package Wiring

import (
	T "github.com/dtauraso/wirefold/Trace"
)

// In is a typed input port. Wraps a <-chan int with auto recv trace.
type In struct {
	ch    <-chan int
	node  string
	port  string
	trace *T.Trace
}

// TryRecv attempts a non-blocking receive. On success emits a recv
// trace event and returns (value, true). Otherwise returns (0, false).
func (i *In) TryRecv() (int, bool) {
	if i == nil || i.ch == nil {
		return 0, false
	}
	select {
	case v := <-i.ch:
		i.trace.Recv(i.node, i.port, v)
		return v, true
	default:
		return 0, false
	}
}

// Out is a typed output port. Wraps a chan<- int with auto send trace.
type Out struct {
	ch    chan<- int
	node  string
	port  string
	trace *T.Trace
}

// TrySend attempts a non-blocking send. On success emits a send trace
// event and returns true.
func (o *Out) TrySend(v int) bool {
	if o == nil || o.ch == nil {
		return false
	}
	select {
	case o.ch <- v:
		o.trace.Send(o.node, o.port, v)
		return true
	default:
		return false
	}
}

// OutMulti is a fanout port: a slice of Outs sharing one logical name.
type OutMulti []*Out

// NewIn / NewOut are exported for tests that construct nodes directly
// without going through reflectBuild.
func NewIn(ch <-chan int, node, port string, tr *T.Trace) *In {
	return &In{ch: ch, node: node, port: port, trace: tr}
}

func NewOut(ch chan<- int, node, port string, tr *T.Trace) *Out {
	return &Out{ch: ch, node: node, port: port, trace: tr}
}
