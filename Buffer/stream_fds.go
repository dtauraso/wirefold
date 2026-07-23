// Buffer/stream_fds.go — the fd-ALLOCATION contract for the "N inherited stdio pipes,
// one per emitting goroutine" transport (memory/feedback_no_single_writer_bridge.md).
//
// fd 3 (WIREFOLD_BUF_OUT_FD, see main.go) was the FIRST such pipe — the ext host's
// `stdio` spawn array already carries it as its 4th entry. This file generalizes that
// same mechanism to fd 4, 5, … : NOT a socket, NOT a server, NOT a handshake protocol —
// just more inherited pipes, agreed by POSITION.
//
// Contract (both sides agree on this, no runtime negotiation):
//
//   - The ext host knows the topology from the spec it already holds. At spawn it
//     computes, per stream KIND (a short string: "view" today; future kinds add more
//     entries), a BASE fd, and passes it to Go via one env var:
//
//     WIREFOLD_STREAM_FDS = "view:4"        (one kind)
//     WIREFOLD_STREAM_FDS = "view:4,foo:8"  (comma-separated "kind:baseFd" pairs)
//
//     Empty/unset ⇒ no dedicated stream fd for ANY kind ⇒ every stream FALLS BACK to
//     its pre-existing fd-3 path (headless tests, non-extension launches, or a launch
//     that simply doesn't wire the extra pipe).
//
//   - Ordering convention within a kind: fd = baseFd[kind] + rowIndex, where rowIndex is
//     the STABLE load/seed order that already assigns buffer rows (see main.go's
//     md.NodeSeeds()/md.EdgeSeeds() loop). VIEW is a singleton stream (one gesture/
//     MoveDispatch goroutine owns camera+overlay+scene network-wide) — its rowIndex is
//     always 0, so its fd is exactly baseFd["view"].
//
//   - The ext host extends its `stdio` spawn array to include one "pipe" entry per
//     allocated fd (indices 0..3 unchanged; new entries at 4, 5, … per kind/row) and
//     attaches a reader per fd. Those readers frame `[len:u32][payload]` — SAME framing
//     as fd 3's splitFrames — but carry NO leading tag byte: the fd POSITION already
//     identifies the kind, so there is nothing left to discriminate inside the frame.
//
// ParseStreamFDs / StreamFDs.FD are the Go-side half of this contract: the owner
// (main.go, for the view stream) resolves its own fd number, opens it via os.NewFile,
// and gets nil back when the env var doesn't name its kind — the required dual-path
// fallback (see NewSnapshotState/SetViewOut in snapshot.go, and buildSnapshot/
// buildViewFrame in pack.go for the resulting either/or on the WRITE side).
package Buffer

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// StreamKindView is the singleton view stream (see this file's header comment).
const StreamKindView = "view"

// StreamKindEdge is the per-edgeMover stream kind: one dedicated fd PER EDGE ROW
// (rowIndex = that edge's stable seed-order row, matching Buffer's Edge block row order —
// see nodes/Wiring's MoveDispatch.SetEdgeStreams and main.go). Each edgeMover goroutine
// writes ITS OWN edge geometry + its wire's own live in-flight beads as one combined
// frame (Buffer.BuildEdgeStreamFrame) to fd = baseFd["edge"] + edgeRow — no sub-tag byte,
// no shared writer (memory/feedback_no_single_writer_bridge.md).
const StreamKindEdge = "edge"

// StreamKindNode is the per-nodeMover stream kind: one dedicated fd PER NODE ROW
// (rowIndex = that node's stable seed-order row, matching Buffer's Node block row order —
// see nodes/Wiring's MoveDispatch.SetNodeStreams and main.go). Each nodeMover goroutine
// writes ITS OWN node geometry + ports + label as one combined frame
// (Buffer.BuildNodeStreamFrame) to fd = baseFd["node"] + nodeRow.
const StreamKindNode = "node"

// StreamKindInterior is the per-node-Update-loop stream kind: one dedicated fd PER NODE
// ROW (same row order as StreamKindNode), written by that node's OWN Update goroutine
// (the SECOND emitting goroutine per node, alongside its nodeMover) whenever its interior
// beads change (Buffer.BuildInteriorStreamFrame) to fd = baseFd["interior"] + nodeRow.
const StreamKindInterior = "interior"

// StreamFDs is the parsed WIREFOLD_STREAM_FDS env var: kind name -> base fd number.
type StreamFDs map[string]int

// ParseStreamFDs parses "kind:baseFd,kind:baseFd,…" into a StreamFDs map. A malformed
// entry (bad int, missing colon) is skipped rather than aborting the whole parse — a
// typo in one future kind's entry should not take down the view stream (or vice versa).
// Empty input returns an empty (non-nil) map, which FD always reports as "not found",
// i.e. every stream falls back to fd 3.
func ParseStreamFDs(env string) StreamFDs {
	out := StreamFDs{}
	if env == "" {
		return out
	}
	for _, part := range strings.Split(env, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(kv[1]))
		if err != nil {
			continue
		}
		out[strings.TrimSpace(kv[0])] = n
	}
	return out
}

// FD resolves (kind, row) to its inherited-pipe fd number per this file's ordering
// convention (fd = baseFd[kind] + row), or ok=false if this StreamFDs has no base fd
// for kind (⇒ caller falls back to its pre-existing fd-3 path).
func (m StreamFDs) FD(kind string, row int) (int, bool) {
	base, ok := m[kind]
	if !ok {
		return 0, false
	}
	return base + row, true
}

// Open resolves (kind, row) via FD and, if present, opens the inherited pipe as an
// *os.File ready to write framed `[len:u32][payload]` records (no tag byte — the fd
// position already identifies the kind). Returns nil, false if this StreamFDs has no
// entry for kind — the required fallback signal (see this file's header comment).
func (m StreamFDs) Open(kind string, row int) (*os.File, bool) {
	fdNum, ok := m.FD(kind, row)
	if !ok {
		return nil, false
	}
	return os.NewFile(uintptr(fdNum), fmt.Sprintf("%s-fd%d", kind, fdNum)), true
}
