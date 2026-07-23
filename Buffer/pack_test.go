// Buffer/pack_test.go — byte-layout tests: block writing, strides, offsets, and
// framing for SnapshotState.BuildSnapshot. Companion to snapshot_test.go, which
// covers the ingest side (Update / on* handlers / event-ingest behavior). Split
// mirrors the production snapshot.go (ingest) / pack.go (pack) split.
//
// Feeds a SnapshotState a sequence of trace events, calls BuildSnapshot, then
// parses the raw bytes back and asserts counts and sampled values match.
// This is the Phase 2 acceptance check: if the snapshot builder, framing, and
// layout constants are consistent the test passes; any mismatch means a bug in
// the column offsets or the builder loop.

package Buffer

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// readF32 decodes one little-endian float32 from buf at off.
func readF32(buf []byte, off int) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(buf[off:]))
}

// readU32 decodes one little-endian uint32 from buf at off.
func readU32(buf []byte, off int) uint32 {
	return binary.LittleEndian.Uint32(buf[off:])
}

// readI32 decodes one little-endian int32 from buf at off.
func readI32(buf []byte, off int) int32 {
	return int32(binary.LittleEndian.Uint32(buf[off:]))
}

// TestEventBlockPopulate asserts the SEND event (a kind not yet decentralized to its own
// owner fd — see decentralizedEventKinds) rides the VIEW frame's trailing EVENTS section,
// NOT the fd-3 scene frame (the EVENT block was retired from the scene frame; memory/
// feedback_no_single_writer_bridge.md). NodeGeometry events are decentralized (their own
// owner fd), so they must NOT appear here.
func TestEventBlockPopulate(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "A", Radius: 1,
		Ports: []T.PortGeom{{Name: "out", IsInput: false, DX: 1}}})
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "B", Radius: 1,
		Ports: []T.PortGeom{{Name: "in", IsInput: true, DX: -1}}})
	s.Update(T.Event{Kind: T.KindSend, Node: "A", Port: "out", Value: 7,
		ArcLength: 12.5, SimLatencyMs: 33.0, Target: "B", TargetHandle: "in"})

	// The fd-3 scene frame no longer carries an EVENT block at all.
	snap := s.BuildSnapshot()
	if len(snap) != BufHeaderSize+BufCameraStride+BufOverlayStride+BufSceneStride {
		t.Fatalf("scene frame length: got %d, want a frame with no EVENT block", len(snap))
	}

	view := s.buildViewFrame()
	eventsOff := BufViewFrameHeaderSize + BufCameraStride + BufOverlayStride + BufSceneStride
	eventCount := int(readU32(view, eventsOff))
	// The two node-geometry events are decentralized (their own owner fd) and are never
	// recorded into this fallback bucket; only the send (not yet decentralized) lands here.
	if eventCount != 1 {
		t.Fatalf("eventCount: got %d, want 1 (send only; node-geometry is decentralized)", eventCount)
	}
	eventOff := eventsOff + 4

	// Find the send row (kind == index of "send" in TraceEventKinds).
	sendKind := -1
	for i, k := range T.TraceEventKinds {
		if k == T.KindSend {
			sendKind = i
		}
	}
	found := false
	for r := 0; r < eventCount; r++ {
		base := eventOff + r*BufEventStride
		if int(view[base+BufEventColKind]) != sendKind {
			continue
		}
		found = true
		// A=row0, out=port row0; B=row1, in=port row1.
		if got := readI32(view, base+BufEventColNodeRow); got != 0 {
			t.Errorf("send NodeRow: got %d, want 0 (A)", got)
		}
		if got := readI32(view, base+BufEventColPortRow); got != 0 {
			t.Errorf("send PortRow: got %d, want 0 (A/out)", got)
		}
		if got := readI32(view, base+BufEventColTargetRow); got != 1 {
			t.Errorf("send TargetRow: got %d, want 1 (B)", got)
		}
		if got := readI32(view, base+BufEventColTargetPortRow); got != 1 {
			t.Errorf("send TargetPortRow: got %d, want 1 (B/in)", got)
		}
		if got := readI32(view, base+BufEventColValue); got != 7 {
			t.Errorf("send Value: got %d, want 7", got)
		}
		if got := readF32(view, base+BufEventColArcLength); got != 12.5 {
			t.Errorf("send ArcLength: got %v, want 12.5", got)
		}
		if got := readF32(view, base+BufEventColSimLatencyMs); got != 33.0 {
			t.Errorf("send SimLatencyMs: got %v, want 33", got)
		}
	}
	if !found {
		t.Fatal("no send event row found in EVENT block")
	}
}

