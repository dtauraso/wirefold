package ReadGateNode

import (
	"fmt"
	S "github.com/dtauraso/wirefold/nodes/SafeWorker"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type ReadGateNode struct {
	Id        int
	Name      string
	Value     int
	HasValue  bool
	HasChainInhibitor bool
	FromInput <-chan int
	FromChainInhibitor   <-chan int
	ToChainInhibitor   chan<- int
}

func (g *ReadGateNode) Update(s *S.SafeWorker) {
	defer s.Wg.Done()
	for {
		select {
		case <-s.Ctx.Done():
			return
		default:
		}

		if !g.HasValue {
			select {
			case v := <-g.FromInput:
				g.Value = v
				g.HasValue = true
				s.Trace.Recv(g.Name, "FromInput", v)
				s.Trace.Slot(g.Name, "FromInput", "filled", v, true)
			default:
			}
		}

		if !g.HasChainInhibitor {
			select {
			case v := <-g.FromChainInhibitor:
				g.HasChainInhibitor = true
				s.Trace.Recv(g.Name, "FromChainInhibitor", v)
				s.Trace.Slot(g.Name, "FromChainInhibitor", "filled", v, true)
			default:
			}
		}

		if g.HasValue && g.HasChainInhibitor {
			fmt.Printf("%s: value=%d → %d\n", g.Name, g.Value, g.Value)
			s.Trace.Fire(g.Name)
			S.Send(g.ToChainInhibitor, g.Value)
			s.Trace.Send(g.Name, "ToChainInhibitor", g.Value)
			g.HasValue = false
			g.HasChainInhibitor = false
			s.Trace.Slot(g.Name, "FromInput", "empty", 0, false)
			s.Trace.Slot(g.Name, "FromChainInhibitor", "empty", 0, false)
		}
	}
}

func init() {
	Wiring.Register("ReadGate", func() any { return &ReadGateNode{} })
}
