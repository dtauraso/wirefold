// Buffer/frame_tags.go — SYNTHETIC ext-host-side tags for the dedicated per-owner stream
// frames (memory/feedback_no_single_writer_bridge.md, Buffer/stream_fds.go).
//
// This is deliberately NOT part of the generated column-layout schema
// (Buffer/layout.go / buffer_layout_gen.go / buffer-layout.ts): it is not a column
// inside a block, it is envelope-level plumbing. It is hand-authored here and mirrored
// by hand in tools/topology-vscode/src/schema/frame-tags.ts — mirroring the existing
// input_codec.go / input-layout.ts split (binary wire ENVELOPE constants live
// hand-paired, not generated).
//
// Every dedicated stream fd (view/edge/node/interior) carries frames as
// [len:u32-LE][payload] with NO tag byte on the wire — the fd POSITION already
// identifies which stream/row it is (see Buffer/stream_fds.go). The four constants
// below exist ONLY so the ext host can relay a decoded frame to the webview under one
// uniform "buffer-snapshot" message shape (tag + optional row), letting the render
// tree route by cell without a second message shape. They are NEVER written as a wire
// tag byte by Go.
//
//   - BufBlockTagView: a decoded VIEW-stream frame (camera + overlay + scene-sphere,
//     built by BuildViewStreamFrame). Singleton — no row.
//   - BufBlockTagEdgeStream: a decoded per-edge stream frame (BuildEdgeStreamFrame),
//     plus a `row` (that edge's stable seed-order row).
//   - BufBlockTagNodeStream: a decoded per-node NODE stream frame
//     (BuildNodeStreamFrame — geometry+ports+label), plus a `row` (that node's stable
//     seed-order row).
//   - BufBlockTagInteriorStream: a decoded per-node INTERIOR stream frame
//     (BuildInteriorStreamFrame — that node's own interior beads), plus a `row` (same
//     node-row numbering as BufBlockTagNodeStream, a SEPARATE goroutine's fd).
package Buffer

// BufBlockTagView is the SYNTHETIC ext-host-side tag for a decoded VIEW-stream frame,
// relayed to the webview under the "buffer-snapshot" message shape. NEVER written as a
// wire tag byte on the dedicated view fd (that fd's frames carry no tag byte at all —
// see this file's header comment). Mirrored by hand in frame-tags.ts's BUF_BLOCK_TAG_VIEW.
const BufBlockTagView byte = 4

// BufViewFrameHeaderSize is the byte width of the VIEW stream's own frame header on its
// dedicated fd: [tick:u32]. Hand-authored here (envelope-level) rather than generated,
// mirroring BufHeaderSize's split from the generated column-layout schema.
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
