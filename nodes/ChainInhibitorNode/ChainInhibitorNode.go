package ChainInhibitorNode

import (
	"fmt"

	S "github.com/dtauraso/wirefold/nodes/SafeWorker"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type ChainInhibitorNode struct {
	Id         int
	Name       string
	HeldValue  int          `wire:"data.initialSlots.held"`
	FromPrevChainInhibitorNode <-chan int
	ToNext0                    chan<- int
	ToNext1                    chan<- int
}

func NewChainInhibitorNode(id int, fromPrev <-chan int) ChainInhibitorNode {
	return ChainInhibitorNode{Id: id, HeldValue: 0, FromPrevChainInhibitorNode: fromPrev}
}

func (in *ChainInhibitorNode) Update(s *S.SafeWorker) {
	defer s.Wg.Done()
	for {
		select {
		case <-s.Ctx.Done():
			return
		default:
		}

		select {
		case value := <-in.FromPrevChainInhibitorNode:
			s.Trace.Recv(in.Name, "FromPrevChainInhibitorNode", value)
			fmt.Printf("%s: received %d (old=%d)\n", in.Name, value, in.HeldValue)
			s.Trace.Fire(in.Name)
			if in.ToNext0 != nil {
				S.Send(in.ToNext0, in.HeldValue)
				s.Trace.Send(in.Name, "ToNext0", in.HeldValue)
			}
			if in.ToNext1 != nil {
				S.Send(in.ToNext1, in.HeldValue)
				s.Trace.Send(in.Name, "ToNext1", in.HeldValue)
			}
			in.HeldValue = value
		default:
		}
	}
}

func init() {
	Wiring.Register("ChainInhibitor", func() any { return &ChainInhibitorNode{} })
}