// TestSnapshotRoundTrip feeds known events into a SnapshotState, builds a
// snapshot, then parses the bytes and asserts all counts and sampled field
// values are correct and match the layout constants in buffer_layout_gen.go.
func TestSnapshotRoundTrip(t *testing.T) {
	s := NewSnapshotState(nil) // no output; just test the builder

	// Register two nodes via KindNodeGeometry events. Each carries the one port the edge
	// below connects, so SrcPortRow/DstPortRow resolve to real rows (0 and 1 — the flattened
	// port-row order is node-A's ports then node-B's ports).
	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "node-A",
		NX: 1.0, NY: 2.0, NZ: 3.0,
		Radius: 0.5, SphereR: 0.25,
		VRX: 0.0, VRY: 0.0, VRZ: 1.0,
		FRX: 1.0, FRY: 0.0, FRZ: 0.0,
		Ports: []T.PortGeom{{Name: "out", IsInput: false, DX: 1}},
	})
	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "node-B",
		NX: 4.0, NY: 5.0, NZ: 6.0,
		Radius: 0.75, SphereR: 0.375,
		Ports: []T.PortGeom{{Name: "in", IsInput: true, DX: -1}},
	})

	// Register one edge via KindGeometry. Node (source) and Target (dest) carry the
	// edge's endpoint node ids; SrcPort/DstPort carry the endpoint port names — the
	// builder resolves both to buffer rows (node-A=row 0, node-B=row 1; port-A/out=row 0,
	// port-B/in=row 1).
	s.Update(T.Event{
		Kind: T.KindGeometry, Edge: "edge-1",
		Node: "node-A", Target: "node-B",
		SrcPort: "out", DstPort: "in",
		SX: 1.1, SY: 2.2, SZ: 3.3,
		EX: 4.4, EY: 5.5, EZ: 6.6,
	})

	// Set camera.
	s.Update(T.Event{
		Kind: T.KindCamera,
		PX:   10.0, PY: 11.0, PZ: 12.0,
		R: 20.0, PosTheta: 0.5, PosPhi: 1.0,
		UpTheta: 0.25, UpPhi: 0.75,
	})

	// Toggle an overlay flag.
	s.Update(T.Event{Kind: T.KindSceneTori, Visible: true})

	// Inject a send event (recorded into the EVENT block by recordEvent).
	s.Update(T.Event{
		Kind: T.KindSend, Node: "node-A", Port: "out", Value: 42,
		Target: "node-B",
	})

	// Inject a live bead via KindPosition (also triggers a snapshot emit internally,
	// but we call BuildSnapshot separately below to inspect).
	s.Update(T.Event{
		Kind: T.KindPosition, Node: "node-A", Port: "out",
		X: 2.5, Y: 3.5, Z: 4.5, Value: 42, F: 0.6, Bead: 1,
	})

	// Inject recv/arrive events too (they land in the EVENT block only; no per-node columns).
	s.Update(T.Event{Kind: T.KindRecv, Node: "node-A", Port: "in", Value: 42})
	s.Update(T.Event{Kind: T.KindArrive, Node: "node-A", Port: "out", Value: 42, Bead: 2})

	// Build snapshot WITHOUT triggering emit (BuildSnapshot is exported for tests).
	snap := s.BuildSnapshot()
	// The Node/Interior/Port blocks + Label/PortName bytes now live in their own tagged
	// frame (BuildNodeFrame — see BufBlockTagNode); build it alongside the scene frame.
	nodeFrame := s.BuildNodeFrame()
	// The Edge block + edge-label bytes now live in their own tagged frame (BuildEdgeFrame —
	// see BufBlockTagEdge); build it alongside the scene frame too.
	edgeFrame := s.BuildEdgeFrame()

	// ── Header ───────────────────────────────────────────────────────────────
	if len(snap) < BufHeaderSize {
		t.Fatalf("snapshot too short: got %d bytes, want >= %d", len(snap), BufHeaderSize)
	}
	if len(nodeFrame) < BufNodeFrameHeaderSize {
		t.Fatalf("node frame too short: got %d bytes, want >= %d", len(nodeFrame), BufNodeFrameHeaderSize)
	}
	if len(edgeFrame) < BufEdgeFrameHeaderSize {
		t.Fatalf("edge frame too short: got %d bytes, want >= %d", len(edgeFrame), BufEdgeFrameHeaderSize)
	}

	nodeCount := readU32(nodeFrame, 4)
	edgeCount := readU32(edgeFrame, 4)

	if nodeCount != 2 {
		t.Errorf("nodeCount: got %d, want 2", nodeCount)
	}
	if edgeCount != 1 {
		t.Errorf("edgeCount: got %d, want 1", edgeCount)
	}

	// ── Scene size check ─────────────────────────────────────────────────────
	// The EVENT block was retired from this frame (memory/feedback_no_single_writer_bridge.md):
	// the SCENE frame's header now carries only [tick][layoutLinkCount].
	layoutLinkCount := readU32(snap, 4)
	wantSize := BufHeaderSize +
		int(layoutLinkCount)*BufLayoutLinkStride +
		BufCameraStride +
		BufOverlayStride +
		BufSceneStride
	if len(snap) != wantSize {
		t.Errorf("snapshot size: got %d, want %d", len(snap), wantSize)
	}

	// ── Edge frame size check ────────────────────────────────────────────────
	edgeLabelBytesCount := readU32(edgeFrame, 8)
	wantEdgeFrameSize := BufEdgeFrameHeaderSize +
		int(edgeCount)*BufEdgeStride +
		int(edgeLabelBytesCount)
	if len(edgeFrame) != wantEdgeFrameSize {
		t.Errorf("edge frame size: got %d, want %d", len(edgeFrame), wantEdgeFrameSize)
	}

	// ── Node frame size check ────────────────────────────────────────────────
	// One port on each node (the edge's endpoints) and no labels were injected, so the
	// Label section is zero-length while the Port section carries 2 rows.
	portCount := readU32(nodeFrame, 8)
	labelBytesCount := readU32(nodeFrame, 12)
	portNameBytesCount := readU32(nodeFrame, 16)
	if portCount != 2 {
		t.Errorf("portCount: got %d, want 2 (one port per node)", portCount)
	}
	if labelBytesCount != 0 {
		t.Errorf("labelBytesCount: got %d, want 0 (no labels in fixture)", labelBytesCount)
	}
	wantNodeFrameSize := BufNodeFrameHeaderSize +
		int(nodeCount)*BufNodeStride +
		int(nodeCount)*BufInteriorSlotsPerNode*BufInteriorStride +
		int(portCount)*BufPortStride +
		int(labelBytesCount) +
		int(portNameBytesCount)
	if len(nodeFrame) != wantNodeFrameSize {
		t.Errorf("node frame size: got %d, want %d", len(nodeFrame), wantNodeFrameSize)
	}

	// ── Bead frame ───────────────────────────────────────────────────────────
	// Beads no longer ride the scene frame (see BufBlockTagBead); check the dedicated
	// bead frame built by buildBeadFrame instead.
	beadFrame := s.BuildBeadFrame()
	beadCount := readU32(beadFrame, 4)
	if beadCount != 1 {
		t.Errorf("bead frame beadCount: got %d, want 1", beadCount)
	}
	beadOff := BufBeadHeaderSize
	// One bead was injected (Bead=1, node-A/out).
	gotX := readF32(beadFrame, beadOff+BufBeadColX)
	gotY := readF32(beadFrame, beadOff+BufBeadColY)
	gotZ := readF32(beadFrame, beadOff+BufBeadColZ)
	gotVal := readI32(beadFrame, beadOff+BufBeadColValue)
	gotLive := beadFrame[beadOff+BufBeadColLive]

	if gotX != 2.5 {
		t.Errorf("bead.X: got %v, want 2.5", gotX)
	}
	if gotY != 3.5 {
		t.Errorf("bead.Y: got %v, want 3.5", gotY)
	}
	if gotZ != 4.5 {
		t.Errorf("bead.Z: got %v, want 4.5", gotZ)
	}
	if gotVal != 42 {
		t.Errorf("bead.Value: got %v, want 42", gotVal)
	}
	if gotLive != 1 {
		t.Errorf("bead.Live: got %v, want 1", gotLive)
	}

	// ── Node block ───────────────────────────────────────────────────────────
	nodeOff := BufNodeFrameHeaderSize

	// node-A (row 0): geometry only (recv/arrive events live in the EVENT block, not
	// per-node columns).
	nA := nodeOff
	if readF32(nodeFrame, nA+BufNodeColCX) != 1.0 {
		t.Errorf("nodeA.CX: got %v, want 1.0", readF32(nodeFrame, nA+BufNodeColCX))
	}
	if readF32(nodeFrame, nA+BufNodeColRadius) != 0.5 {
		t.Errorf("nodeA.Radius: got %v, want 0.5", readF32(nodeFrame, nA+BufNodeColRadius))
	}
	// Ring-plane normals (vr vertical, fr flat) reach the node columns for SphereRing.
	if readF32(nodeFrame, nA+BufNodeColVRZ) != 1.0 {
		t.Errorf("nodeA.VRZ: got %v, want 1.0", readF32(nodeFrame, nA+BufNodeColVRZ))
	}
	if readF32(nodeFrame, nA+BufNodeColFRX) != 1.0 {
		t.Errorf("nodeA.FRX: got %v, want 1.0", readF32(nodeFrame, nA+BufNodeColFRX))
	}

	// node-B (row 1): geometry only.
	nB := nodeOff + BufNodeStride
	if readF32(nodeFrame, nB+BufNodeColCX) != 4.0 {
		t.Errorf("nodeB.CX: got %v, want 4.0", readF32(nodeFrame, nB+BufNodeColCX))
	}

	// ── Edge block ───────────────────────────────────────────────────────────
	// SrcPortRow/DstPortRow reference the Port block rows the Port frame owns: node-A/out
	// is port row 0, node-B/in is port row 1 (flattened node-row order — node-A's ports
	// then node-B's ports).
	edgeOff := BufEdgeFrameHeaderSize
	if got := readI32(edgeFrame, edgeOff+BufEdgeColSrcPortRow); got != 0 {
		t.Errorf("edge.SrcPortRow: got %d, want 0 (node-A/out)", got)
	}
	if got := readI32(edgeFrame, edgeOff+BufEdgeColDstPortRow); got != 1 {
		t.Errorf("edge.DstPortRow: got %d, want 1 (node-B/in)", got)
	}
	// ── Camera block ─────────────────────────────────────────────────────────
	// LayoutLink block (layoutLinkCount rows) sits between the header and camera block in
	// the SCENE frame, so the camera offset must skip it — none injected in this fixture
	// (layoutLinkCount 0).
	camOff := BufHeaderSize + int(layoutLinkCount)*BufLayoutLinkStride
	if readF32(snap, camOff+BufCameraColPX) != 10.0 {
		t.Errorf("camera.PX: got %v, want 10.0", readF32(snap, camOff+BufCameraColPX))
	}
	if readF32(snap, camOff+BufCameraColR) != 20.0 {
		t.Errorf("camera.R: got %v, want 20.0", readF32(snap, camOff+BufCameraColR))
	}

	// ── Overlay block ────────────────────────────────────────────────────────
	ovOff := camOff + BufCameraStride
	if snap[ovOff+BufOverlayColSceneTori] != 1 {
		t.Errorf("overlay.SceneTori: got %v, want 1", snap[ovOff+BufOverlayColSceneTori])
	}
	if snap[ovOff+BufOverlayColScenePoles] != 0 {
		t.Errorf("overlay.ScenePoles: got %v, want 0", snap[ovOff+BufOverlayColScenePoles])
	}

}

