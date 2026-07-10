package Wiring

import "context"

// aimedSrc / aimedSink / aimedPacer are minimal node kinds used as fixtures by other tests
// (the lock cascade). The aimed-port registry itself is gone (edges run node-to-node), so
// these are just plain kinds now.
type aimedSrc struct {
	Out        *Out
	FeedbackIn *In
	Layout     *LayoutPort
}

// Update polls only the hidden layout port (SLICE 3, layout-on-domain-network.md):
// this node's own Update() goroutine is the sole writer of its position, so a test
// that drags this node must have this loop running to drain the write.
func (n *aimedSrc) Update(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-n.Layout.in:
			n.Layout.Handle(msg)
		}
	}
}

type aimedSink struct {
	In     *In
	Layout *LayoutPort
}

func (n *aimedSink) Update(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-n.Layout.in:
			n.Layout.Handle(msg)
		}
	}
}

type aimedPacer struct {
	FromSrc  *In
	Feedback *Out
	Layout   *LayoutPort
}

func (n *aimedPacer) Update(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-n.Layout.in:
			n.Layout.Handle(msg)
		}
	}
}

func init() {
	Register("AimedSrc", func() any { return &aimedSrc{} })
	Register("AimedSink", func() any { return &aimedSink{} })
	Register("AimedPacer", func() any { return &aimedPacer{} })
}
