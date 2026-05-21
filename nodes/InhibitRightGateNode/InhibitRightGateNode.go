package InhibitRightGateNode

import (
	"fmt"

	S "github.com/dtauraso/wirefold/nodes/SafeWorker"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type InhibitRightGateNode struct {
	Id       int
	Name     string
	Left     int
	HasLeft  bool
	Right    int
	HasRight bool
	FromLeft  <-chan int
	FromRight <-chan int
}

func (g *InhibitRightGateNode) Update(s *S.SafeWorker) {
	defer s.Wg.Done()
	for {
		select {
		case <-s.Ctx.Done():
			return
		default:
		}

		if !g.HasLeft {
			select {
			case v := <-g.FromLeft:
				g.Left = v
				g.HasLeft = true
				s.Trace.Recv(g.Name, "FromLeft", v)
			default:
			}
		}

		if !g.HasRight {
			select {
			case v := <-g.FromRight:
				g.Right = v
				g.HasRight = true
				s.Trace.Recv(g.Name, "FromRight", v)
			default:
			}
		}

		if g.HasLeft && g.HasRight {
			result := 0
			if g.Left == 1 && g.Right == 0 {
				result = 1
			}
			fmt.Printf("%s: left=%d right=%d → %d\n", g.Name, g.Left, g.Right, result)
			s.Trace.Fire(g.Name)
			g.HasLeft = false
			g.HasRight = false
			_ = result
		}
	}
}

func init() {
	Wiring.Register("InhibitRightGate", func() any { return &InhibitRightGateNode{} })
}