// TestSnapshotPorts verifies the Port block: port rows are populated from the
// node-geometry Ports, flattened over nodes in node-row order (each node's ports in
// event order), with the correct NodeRow / dir / IsInput, and that portCount lands in
// the header. Two nodes carry ports so the flattening + NodeRow indexing is exercised.
func TestSnapshotPorts(t *testing.T) {
	s := NewSnapshotState(nil)

	// node-0: one input + one output port.
	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "n0", Radius: 1,
		Ports: []T.PortGeom{
			{Name: "in", IsInput: true, DX: -1, DY: 0, DZ: 0},
			{Name: "out", IsInput: false, DX: 1, DY: 0, DZ: 0},
		},
	})
	// node-1: one output port.
	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "n1", Radius: 1,
		Ports: []T.PortGeom{
			{Name: "out", IsInput: false, DX: 0, DY: 1, DZ: 0},
		},
	})

	if got := s.PortCount(); got != 3 {
		t.Fatalf("PortCount: got %d, want 3", got)
	}

	// The Port block now lives in the node-owner-group frame (see BufBlockTagNode).
	nodeFrame := s.BuildNodeFrame()
	nodeCount := readU32(nodeFrame, 4)
	portCount := readU32(nodeFrame, 8)
	if portCount != 3 {
		t.Fatalf("header portCount: got %d, want 3", portCount)
	}

	portOff := BufNodeFrameHeaderSize +
		int(nodeCount)*BufNodeStride +
		int(nodeCount)*BufInteriorSlotsPerNode*BufInteriorStride

	row := func(i int) int { return portOff + i*BufPortStride }

	// Row 0: n0/in (NodeRow 0, dir -1,0,0, input).
	if got := readI32(nodeFrame, row(0)+BufPortColNodeRow); got != 0 {
		t.Errorf("port0.NodeRow: got %d, want 0", got)
	}
	if got := readF32(nodeFrame, row(0)+BufPortColDX); got != -1 {
		t.Errorf("port0.DX: got %v, want -1", got)
	}
	if nodeFrame[row(0)+BufPortColIsInput] != 1 {
		t.Errorf("port0.IsInput: got %d, want 1", nodeFrame[row(0)+BufPortColIsInput])
	}
	// Row 1: n0/out (NodeRow 0, dir 1,0,0, output).
	if got := readI32(nodeFrame, row(1)+BufPortColNodeRow); got != 0 {
		t.Errorf("port1.NodeRow: got %d, want 0", got)
	}
	if nodeFrame[row(1)+BufPortColIsInput] != 0 {
		t.Errorf("port1.IsInput: got %d, want 0", nodeFrame[row(1)+BufPortColIsInput])
	}
	// Row 2: n1/out (NodeRow 1, dir 0,1,0, output).
	if got := readI32(nodeFrame, row(2)+BufPortColNodeRow); got != 1 {
		t.Errorf("port2.NodeRow: got %d, want 1", got)
	}
	if got := readF32(nodeFrame, row(2)+BufPortColDY); got != 1 {
		t.Errorf("port2.DY: got %v, want 1", got)
	}

	// A re-emit (node move) updates dirs but keeps the port set/order/count stable.
	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "n0", Radius: 1,
		Ports: []T.PortGeom{
			{Name: "in", IsInput: true, DX: 0, DY: -1, DZ: 0},
			{Name: "out", IsInput: false, DX: 0, DY: 1, DZ: 0},
		},
	})
	if got := s.PortCount(); got != 3 {
		t.Fatalf("PortCount after re-emit: got %d, want 3", got)
	}
	nodeFrame = s.BuildNodeFrame()
	if got := readF32(nodeFrame, row(0)+BufPortColDY); got != -1 {
		t.Errorf("port0.DY after re-emit: got %v, want -1", got)
	}
}

