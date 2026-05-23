package inhibitrightgate

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type Node struct {
	Fire     func()
	Left     int
	HasLeft  bool
	Right    int
	HasRight bool
	FromLeft  *Wiring.In
	FromRight *Wiring.In
	ToPassed  *Wiring.Out
}

func (g *Node) Update(ctx context.Context) {
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
			g.Fire()
			if g.ToPassed.TrySend(result) {
				g.FromLeft.Done()
				g.FromRight.Done()
				g.HasLeft = false
				g.HasRight = false
			}
		}
	}
}

func init() {
	Wiring.Register("InhibitRightGate", func() any { return &Node{} })
}
