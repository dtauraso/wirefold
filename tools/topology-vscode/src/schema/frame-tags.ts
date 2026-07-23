// frame-tags.ts — hand-authored mirror of Buffer/frame_tags.go's fd-3 frame ENVELOPE
// discriminator. NOT generated (see that file's header comment for why: it's the
// outer frame's tag byte, not a column-layout block). Keep these values in lockstep
// with BufBlockTagScene/BufBlockTagBead by hand, the same way input-layout.ts mirrors
// input_codec.go.
//
// Frame format on fd 3 (see runCommand.ts splitFrames/handleFd3):
//   [len:u32-LE][blockTag:u8][block bytes]
// len counts the tag byte plus the block bytes. Four tag values exist today:
//
//   - BUF_BLOCK_TAG_SCENE: the combined snapshot (layout-link/camera/overlay/scene/event
//     blocks). No longer carries beads, the node-owner-group blocks, or the Edge block +
//     edge-label bytes (see BUF_HEADER_SIZE's comment in buffer-layout.ts).
//   - BUF_BLOCK_TAG_BEAD: the Bead block ALONE, in its own self-contained per-tick
//     frame. Its payload layout is:
//
//     BUF_BEAD_HEADER_SIZE (8) bytes: [tick:u32][beadCount:u32]
//     Bead                    beadCount × BEAD_STRIDE bytes (same Bead block columns
//                             as before — buffer-layout.ts's BEAD_* — unchanged)
//
//   - BUF_BLOCK_TAG_NODE: the Node/Interior/Port blocks + their string sections (node
//     Label bytes, Port-name bytes) ALONE, in their own self-contained frame. These
//     three blocks share ONE owner group (the node movers), so they travel together.
//     Its payload layout is:
//
//     BUF_NODE_FRAME_HEADER_SIZE (20) bytes: [tick:u32][nodeCount:u32][portCount:u32]
//       [labelBytesCount:u32][portNameBytesCount:u32]
//     Node      nodeCount × NODE_STRIDE bytes
//     Interior  nodeCount × INTERIOR_SLOTS_PER_NODE × INTERIOR_STRIDE bytes (fixed slots
//               per node — no separate count; length derives from nodeCount)
//     Port      portCount × PORT_STRIDE bytes (flattened over nodes in node-row order)
//     Label     labelBytesCount bytes (node labels' UTF-8 bytes, node-row order)
//     PortName  portNameBytesCount bytes (port names' UTF-8 bytes, flattened port-row order)
//
//     The scene frame's LayoutLink block still packs SrcNodeRow/DstNodeRow as node-row
//     indices — those rows resolve against THIS frame's Node block (both frames are
//     built from the same Go SnapshotState in the same emitSnapshot call and share the
//     same stable node-row order).
//
//   - BUF_BLOCK_TAG_EDGE: the Edge block ALONE + its EdgeLabel bytes section, in its own
//     self-contained frame. The Edge block carries NO endpoint coordinates: it
//     references its two port rows (SrcPortRow/DstPortRow), which resolve against THIS
//     SAME TICK's NODE frame's Port block — the endpoint's world position lives ONLY
//     there (node-owned), so a fast drag can never composite a fresh Node frame against
//     a stale Edge-block endpoint (the tear this replaces). Its payload layout is:
//
//     BUF_EDGE_FRAME_HEADER_SIZE (12) bytes: [tick:u32][edgeCount:u32][edgeLabelBytesCount:u32]
//     Edge      edgeCount × EDGE_STRIDE bytes
//     EdgeLabel edgeLabelBytesCount bytes (edge labels' UTF-8 bytes, edge-row order)
//
//   - BUF_BLOCK_TAG_VIEW: SYNTHETIC ext-host-side tag for a decoded VIEW-stream frame
//     (camera + overlay + scene-sphere), the first stream migrated OFF fd 3 onto its
//     own dedicated inherited pipe (see runCommand.ts's stream-fd allocation and
//     Buffer/stream_fds.go — memory/feedback_no_single_writer_bridge.md). The wire bytes
//     on that dedicated fd carry NO tag byte (the fd POSITION identifies the stream);
//     this tag exists only so the ext host can relay a decoded view frame to the
//     webview under the SAME "buffer-snapshot" message shape as the fd-3 tags above,
//     extending the existing tag-routed-cell pattern to a fifth cell instead of adding a
//     second message shape. Its payload layout (dedicated-fd wire bytes, no tag):
//
//     BUF_VIEW_FRAME_HEADER_SIZE (4) bytes: [tick:u32]
//     Camera   CAMERA_STRIDE bytes
//     Overlay  OVERLAY_STRIDE bytes
//     Scene    SCENE_STRIDE bytes
//
// This is the protocol foundation for eventually splitting the rest of the single
// content buffer into N per-block buffers, each streamed as its own tagged frame; Bead,
// Node, and Edge above are three of that series; View is the first to move onto its own
// dedicated fd rather than just its own tag within fd 3 (do not add a further tag value
// to the fd-3 vocabulary until the next such split actually lands).

/** The fd-3 block tag for the combined content-buffer snapshot (everything except beads,
 * the node-owner-group blocks, and the Edge block). */
export const BUF_BLOCK_TAG_SCENE = 0;

/** The fd-3 block tag for the self-contained per-tick Bead frame. See this file's header
 * comment for its payload layout. */
export const BUF_BLOCK_TAG_BEAD = 1;

/** The fd-3 block tag for the self-contained Node/Interior/Port frame (+ Label/PortName
 * bytes). See this file's header comment for its payload layout. */
export const BUF_BLOCK_TAG_NODE = 2;