// parseFrameStream reads all framed snapshots from raw and returns their payloads.
// Each frame is [len:u32-LE][payload]; returns an error string on malformed input.
func parseFrameStream(t *testing.T, raw []byte) [][]byte {
	t.Helper()
	var frames [][]byte
	off := 0
	for off < len(raw) {
		if off+4 > len(raw) {
			t.Errorf("truncated frame header at offset %d", off)
			return frames
		}
		frameLen := int(readU32(raw, off))
		off += 4
		if off+frameLen > len(raw) {
			t.Errorf("truncated frame payload at offset %d (want %d bytes)", off, frameLen)
			return frames
		}
		frames = append(frames, raw[off:off+frameLen])
		off += frameLen
	}
	return frames
}

// TestSnapshotFraming verifies that the framed binary output produced by
// emitSnapshot has the correct [len:u32-LE][snapshot] structure and that the
// output stream may contain multiple frames (one per state-change event).
func TestSnapshotFraming(t *testing.T) {
	var buf bytes.Buffer
	s := NewSnapshotState(&buf)

	// KindNodeGeometry, KindGeometry, and KindPosition each trigger emitSnapshot,
	// so the stream will contain 3 frames when all three events are processed.
	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "node-X",
		NX: 0, NY: 0, NZ: 0, Radius: 1.0, SphereR: 0.5,
	})
	s.Update(T.Event{
		Kind: T.KindGeometry, Edge: "edge-X",
		SX: 0, SY: 0, SZ: 0, EX: 1, EY: 0, EZ: 0,
	})
	s.Update(T.Event{
		Kind: T.KindPosition, Node: "node-X", Port: "out",
		X: 0.5, Y: 0, Z: 0, Value: 1, F: 0.5, Bead: 7,
	})

	frames := parseFrameStream(t, buf.Bytes())
	// Each trigger now emits FOUR frames (bead, then node, then edge, then scene — see
	// emitSnapshot's comment) — see BufBlockTagBead/BufBlockTagNode/BufBlockTagEdge/
	// BufBlockTagScene in frame_tags.go — so 3 triggers produce 12 frames.
	if len(frames) != 12 {
		t.Fatalf("expected 12 frames (3 triggers × [bead, node, edge, scene]), got %d", len(frames))
	}

	// Every frame carries a leading block tag; split by tag and take the LAST of each kind.
	var lastScene, lastBead, lastNode, lastEdge []byte
	for _, f := range frames {
		if len(f) < 1 {
			t.Fatalf("frame too short to carry a block tag")
		}
		switch f[0] {
		case BufBlockTagScene:
			lastScene = f[1:]
		case BufBlockTagBead:
			lastBead = f[1:]
		case BufBlockTagNode:
			lastNode = f[1:]
		case BufBlockTagEdge:
			lastEdge = f[1:]
		default:
			t.Fatalf("unexpected block tag: %d", f[0])
		}
	}
	if lastScene == nil || lastBead == nil || lastNode == nil || lastEdge == nil {
		t.Fatalf("expected at least one scene, bead, node, and edge frame")
	}

	// The last bead frame (position trigger) must carry 1 bead.
	beadCount := readU32(lastBead, 4)
	if beadCount != 1 {
		t.Errorf("last bead frame beadCount: got %d, want 1", beadCount)
	}
	wantBeadSize := BufBeadHeaderSize + int(beadCount)*BufBeadStride
	if len(lastBead) != wantBeadSize {
		t.Errorf("last bead frame size: got %d, want %d", len(lastBead), wantBeadSize)
	}

	// The last node frame (position trigger) must carry 1 node.
	nodeCount := readU32(lastNode, 4)
	if nodeCount != 1 {
		t.Errorf("last node frame nodeCount: got %d, want 1", nodeCount)
	}
	nodePortCount := readU32(lastNode, 8)
	nodeLabelBytesCount := readU32(lastNode, 12)
	nodePortNameBytesCount := readU32(lastNode, 16)
	wantNodeSize := BufNodeFrameHeaderSize +
		int(nodeCount)*BufNodeStride +
		int(nodeCount)*BufInteriorSlotsPerNode*BufInteriorStride +
		int(nodePortCount)*BufPortStride +
		int(nodeLabelBytesCount) +
		int(nodePortNameBytesCount)
	if len(lastNode) != wantNodeSize {
		t.Errorf("last node frame size: got %d, want %d", len(lastNode), wantNodeSize)
	}

	// The last edge frame (position trigger) must carry 1 edge.
	edgeCount := readU32(lastEdge, 4)
	if edgeCount != 1 {
		t.Errorf("last edge frame edgeCount: got %d, want 1", edgeCount)
	}
	edgeLabelBytesCount := readU32(lastEdge, 8)
	wantEdgeSize := BufEdgeFrameHeaderSize +
		int(edgeCount)*BufEdgeStride +
		int(edgeLabelBytesCount)
	if len(lastEdge) != wantEdgeSize {
		t.Errorf("last edge frame size: got %d, want %d", len(lastEdge), wantEdgeSize)
	}

	// The last scene frame (position trigger). No EVENT block anymore (memory/
	// feedback_no_single_writer_bridge.md): header is [tick][layoutLinkCount] only.
	payload := lastScene
	layoutLinkCount := readU32(payload, 4)
	wantSize := BufHeaderSize +
		int(layoutLinkCount)*BufLayoutLinkStride +
		BufCameraStride + BufOverlayStride + BufSceneStride
	if len(payload) != wantSize {
		t.Errorf("last frame size: got %d, want %d", len(payload), wantSize)
	}
}

