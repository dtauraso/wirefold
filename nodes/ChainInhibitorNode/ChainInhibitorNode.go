package ChainInhibitorNode

import (
	"context"
	"fmt"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type ChainInhibitorNode struct {
	Id                         int
	Name                       string
	Trace                      *T.Trace
	HeldValue                  int `wire:"data.initialSlots.held"`
	FromPrevChainInhibitorNode <-chan int
	ToNext                     []chan<- int
}

func NewChainInhibitorNode(id int, fromPrev <-chan int) ChainInhibitorNode {
	return ChainInhibitorNode{Id: id, HeldValue: 0, FromPrevChainInhibitorNode: fromPrev}
}

func (in *ChainInhibitorNode) Update(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		select {
		case value := <-in.FromPrevChainInhibitorNode:
			in.Trace.Recv(in.Name, "FromPrevChainInhibitorNode", value)
			fmt.Printf("%s: received %d (old=%d)\n", in.Name, value, in.HeldValue)
			in.Trace.Fire(in.Name)
			for _, ch := range in.ToNext {
				select {
				case ch <- in.HeldValue:
				default:
				}
				in.Trace.Send(in.Name, "ToNext", in.HeldValue)
			}
			in.HeldValue = value
		default:
		}
	}
}

func init() {
	Wiring.Register("ChainInhibitor", func() any { return &ChainInhibitorNode{} })
}