/** The fd-3 block tag for the self-contained Edge frame (+ EdgeLabel bytes). See this
 * file's header comment for its payload layout. */
export const BUF_BLOCK_TAG_EDGE = 3;

/** Byte width of the Bead frame's own header: [tick:u32][beadCount:u32]. Hand-authored
 * (envelope-level), mirroring BUF_HEADER_SIZE's split from the generated column-layout schema. */
export const BUF_BEAD_HEADER_SIZE = 8;

/** Byte width of the Node frame's own header:
 * [tick:u32][nodeCount:u32][portCount:u32][labelBytesCount:u32][portNameBytesCount:u32].
 * Hand-authored (envelope-level), mirroring BUF_BEAD_HEADER_SIZE/BUF_HEADER_SIZE's split
 * from the generated column-layout schema. */
export const BUF_NODE_FRAME_HEADER_SIZE = 20;

/** Byte width of the Edge frame's own header: [tick:u32][edgeCount:u32][edgeLabelBytesCount:u32].
 * Hand-authored (envelope-level), mirroring BUF_NODE_FRAME_HEADER_SIZE/BUF_BEAD_HEADER_SIZE/
 * BUF_HEADER_SIZE's split from the generated column-layout schema. */
export const BUF_EDGE_FRAME_HEADER_SIZE = 12;

/** SYNTHETIC ext-host-side tag for a decoded VIEW-stream frame (camera+overlay+scene),
 * relayed to the webview under the same message shape as the fd-3 tags. NEVER a wire
 * tag byte on the dedicated view fd itself — see this file's header comment. Mirrors
 * Buffer/frame_tags.go's BufBlockTagView. */
export const BUF_BLOCK_TAG_VIEW = 4;

/** Byte width of the VIEW stream's own frame header on its dedicated fd: [tick:u32].
 * Hand-authored (envelope-level), mirroring BUF_EDGE_FRAME_HEADER_SIZE/.../BUF_HEADER_SIZE's
 * split from the generated column-layout schema. */
export const BUF_VIEW_FRAME_HEADER_SIZE = 4;

/** SYNTHETIC ext-host-side tag for a decoded per-edge dedicated-stream frame (one edgeMover
 * writes ITS OWN combined edge+bead frame to its own fd — see runCommand.ts's edge-fd
 * range and Buffer/stream_fds.go's StreamKindEdge). NEVER a wire tag byte on the dedicated
 * per-edge fd itself (the fd POSITION already identifies which edge). Relayed to the
 * webview under the same "buffer-snapshot" shape as BUF_BLOCK_TAG_VIEW, plus a `row` field
 * (there are many edge streams, not a singleton). Mirrors Buffer/frame_tags.go's
 * BufBlockTagEdgeStream. */
export const BUF_BLOCK_TAG_EDGE_STREAM = 5;

/** Byte layout of one edge's combined per-fd frame (Buffer.BuildEdgeStreamFrame), no outer
 * tag: [tick:u32] + one EDGE_STRIDE row (SrcPortRow/DstPortRow/Selected, EdgeLabelOff=0/Len)
 * + that edge's own label bytes (labelLen, from the row) + [beadCount:u32] + beadCount ×
 * BEAD_STRIDE bead rows. Header before the Edge row is just the tick. */
export const BUF_EDGE_STREAM_FRAME_HEADER_SIZE = 4;

/** SYNTHETIC ext-host-side tag for a decoded per-node dedicated-stream frame (one
 * nodeMover writes ITS OWN node geometry + ports + label to its own fd — see
 * runCommand.ts's node-fd range and Buffer/stream_fds.go's StreamKindNode). NEVER a wire
 * tag byte on the dedicated per-node fd itself (the fd POSITION already identifies which
 * node). Relayed to the webview under the same "buffer-snapshot" shape as
 * BUF_BLOCK_TAG_EDGE_STREAM, plus a `row` field (one per node row). Mirrors
 * Buffer/frame_tags.go's BufBlockTagNodeStream (ext-host-only; not carried on any Go wire
 * byte — this numbering only needs to stay distinct from the other synthetic tags here). */
export const BUF_BLOCK_TAG_NODE_STREAM = 6;

/** SYNTHETIC ext-host-side tag for a decoded per-node dedicated INTERIOR-stream frame (that
 * node's OWN Update goroutine writes its interior beads to its own fd — see
 * runCommand.ts's interior-fd range and Buffer/stream_fds.go's StreamKindInterior). NEVER a
 * wire tag byte on the dedicated fd itself. Relayed under the same "buffer-snapshot" shape,
 * plus a `row` field (one per node row, same numbering as BUF_BLOCK_TAG_NODE_STREAM). */
export const BUF_BLOCK_TAG_INTERIOR_STREAM = 7;

/** Byte layout of one node's combined per-fd frame (Buffer.BuildNodeStreamFrame), no outer
 * tag: [tick:u32][portCount:u32][labelLen:u32][portNameBytesCount:u32] + one NODE_STRIDE row
 * (LabelOff=0 into this frame's own label bytes) + labelLen label bytes + portCount ×
 * PORT_STRIDE port rows (each row's NodeRow column already the global node row) +
 * portNameBytesCount port-name bytes. */
export const BUF_NODE_STREAM_FRAME_HEADER_SIZE = 16;

/** Byte layout of one node's INTERIOR per-fd frame (Buffer.BuildInteriorStreamFrame), no
 * outer tag: [tick:u32] followed by a FIXED INTERIOR_SLOTS_PER_NODE × INTERIOR_STRIDE bytes
 * (no count — the decoder derives the length from the fixed per-node slot count, same as
 * the shared fd-3 Interior block). */
export const BUF_INTERIOR_STREAM_FRAME_HEADER_SIZE = 4;
