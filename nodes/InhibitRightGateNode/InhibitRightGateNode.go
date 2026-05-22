package InhibitRightGateNode

import (
	"context"
	"fmt"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type InhibitRightGateNode struct {
	Id       int
	Name     string
	Trace    *T.Trace
	Left     int
	HasLeft  bool
	Right    int
	HasRight bool
	FromLeft  <-chan int
	FromRight <-chan int
	ToPassed  chan<- int
}

func (g *InhibitRightGateNode) Update(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !g.HasLeft {
			select {
			case v := <-g.FromLeft:
				g.Left = v
				g.HasLeft = true
				g.Trace.Recv(g.Name, "FromLeft", v)
			default:
			}
		}

		if !g.HasRight {
			select {
			case v := <-g.FromRight:
				g.Right = v
				g.HasRight = true
				g.Trace.Recv(g.Name, "FromRight", v)
			default:
			}
		}

		if g.HasLeft && g.HasRight {
			result := 0
			if g.Left == 1 && g.Right == 0 {
				result = 1
			}
			fmt.Printf("%s: left=%d right=%d → %d\n", g.Name, g.Left, g.Right, result)
			g.Trace.Fire(g.Name)
			select {
			case g.ToPassed <- result:
			default:
			}
			g.Trace.Send(g.Name, "ToPassed", result)
			g.HasLeft = false
			g.HasRight = false
		}
	}
}

func init() {
	Wiring.Register("InhibitRightGate", func() any { return &InhibitRightGateNode{} })
}
