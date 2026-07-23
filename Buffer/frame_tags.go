// Buffer/frame_tags.go — the fd-3 frame ENVELOPE discriminator.
//
// This is deliberately NOT part of the generated column-layout schema
// (Buffer/layout.go / buffer_layout_gen.go / buffer-layout.ts): it is not a column
// inside a block, it is the outer frame's own tag byte, sitting alongside the u32
// length prefix. It is hand-authored here and mirrored by hand in
// tools/topology-vscode/src/schema/frame-tags.ts — mirroring the existing
// input_codec.go / input-layout.ts split (binary wire ENVELOPE constants live
// hand-paired, not generated).
//
// Frame format on fd 3: [len:u32-LE][blockTag:u8][block bytes] where len counts the
// tag byte plus the block bytes. Four tag values exist today:
//
//   - BufBlockTagScene: the combined snapshot (layout-link/camera/overlay/scene/event
//     blocks — see snapshot.go), built by buildSnapshot. No longer carries beads
//     (BufBlockTagBead), the node-owner-group blocks (BufBlockTagNode), or the Edge
//     block + edge-label bytes (BufBlockTagEdge).
//
//   - BufBlockTagBead: the Bead block ALONE, in its own self-contained frame, built by
//     buildBeadFrame (Buffer/pack.go). Beads churn every tick, independent of the rest
//     of scene state, so they are split into their own per-tick frame rather than
//     riding the scene frame. Its payload layout is:
//
//     BufBeadHeaderSize (8) bytes: [tick:u32][beadCount:u32]
//     Bead                  beadCount × BufBeadStride bytes (same Bead block columns
//     as before — Buffer/layout.go's bufLayoutBead — unchanged)
//
//   - BufBlockTagNode: the Node/Interior/Port blocks + their string sections (node Label
//     bytes, Port-name bytes) ALONE, in their own self-contained frame, built by
//     buildNodeFrame (Buffer/pack.go). These three blocks share ONE owner group (the
//     node movers), so they travel together, split out of the scene frame the same way
//     the Bead block was. Its payload layout is:
//
//     BufNodeFrameHeaderSize (20) bytes: [tick:u32][nodeCount:u32][portCount:u32]
//     [labelBytesCount:u32][portNameBytesCount:u32]
//     Node      nodeCount × BufNodeStride bytes
//     Interior  nodeCount × BufInteriorSlotsPerNode × BufInteriorStride bytes (fixed
//     slots per node — no separate count; length derives from nodeCount)
//     Port      portCount × BufPortStride bytes (flattened over nodes in node-row order)
//     Label     labelBytesCount bytes (node labels' UTF-8 bytes, node-row order)
//     PortName  portNameBytesCount bytes (port names' UTF-8 bytes, flattened port-row order)
//
//     The scene frame's LayoutLink block still packs SrcNodeRow/DstNodeRow as node-row
//     indices (nodeRowIndex) — those rows resolve against THIS frame's Node block, since
//     both frames are built from the same SnapshotState in the same emitSnapshot call and
//     share the same stable node-row order (node id insertion order).
//
//   - BufBlockTagEdge: the Edge block ALONE + its EdgeLabel bytes section, in its own
//     self-contained frame, built by buildEdgeFrame. The Edge block carries NO endpoint
//     coordinates — see bufLayoutEdge's doc comment in layout.go for why (the endpoint-
//     tear fix): it references its two port rows (SrcPortRow/DstPortRow), which resolve
//     against the NODE frame's Port block (same reasoning as LayoutLink's SrcNodeRow/
//     DstNodeRow above — both frames share one SnapshotState/emitSnapshot call). Its
//     payload layout is:
//
//     BufEdgeFrameHeaderSize (12) bytes: [tick:u32][edgeCount:u32][edgeLabelBytesCount:u32]
//     Edge      edgeCount × BufEdgeStride bytes
//     EdgeLabel edgeLabelBytesCount bytes (edge labels' UTF-8 bytes, edge-row order)
//
//   - BufBlockTagView: the VIEW stream's own frame (camera + overlay + scene-sphere),
//     built by buildViewFrame. This is the first stream migrated OFF fd 3 onto its own
//     dedicated inherited pipe (see StreamFDs / main.go), per the no-single-writer-bridge
//     rule (memory/feedback_no_single_writer_bridge.md): the view/gesture goroutine gets
//     its own binary channel to TS instead of riding the fd-3 scene frame. When the
//     dedicated view fd is ACTIVE, the wire bytes on THAT fd carry NO tag byte — the fd
//     POSITION already identifies the stream (see StreamFDs' doc comment) — so
//     BufBlockTagView is never written to the wire in that mode; it exists only as the
//     synthetic tag the ext host attaches when relaying a decoded view frame onward to
//     the webview (mirrored in frame-tags.ts), so the webview's existing
//     tag-routed-cell pattern (scene/bead/node/edge) extends uniformly to a fifth cell
//     without a second message shape. When the dedicated view fd is NOT active (env
//     unset — headless tests, non-extension launch), camera/overlay/scene stay embedded
//     in the fd-3 SCENE frame exactly as before (the fallback path) and BufBlockTagView
//     is unused. Its payload layout (dedicated-fd wire bytes, no tag):
//
//     BufViewFrameHeaderSize (4) bytes: [tick:u32]
//     Camera   BufCameraStride bytes
//     Overlay  BufOverlayStride bytes
//     Scene    BufSceneStride bytes
//
// This is the protocol foundation for eventually splitting the rest of the single
// content buffer into N per-block buffers, each streamed as its own tagged frame; Bead,
// Node, and Edge above are three of that series; View is the first to move onto its own
// dedicated fd rather than just its own tag within fd 3 (do not add a further tag value
// to the fd-3 vocabulary until the next such split actually lands).
package Buffer

