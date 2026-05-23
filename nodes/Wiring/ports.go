// ports.go — typed port wrappers that bake tracing into send/recv.
//
// Nodes hold In / Out / OutMulti fields instead of raw channels.
// TryRecv / TrySend emit the corresponding trace event on success,
// so a node cannot forget to trace, nor can it mis-type a port name
// string — the port name lives in the wrapper and is set by
// reflectBuild from the struct field name.
//
// Two backing modes:
//   - chan mode (NewIn / NewOut): used by node unit tests. Non-blocking
//     select on the raw channel — original TryRecv/TrySend semantics.
//   - PacedWire mode (NewInPaced / NewOutPaced): used by the loader.
//     TrySend blocks until the paced wire delivers the value (always
//     returns true); TryRecv blocks until a value arrives. Ctx cancel
//     causes both to return the zero-value / false.

package Wiring

import (
	"context"

	T "github.com/dtauraso/wirefold/Trace"
)

// In is a typed input port.
type In struct {
	// chan mode
	ch <-chan int
	// paced mode
	pw  *PacedWire
	ctx context.Context
	// shared
	node  string
	port  string
	trace *T.Trace
}

// TryRecv in chan mode: non-blocking select. In paced mode: blocks until
// a value is placed or ctx is cancelled.
func (i *In) TryRecv() (int, bool) {
	if i == nil {
		return 0, false
	}
	if i.pw != nil {
		v, err := i.pw.Recv(i.ctx)
		if err != nil {
			return 0, false
		}
		n, _ := v.(int)
		i.trace.Recv(i.node, i.port, n)
		return n, true
	}
	if i.ch == nil {
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

// Out is a typed output port.
type Out struct {
	// chan mode
	ch chan<- int
	// paced mode
	pw  *PacedWire
	ctx context.Context
	// shared
	node  string
	port  string
	trace *T.Trace
}

// TrySend in chan mode: non-blocking select. In paced mode: blocks until
// the wire delivers the value or ctx is cancelled.
func (o *Out) TrySend(v int) bool {
	if o == nil {
		return false
	}
	if o.pw != nil {
		// Emit the trace send event BEFORE blocking on delivery so the
		// webview sees it, animates, posts "delivered", and unblocks Send.
		// Emitting after Send returns causes a deadlock: the webview never
		// receives the event, never posts delivered, and Send never returns.
		o.trace.Send(o.node, o.port, v)
		if err := o.pw.Send(o.ctx, v); err != nil {
			return false
		}
		return true
	}
	if o.ch == nil {
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
// without going through reflectBuild. Uses chan mode.
func NewIn(ch <-chan int, node, port string, tr *T.Trace) *In {
	return &In{ch: ch, node: node, port: port, trace: tr}
}

func NewOut(ch chan<- int, node, port string, tr *T.Trace) *Out {
	return &Out{ch: ch, node: node, port: port, trace: tr}
}

// NewInPaced / NewOutPaced are used by the loader. Uses PacedWire mode.
func NewInPaced(pw *PacedWire, ctx context.Context, node, port string, tr *T.Trace) *In {
	return &In{pw: pw, ctx: ctx, node: node, port: port, trace: tr}
}

func NewOutPaced(pw *PacedWire, ctx context.Context, node, port string, tr *T.Trace) *Out {
	return &Out{pw: pw, ctx: ctx, node: node, port: port, trace: tr}
}