// TestEdgePortRowResolvesToPortPosition is the endpoint-tear fix's core equality proof
// (per-owner-buffer-rows.md, option (a)): the edge stores NO endpoint coordinate of its
// own — it references its two port rows (SrcPortRow/DstPortRow), and those rows are the
// ONLY place the endpoint's world position lives (the Port block, node-owned). This test
// feeds a distinctive PX/PY/PZ on each of two nodes' single ports, connects them with a
// KindGeometry event carrying SrcPort/DstPort, and asserts the edge's resolved port rows'
// PX/PY/PZ are EXACT (same float32 bits) matches of what was fed in — not "close enough",
// exact, because there is exactly one copy of this value in the whole buffer. This is
// zero skew BY CONSTRUCTION: there is no second copy anywhere that could ever go stale
// under a fast drag (the tear the old SX..EZ duplication produced).
func TestEdgePortRowResolvesToPortPosition(t *testing.T) {
	s := NewSnapshotState(nil)

	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "src",
		Ports: []T.PortGeom{{Name: "out", IsInput: false, PX: 11.5, PY: -22.25, PZ: 3.75}},
	})
	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "dst",
		Ports: []T.PortGeom{{Name: "in", IsInput: true, PX: 100.5, PY: 200.25, PZ: -300.125}},
	})
	s.Update(T.Event{
		Kind: T.KindGeometry, Edge: "e0",
		Node: "src", Target: "dst", SrcPort: "out", DstPort: "in",
	})

	edgeFrame := s.BuildEdgeFrame()
	nodeFrame := s.BuildNodeFrame()

	edgeOff := BufEdgeFrameHeaderSize
	srcRow := readI32(edgeFrame, edgeOff+BufEdgeColSrcPortRow)
	dstRow := readI32(edgeFrame, edgeOff+BufEdgeColDstPortRow)
	if srcRow < 0 || dstRow < 0 {
		t.Fatalf("edge port rows unresolved: SrcPortRow=%d DstPortRow=%d", srcRow, dstRow)
	}

	nodeCount := int(readU32(nodeFrame, 4))
	portOff := BufNodeFrameHeaderSize + nodeCount*BufNodeStride + nodeCount*BufInteriorSlotsPerNode*BufInteriorStride

	srcBase := portOff + int(srcRow)*BufPortStride
	dstBase := portOff + int(dstRow)*BufPortStride

	wantSrc := [3]float32{11.5, -22.25, 3.75}
	gotSrc := [3]float32{
		readF32(nodeFrame, srcBase+BufPortColPX),
		readF32(nodeFrame, srcBase+BufPortColPY),
		readF32(nodeFrame, srcBase+BufPortColPZ),
	}
	if gotSrc != wantSrc {
		t.Errorf("src port position via SrcPortRow: got %v, want %v (exact float32 match required — zero skew by construction)", gotSrc, wantSrc)
	}

	wantDst := [3]float32{100.5, 200.25, -300.125}
	gotDst := [3]float32{
		readF32(nodeFrame, dstBase+BufPortColPX),
		readF32(nodeFrame, dstBase+BufPortColPY),
		readF32(nodeFrame, dstBase+BufPortColPZ),
	}
	if gotDst != wantDst {
		t.Errorf("dst port position via DstPortRow: got %v, want %v (exact float32 match required — zero skew by construction)", gotDst, wantDst)
	}
}

