package Wiring

import "context"

// aimedSrc / aimedSink / aimedPacer are minimal node kinds used as fixtures by other tests
// (the lock cascade). The aimed-port registry itself is gone (edges run node-to-node), and
// position writes route through nodeMover's own goroutine (node_move.go), so these are just
// plain kinds now — no layout plumbing to drain.
type aimedSrc struct {
	Out        *Out
	FeedbackIn *In
}

func (n *aimedSrc) Update(ctx context.Context) {
	<-ctx.Done()
}

type aimedSink struct {
	In *In
}

func (n *aimedSink) Update(ctx context.Context) {
	<-ctx.Done()
}

type aimedPacer struct {
	FromSrc  *In
	Feedback *Out
}

func (n *aimedPacer) Update(ctx context.Context) {
	<-ctx.Done()
}

func init() {
	Register("AimedSrc", func() any { return &aimedSrc{} })
	Register("AimedSink", func() any { return &aimedSink{} })
	Register("AimedPacer", func() any { return &aimedPacer{} })
}
