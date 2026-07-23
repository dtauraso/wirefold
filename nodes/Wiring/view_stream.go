// view_stream.go — the VIEW stream's write side (Step C, docs/planning/visual-editor/
// per-owner-buffer-rows.md; memory/feedback_no_single_writer_bridge.md).
//
// Camera/overlay/scene-sphere/selection/hover state already lives on MoveDispatch (md.vp/
// md.ov/md.sceneSphere/md.sel), mutated only by the gesture/stdin-reader goroutine
// (RunStdinReader's single dispatch loop) — no lock. This file adds the WRITE side: pack
// that state into the VIEW stream's own frame (byte-identical to Buffer.SnapshotState's
// (retired) buildViewFrame layout — see Buffer.BuildViewStreamFrame) and write it to the
// dedicated view fd whenever it changes.
//
// AbcDragCount is the one exception: it is INCREMENTED by a DIFFERENT goroutine (an
// abc-drag recipient's own nodeMover, quantized_move.go's neighborSetCRequantize) than the
// one that WRITES the view frame. Per MODEL.md's explicit no-atomic/no-mutex directive,
// this is message-passing: a recipient sends on abcDragCh (non-blocking — a full channel
// just drops that one count-observability tick, same "no delivery guarantee" shape as
// every other cross-goroutine bridge here), and RunStdinReader's own select loop drains it,
// incrementing its OWN plain int (abcDragCount, touched by no other goroutine) and
// re-emitting the view frame.

package Wiring

import (
	"encoding/binary"
	"io"

	T "github.com/dtauraso/wirefold/Trace"
)

// ViewFrameBuilder packs the VIEW stream's own frame payload from plain values (mirrors
// nodeMover/edgeMover's buildFrame closures — injected from main.go, which imports Buffer,
// so this package stays Buffer-independent). tick is a purely local sequence counter (not
// shared with any other stream). events are this goroutine's OWN resolved events since the
// last write.
type ViewFrameBuilder func(tick uint32,
	camPX, camPY, camPZ, camR, camPosTheta, camPosPhi, camUpTheta, camUpPhi float32,
	sceneTori, scenePoles, nodePoles, selSpherePoles, handholds, labelsGlobal, overlaysVis, doubleLinks uint8,
	abcDragCount uint32,
	sceneCX, sceneCY, sceneCZ, sceneRadius float32,
	events []RowEvent,
) []byte

// SetViewStream installs the VIEW stream's write side: out is the dedicated view fd (nil =
// no dedicated stream — the fallback, Buffer.SnapshotState's own fd-3 embed keeps serving
// camera/overlay/scene) and buildFrame packs this goroutine's own frame bytes
// (Buffer.BuildViewStreamFrame, injected from main.go). Call once at startup, before any
// gesture/edit reaches RunStdinReader, when WIREFOLD_STREAM_FDS carries a "view" entry.
func (md *MoveDispatch) SetViewStream(out io.Writer, buildFrame ViewFrameBuilder) {
	md.viewOut = out
	md.viewBuildFrame = buildFrame
	md.abcDragCh = make(chan struct{}, 64)
}

// sendAbcDragTick is called by an abc-drag RECIPIENT's own nodeMover goroutine
// (quantized_move.go's neighborSetCRequantize) to signal the view-owner goroutine that one
// more abc-drag occurred. Non-blocking (a nil channel — SetViewStream never ran — or a full
// one just drops this tick; see abcDragCh's doc comment). Never touches abcDragCount
// directly: only DrainAbcDragChan (the view-owner goroutine) does that.
func (md *MoveDispatch) sendAbcDragTick() {
	select {
	case md.abcDragCh <- struct{}{}:
	default:
	}
}

// DrainAbcDragChan drains every pending abc-drag tick non-blockingly, incrementing
// abcDragCount once per tick (this goroutine's OWN plain int — no atomic, no lock: only
// RunStdinReader's single dispatch goroutine ever touches it) and reporting how many were
// drained (0 = nothing pending, or no dedicated view stream — the caller's cue to skip the
// re-emit). Call from RunStdinReader's own select loop whenever it wakes on abcDragCh.
func (md *MoveDispatch) DrainAbcDragChan() int {
	n := 0
	for {
		select {
		case <-md.abcDragCh:
			md.abcDragCount++
			n++
		default:
			return n
		}
	}
}

// EmitLayoutLinkViewEvent writes one LayoutLink event onto this goroutine's own VIEW
// frame (Step C, per-owner-buffer-rows.md): LayoutLink is a load-time-once topology fact
// with no live per-goroutine owner to stream it from, so it is emitted once here rather
// than from a per-tick owner. Exported so main.go (package main, which already
// resolves nodeRow/targetRow via md.NodeRowFor) can call it directly after wiring
// SetViewStream, mirroring the Seed* idiom Step A used for NodeGeometry/Geometry.
func (md *MoveDispatch) EmitLayoutLinkViewEvent(nodeRow, targetRow int32) {
	md.emitViewFrame([]RowEvent{{Kind: T.KindLayoutLink, NodeRow: nodeRow, PortRow: -1, TargetRow: targetRow, TargetPortRow: -1, EdgeRow: -1}})
}

// emitViewFrame packs and writes the current camera/overlay/scene-sphere state as this
// goroutine's own VIEW frame, if the dedicated stream is active (nil viewBuildFrame — no
// WIREFOLD_STREAM_FDS "view" entry — is the required no-op fallback). events carries
// whatever this call's OWN state change should log (camera/select/hover/scene-sphere/
// abc-drag/overlay-toggle) — resolved to buffer rows by the caller, mirroring
// owner_events.go's pattern for every other per-owner stream.
func (md *MoveDispatch) emitViewFrame(events []RowEvent) {
	if md.viewBuildFrame == nil {
		return
	}
	md.viewTick++
	v := md.vp.viewpoint
	sc := md.sceneSphere
	frame := md.viewBuildFrame(md.viewTick,
		float32(v.pivot.X), float32(v.pivot.Y), float32(v.pivot.Z), float32(v.r),
		float32(v.pos.Theta), float32(v.pos.Phi), float32(v.up.Theta), float32(v.up.Phi),
		boolU8(md.ov.sceneToriVisible), boolU8(md.ov.scenePolesVisible), boolU8(md.ov.nodePolesVisible),
		boolU8(md.ov.selSpherePolesVisible), boolU8(md.ov.handholdsVisible), boolU8(md.ov.labelsGlobalVisible),
		boolU8(md.ov.overlaysVisible), boolU8(md.ov.doubleLinksVisible),
		md.abcDragCount,
		float32(sc.Center.X), float32(sc.Center.Y), float32(sc.Center.Z), float32(sc.Radius),
		events,
	)
	if md.viewOut == nil {
		return
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(frame)))
	// Fire-and-forget, same reasoning as every other stream's frame write in this codebase:
	// no delivery guarantee on this channel, errors ignored.
	_, _ = md.viewOut.Write(hdr[:])
	_, _ = md.viewOut.Write(frame)
}
