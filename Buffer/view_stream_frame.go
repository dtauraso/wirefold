// Buffer/view_stream_frame.go — the VIEW stream's dedicated-frame packer (see
// Buffer/stream_fds.go's StreamKindView doc comment and
// memory/feedback_no_single_writer_bridge.md). Step C of retiring Buffer.SnapshotState's
// central accumulator (memory/feedback_no_single_writer_bridge.md): the
// gesture/stdin-reader goroutine (nodes/Wiring's MoveDispatch) already owns camera/overlay/
// scene-sphere/selection/hover state — it now WRITES this frame itself, directly, instead
// of routing that state through Buffer.SnapshotState's Trace-drain accumulator.
//
// BuildViewStreamFrame produces the BYTE-IDENTICAL layout Buffer.SnapshotState.
// buildViewFrame already produces (same SetCameraRow/SetOverlayRow/SetSceneRow column
// writers, same BuildEventsSection trailer) — built from plain values, mirroring
// BuildNodeStreamFrame/BuildEdgeStreamFrame's shape, so the emitting side (nodes/Wiring)
// needs only this one plain function, injected from main.go (which imports Buffer), to
// stay Buffer-independent itself.
package Buffer

import "encoding/binary"

// BuildViewStreamFrame packs the VIEW stream's own frame payload (no outer tag byte — the
// fd position already identifies the stream):
//
//	[tick:u32]
//	Camera  BufCameraStride bytes  (SAME SetCameraRow column writer buildViewFrame uses)
//	Overlay BufOverlayStride bytes (SAME SetOverlayRow column writer; overlay carries
//	        AbcDragCount too — see OverlayRow's doc comment)
//	Scene   BufSceneStride bytes   (SAME SetSceneRow column writer)
//	EVENTS section (BuildEventsSection)
func BuildViewStreamFrame(tick uint32,
	camPX, camPY, camPZ, camR, camPosTheta, camPosPhi, camUpTheta, camUpPhi float32,
	overlay OverlayRow,
	sceneCX, sceneCY, sceneCZ, sceneRadius float32,
	events []StreamEvent,
) []byte {
	buf := make([]byte, BufViewFrameHeaderSize+BufCameraStride+BufOverlayStride+BufSceneStride)
	binary.LittleEndian.PutUint32(buf[0:], tick)
	off := BufViewFrameHeaderSize
	SetCameraRow(buf[off:], camPX, camPY, camPZ, camR, camPosTheta, camPosPhi, camUpTheta, camUpPhi)
	off += BufCameraStride
	SetOverlayRow(buf[off:], overlay)
	off += BufOverlayStride
	SetSceneRow(buf[off:], sceneCX, sceneCY, sceneCZ, sceneRadius)
	return append(buf, BuildEventsSection(events)...)
}
