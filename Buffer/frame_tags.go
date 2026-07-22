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
// tag byte plus the block bytes. Today there is exactly one tag value,
// BufBlockTagScene, carrying the whole combined snapshot (see snapshot.go). This is
// the protocol foundation for later splitting the single content buffer into N
// per-block buffers, each streamed as its own tagged frame; do not add a second tag
// value until that split actually lands.
package Buffer

// BufBlockTagScene is the (currently sole) fd-3 block tag: the whole combined
// content-buffer snapshot, exactly as built by buildSnapshot today.
const BufBlockTagScene byte = 0
