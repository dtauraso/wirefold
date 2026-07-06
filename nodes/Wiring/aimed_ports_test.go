package Wiring

import "context"

// aimedSrc / aimedSink / aimedPacer are minimal node kinds used as fixtures by other tests
// (the lock cascade). The aimed-port registry itself is gone (edges run node-to-node), so
// these are just plain kinds now.
type aimedSrc struct {
	Out        *Out
	FeedbackIn *In
}

func (n *aimedSrc) Update(_ context.Context) {}

type aimedSink struct{ In *In }

func (n *aimedSink) Update(_ context.Context) {}

type aimedPacer struct {
	FromSrc  *In
	Feedback *Out
}

func (n *aimedPacer) Update(_ context.Context) {}

func init() {
	Register("AimedSrc", func() any { return &aimedSrc{} })
	Register("AimedSink", func() any { return &aimedSink{} })
	Register("AimedPacer", func() any { return &aimedPacer{} })
}