// TestSnapshotNodeLabels verifies each node's label UTF-8 bytes are written into the
// trailing label section and its LabelOff/LabelLen columns slice back to the exact string.
func TestSnapshotNodeLabels(t *testing.T) {
	s := NewSnapshotState(nil)
	nodeGeom := func(id, label string) T.Event {
		return T.Event{
			Kind: T.KindNodeGeometry, Node: id, Label: label,
			NX: 0, NY: 0, NZ: 0, Radius: 10, SphereR: 10,
		}
	}
	// Distinct labels incl. a multi-byte rune to prove byte-length (not rune-count) sizing.
	s.Update(nodeGeom("n0", "alpha"))
	s.Update(nodeGeom("n1", "β-node")) // β is 2 UTF-8 bytes
	s.Update(nodeGeom("n2", ""))       // empty label → len 0

	// The Node block + Label bytes now live in the node-owner-group frame (see
	// BufBlockTagNode).
	nodeFrame := s.BuildNodeFrame()

	nodeCount := int(readU32(nodeFrame, 4))
	if nodeCount != 3 {
		t.Fatalf("nodeCount: got %d, want 3", nodeCount)
	}
	portCount := int(readU32(nodeFrame, 8))
	labelBytesCount := int(readU32(nodeFrame, 12))
	portNameBytesCount := int(readU32(nodeFrame, 16))

	// Label section sits after the node/interior/port blocks. Recompute its start the same
	// way the decoder does: header + node + interior + port.
	labelSecOff := BufNodeFrameHeaderSize +
		nodeCount*BufNodeStride +
		nodeCount*BufInteriorSlotsPerNode*BufInteriorStride +
		portCount*BufPortStride
	// The label section is followed by the port-name-bytes section, so its end is the
	// start of that section, not the frame end. Verify the full length.
	fullLen := labelSecOff + labelBytesCount + portNameBytesCount
	if fullLen != len(nodeFrame) {
		t.Fatalf("computed full length %d != node frame len %d", fullLen, len(nodeFrame))
	}

	nodeOff := BufNodeFrameHeaderSize
	want := []string{"alpha", "β-node", ""}
	for i := 0; i < nodeCount; i++ {
		off := int(readU32(nodeFrame, nodeOff+i*BufNodeStride+BufNodeColLabelOff))
		ln := int(readU32(nodeFrame, nodeOff+i*BufNodeStride+BufNodeColLabelLen))
		got := string(nodeFrame[labelSecOff+off : labelSecOff+off+ln])
		if got != want[i] {
			t.Errorf("node %d label: got %q, want %q", i, got, want[i])
		}
	}
	// β is 2 bytes: "β-node" = 2+1+4 = 7 bytes; total = 5 + 7 + 0 = 12.
	if labelBytesCount != 12 {
		t.Errorf("labelBytesCount: got %d, want 12", labelBytesCount)
	}
}