// BufBlockTagScene is the fd-3 block tag for the combined content-buffer snapshot
// (everything except beads, the node-owner-group blocks, and the Edge block), built by
// buildSnapshot.
const BufBlockTagScene byte = 0

// BufBlockTagBead is the fd-3 block tag for the self-contained per-tick Bead frame,
// built by buildBeadFrame. See this file's header comment for its payload layout.
const BufBlockTagBead byte = 1

// BufBlockTagNode is the fd-3 block tag for the self-contained Node/Interior/Port frame
// (+ Label/PortName bytes), built by buildNodeFrame. See this file's header comment for
// its payload layout.
const BufBlockTagNode byte = 2

// BufBlockTagEdge is the fd-3 block tag for the self-contained Edge frame (+ EdgeLabel
// bytes), built by buildEdgeFrame. See this file's header comment for its payload layout.
const BufBlockTagEdge byte = 3

// BufBeadHeaderSize is the byte width of the Bead frame's own header: [tick:u32][beadCount:u32].
// Hand-authored here (envelope-level, like the rest of this file) rather than generated,
// mirroring BufHeaderSize's split from the generated column-layout schema.
const BufBeadHeaderSize = 8

// BufNodeFrameHeaderSize is the byte width of the Node frame's own header:
// [tick:u32][nodeCount:u32][portCount:u32][labelBytesCount:u32][portNameBytesCount:u32].
// Hand-authored here (envelope-level, like the rest of this file) rather than generated,
// mirroring BufBeadHeaderSize/BufHeaderSize's split from the generated column-layout schema.
const BufNodeFrameHeaderSize = 20

// BufEdgeFrameHeaderSize is the byte width of the Edge frame's own header:
// [tick:u32][edgeCount:u32][edgeLabelBytesCount:u32]. Hand-authored here (envelope-level,
// like the rest of this file) rather than generated, mirroring
// BufNodeFrameHeaderSize/BufBeadHeaderSize/BufHeaderSize's split from the generated
// column-layout schema.
const BufEdgeFrameHeaderSize = 12

// BufBlockTagView is the SYNTHETIC ext-host-side tag for a decoded VIEW-stream frame,
// relayed to the webview under the same "buffer-snapshot" message shape as the fd-3
// tags above so the webview's existing tag-routed-cell pattern extends to a fifth cell.
// It is NEVER written as a wire tag byte on the dedicated view fd (that fd's frames
// carry no tag byte at all — see this file's header comment) and is unrelated to the
// fd-3 tag vocabulary (BufBlockTagScene/Bead/Node/Edge, values 0-3); it exists purely so
// Go and TS agree on ONE numeric value for "this is the view frame" when the ext host
// relays it. Mirrored by hand in frame-tags.ts's BUF_BLOCK_TAG_VIEW.
const BufBlockTagView byte = 4

// BufViewFrameHeaderSize is the byte width of the VIEW stream's own frame header on its
// dedicated fd: [tick:u32]. Hand-authored here (envelope-level), mirroring
// BufEdgeFrameHeaderSize/BufNodeFrameHeaderSize/BufBeadHeaderSize/BufHeaderSize's split
// from the generated column-layout schema.
const BufViewFrameHeaderSize = 4

// BufBlockTagEdgeStream is the SYNTHETIC ext-host-side tag for a decoded per-edge stream
// frame (see Buffer/stream_fds.go's StreamKindEdge / Buffer/edge_stream_frame.go), relayed
// to the webview under the same "buffer-snapshot" message shape as BufBlockTagView, PLUS a
// `row` field (the edge's stable row) so the webview can route it to the right per-edge
// cell — there are many edge streams (one per edge), unlike VIEW's singleton row. NEVER a
// wire tag byte: the dedicated per-edge fd's frames carry no tag byte at all (the fd
// POSITION already identifies which edge — see stream_fds.go). Mirrored by hand in
// frame-tags.ts's BUF_BLOCK_TAG_EDGE_STREAM.
const BufBlockTagEdgeStream byte = 5

// BufBlockTagNodeStream is the SYNTHETIC ext-host-side tag for a decoded per-node stream
// frame (see Buffer/stream_fds.go's StreamKindNode / Buffer/node_stream_frame.go's
// BuildNodeStreamFrame), relayed under the same "buffer-snapshot" shape as
// BufBlockTagEdgeStream, plus a `row` field (the node's stable row). NEVER a wire tag byte:
// the dedicated per-node fd's frames carry no tag byte at all (the fd POSITION already
// identifies which node). Mirrored by hand in frame-tags.ts's BUF_BLOCK_TAG_NODE_STREAM.
const BufBlockTagNodeStream byte = 6

// BufBlockTagInteriorStream is the SYNTHETIC ext-host-side tag for a decoded per-node
// INTERIOR stream frame (see Buffer/stream_fds.go's StreamKindInterior /
// Buffer/node_stream_frame.go's BuildInteriorStreamFrame), relayed under the same shape as
// BufBlockTagNodeStream, plus a `row` field (same node-row numbering). NEVER a wire tag
// byte. Mirrored by hand in frame-tags.ts's BUF_BLOCK_TAG_INTERIOR_STREAM.
const BufBlockTagInteriorStream byte = 7
