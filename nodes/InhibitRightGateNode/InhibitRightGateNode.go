package InhibitRightGateNode

import (
	"context"
	"fmt"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type InhibitRightGateNode struct {
	Id       int
	Name     string
	Fire     func()
	Left     int
	HasLeft  bool
	Right    int
	HasRight bool
	FromLeft  *Wiring.In
	FromRight *Wiring.In
	ToPassed  *Wiring.Out
}

func (g *InhibitRightGateNode) Update(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !g.HasLeft {
			if v, ok := g.FromLeft.TryRecv(); ok {
				g.Left = v
				g.HasLeft = true
			}
		}

		if !g.HasRight {
			if v, ok := g.FromRight.TryRecv(); ok {
				g.Right = v
				g.HasRight = true
			}
		}

		if g.HasLeft && g.HasRight {
			result := 0
			if g.Left == 1 && g.Right == 0 {
				result = 1
			}
			fmt.Printf("%s: left=%d right=%d → %d\n", g.Name, g.Left, g.Right, result)
			if g.ToPassed.TrySend(result) {
				g.Fire()
				g.HasLeft = false
				g.HasRight = false
			}
		}
	}
}

func init() {
	Wiring.Register("InhibitRightGate", func() any { return &InhibitRightGateNode{} })
}
