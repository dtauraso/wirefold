// frame-tags.ts — hand-authored mirror of Buffer/frame_tags.go's fd-3 frame ENVELOPE
// discriminator. NOT generated (see that file's header comment for why: it's the
// outer frame's tag byte, not a column-layout block). Keep this value in lockstep
// with BufBlockTagScene by hand, the same way input-layout.ts mirrors input_codec.go.
//
// Frame format on fd 3 (see runCommand.ts splitFrames/handleFd3):
//   [len:u32-LE][blockTag:u8][block bytes]
// len counts the tag byte plus the block bytes. Today there is exactly one tag
// value, BUF_BLOCK_TAG_SCENE, carrying the whole combined snapshot. This is the
// protocol foundation for later per-block buffers; do not add a second tag value
// until that split actually lands.

/** The (currently sole) fd-3 block tag: the whole combined content-buffer snapshot. */
export const BUF_BLOCK_TAG_SCENE = 0;
