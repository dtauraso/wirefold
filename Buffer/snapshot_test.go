// Buffer/snapshot_test.go — round-trip parse test for SnapshotState.
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

// TestEventBlockPopulate feeds two nodes (with ports) and a wire send, then asserts the
// snapshot's EVENT block resolves the send's node/port/target/targetHandle to the correct
// buffer rows and carries the value/arc/latency — the populate half of the buffer-decoded
// .probe log path (the ext-host decode half is covered by the TS equivalence test).
func TestEventBlockPopulate(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "A", Radius: 1,
		Ports: []T.PortGeom{{Name: "out", IsInput: false, DX: 1}}})
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "B", Radius: 1,
		Ports: []T.PortGeom{{Name: "in", IsInput: true, DX: -1}}})
	s.Update(T.Event{Kind: T.KindSend, Node: "A", Port: "out", Value: 7,
		ArcLength: 12.5, SimLatencyMs: 33.0, Target: "B", TargetHandle: "in"})

	snap := s.BuildSnapshot()
	beadCount := int(readU32(snap, 4))
	nodeCount := int(readU32(snap, 8))
	edgeCount := int(readU32(snap, 12))
	portCount := int(readU32(snap, 16))
	labelBytesCount := int(readU32(snap, 20))
	eventCount := int(readU32(snap, 24))
	// The two node-geometry events each emitted (and flushed) their own snapshot; only the
	// send (which does not emit) is still pending, so this snapshot's EVENT block holds 1 row.
	if eventCount != 1 {
		t.Fatalf("eventCount: got %d, want 1 (send; node-geometry flushed on its own emits)", eventCount)
	}
	eventOff := BufHeaderSize +
		beadCount*BufBeadStride +
		nodeCount*BufNodeStride +
		nodeCount*BufInteriorSlotsPerNode*BufInteriorStride +
		edgeCount*BufEdgeStride +
		portCount*BufPortStride +
		BufCameraStride + BufOverlayStride + BufSceneStride + labelBytesCount

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
		if int(snap[base+BufEventColKind]) != sendKind {
			continue
		}
		found = true
		// A=row0, out=port row0; B=row1, in=port row1.
		if got := readI32(snap, base+BufEventColNodeRow); got != 0 {
			t.Errorf("send NodeRow: got %d, want 0 (A)", got)
		}
		if got := readI32(snap, base+BufEventColPortRow); got != 0 {
			t.Errorf("send PortRow: got %d, want 0 (A/out)", got)
		}
		if got := readI32(snap, base+BufEventColTargetRow); got != 1 {
			t.Errorf("send TargetRow: got %d, want 1 (B)", got)
		}
		if got := readI32(snap, base+BufEventColTargetPortRow); got != 1 {
			t.Errorf("send TargetPortRow: got %d, want 1 (B/in)", got)
		}
		if got := readI32(snap, base+BufEventColValue); got != 7 {
			t.Errorf("send Value: got %d, want 7", got)
		}
		if got := readF32(snap, base+BufEventColArcLength); got != 12.5 {
			t.Errorf("send ArcLength: got %v, want 12.5", got)
		}
		if got := readF32(snap, base+BufEventColSimLatencyMs); got != 33.0 {
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

	// Register two nodes via KindNodeGeometry events.
	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "node-A",
		NX: 1.0, NY: 2.0, NZ: 3.0,
		Radius: 0.5, SphereR: 0.25,
		VRX: 0.0, VRY: 0.0, VRZ: 1.0,
		FRX: 1.0, FRY: 0.0, FRZ: 0.0,
	})
	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "node-B",
		NX: 4.0, NY: 5.0, NZ: 6.0,
		Radius: 0.75, SphereR: 0.375,
	})

	// Register one edge via KindGeometry. Node (source) and Target (dest) carry the
	// edge's endpoint node ids; the builder resolves them to node-row indices
	// (node-A=row 0, node-B=row 1).
	s.Update(T.Event{
		Kind: T.KindGeometry, Edge: "edge-1",
		Node: "node-A", Target: "node-B",
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

	// Inject a send event to build the srcToDest mapping (so arrive can route EvArrive).
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

	// Set a transient event on node-A (recv) and node-B (arrive via srcToDest lookup).
	s.Update(T.Event{Kind: T.KindRecv, Node: "node-A", Port: "in", Value: 42})
	s.Update(T.Event{Kind: T.KindArrive, Node: "node-A", Port: "out", Value: 42, Bead: 2})

	// Build snapshot WITHOUT triggering emit (BuildSnapshot is exported for tests).
	snap := s.BuildSnapshot()

	// ── Header ───────────────────────────────────────────────────────────────
	if len(snap) < BufHeaderSize {
		t.Fatalf("snapshot too short: got %d bytes, want >= %d", len(snap), BufHeaderSize)
	}

	beadCount := readU32(snap, 4)
	nodeCount := readU32(snap, 8)
	edgeCount := readU32(snap, 12)

	if beadCount != 1 {
		t.Errorf("beadCount: got %d, want 1", beadCount)
	}
	if nodeCount != 2 {
		t.Errorf("nodeCount: got %d, want 2", nodeCount)
	}
	if edgeCount != 1 {
		t.Errorf("edgeCount: got %d, want 1", edgeCount)
	}

	// ── Size check ───────────────────────────────────────────────────────────
	// Trailing self-sizing sections (header counts at offsets 24/28/32): the EVENT block,
	// port-name bytes, edge-label bytes. The fixture feeds events + an edge label, so these
	// are non-zero — read their counts from the header rather than hard-coding.
	eventCount := readU32(snap, 24)
	portNameBytesCount := readU32(snap, 28)
	edgeLabelBytesCount := readU32(snap, 32)
	wantSize := BufHeaderSize +
		int(beadCount)*BufBeadStride +
		int(nodeCount)*BufNodeStride +
		int(nodeCount)*BufInteriorSlotsPerNode*BufInteriorStride +
		int(edgeCount)*BufEdgeStride +
		BufCameraStride +
		BufOverlayStride +
		BufSceneStride +
		int(eventCount)*BufEventStride +
		int(portNameBytesCount) +
		int(edgeLabelBytesCount)
	// No ports and no labels were injected in this fixture, so the Port and Label sections
	// are zero-length; the header labelBytesCount reflects that.
	if got := readU32(snap, 20); got != 0 {
		t.Errorf("labelBytesCount: got %d, want 0 (no labels in fixture)", got)
	}
	if len(snap) != wantSize {
		t.Errorf("snapshot size: got %d, want %d", len(snap), wantSize)
	}

	// ── Bead block ───────────────────────────────────────────────────────────
	beadOff := BufHeaderSize
	// One bead was injected (Bead=1, node-A/out).
	gotX := readF32(snap, beadOff+BufBeadColX)
	gotY := readF32(snap, beadOff+BufBeadColY)
	gotZ := readF32(snap, beadOff+BufBeadColZ)
	gotVal := readI32(snap, beadOff+BufBeadColValue)
	gotLive := snap[beadOff+BufBeadColLive]

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
	nodeOff := BufHeaderSize + int(beadCount)*BufBeadStride

	// node-A (row 0): geometry + evRecv=1 (set above) + evArrive=0 (arrive was on dest node-B)
	nA := nodeOff
	if readF32(snap, nA+BufNodeColCX) != 1.0 {
		t.Errorf("nodeA.CX: got %v, want 1.0", readF32(snap, nA+BufNodeColCX))
	}
	if readF32(snap, nA+BufNodeColRadius) != 0.5 {
		t.Errorf("nodeA.Radius: got %v, want 0.5", readF32(snap, nA+BufNodeColRadius))
	}
	if snap[nA+BufNodeColEvRecv] != 1 {
		t.Errorf("nodeA.EvRecv: got %v, want 1", snap[nA+BufNodeColEvRecv])
	}
	if snap[nA+BufNodeColEvArrive] != 0 {
		t.Errorf("nodeA.EvArrive: got %v, want 0 (arrive routes to dest node-B)", snap[nA+BufNodeColEvArrive])
	}
	// Ring-plane normals (vr vertical, fr flat) reach the node columns for SphereRing.
	if readF32(snap, nA+BufNodeColVRZ) != 1.0 {
		t.Errorf("nodeA.VRZ: got %v, want 1.0", readF32(snap, nA+BufNodeColVRZ))
	}
	if readF32(snap, nA+BufNodeColFRX) != 1.0 {
		t.Errorf("nodeA.FRX: got %v, want 1.0", readF32(snap, nA+BufNodeColFRX))
	}

	// node-B (row 1): geometry + evArrive=1 (arrive routed from node-A/out → node-B)
	nB := nodeOff + BufNodeStride
	if readF32(snap, nB+BufNodeColCX) != 4.0 {
		t.Errorf("nodeB.CX: got %v, want 4.0", readF32(snap, nB+BufNodeColCX))
	}
	if snap[nB+BufNodeColEvArrive] != 1 {
		t.Errorf("nodeB.EvArrive: got %v, want 1", snap[nB+BufNodeColEvArrive])
	}

	// ── Edge block ───────────────────────────────────────────────────────────
	// The Interior block (nodeCount×BufInteriorSlotsPerNode rows) sits between the
	// node and edge blocks, so the edge offset must skip it.
	edgeOff := nodeOff + int(nodeCount)*BufNodeStride +
		int(nodeCount)*BufInteriorSlotsPerNode*BufInteriorStride
	if readF32(snap, edgeOff+BufEdgeColSX) != float32(1.1) {
		t.Errorf("edge.SX: got %v, want ~1.1", readF32(snap, edgeOff+BufEdgeColSX))
	}
	if readF32(snap, edgeOff+BufEdgeColEZ) != float32(6.6) {
		t.Errorf("edge.EZ: got %v, want ~6.6", readF32(snap, edgeOff+BufEdgeColEZ))
	}
	// Edge-graph adjacency: source node-A → row 0, dest node-B → row 1.
	if got := readI32(snap, edgeOff+BufEdgeColSrcNodeRow); got != 0 {
		t.Errorf("edge.SrcNodeRow: got %d, want 0 (node-A)", got)
	}
	if got := readI32(snap, edgeOff+BufEdgeColDstNodeRow); got != 1 {
		t.Errorf("edge.DstNodeRow: got %d, want 1 (node-B)", got)
	}

	// ── Camera block ─────────────────────────────────────────────────────────
	camOff := edgeOff + int(edgeCount)*BufEdgeStride
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

	snap := s.BuildSnapshot()
	beadCount := readU32(snap, 4)
	nodeCount := readU32(snap, 8)
	edgeCount := readU32(snap, 12)
	portCount := readU32(snap, 16)
	if portCount != 3 {
		t.Fatalf("header portCount: got %d, want 3", portCount)
	}

	portOff := BufHeaderSize +
		int(beadCount)*BufBeadStride +
		int(nodeCount)*BufNodeStride +
		int(nodeCount)*BufInteriorSlotsPerNode*BufInteriorStride +
		int(edgeCount)*BufEdgeStride

	row := func(i int) int { return portOff + i*BufPortStride }

	// Row 0: n0/in (NodeRow 0, dir -1,0,0, input).
	if got := readI32(snap, row(0)+BufPortColNodeRow); got != 0 {
		t.Errorf("port0.NodeRow: got %d, want 0", got)
	}
	if got := readF32(snap, row(0)+BufPortColDX); got != -1 {
		t.Errorf("port0.DX: got %v, want -1", got)
	}
	if snap[row(0)+BufPortColIsInput] != 1 {
		t.Errorf("port0.IsInput: got %d, want 1", snap[row(0)+BufPortColIsInput])
	}
	// Row 1: n0/out (NodeRow 0, dir 1,0,0, output).
	if got := readI32(snap, row(1)+BufPortColNodeRow); got != 0 {
		t.Errorf("port1.NodeRow: got %d, want 0", got)
	}
	if snap[row(1)+BufPortColIsInput] != 0 {
		t.Errorf("port1.IsInput: got %d, want 0", snap[row(1)+BufPortColIsInput])
	}
	// Row 2: n1/out (NodeRow 1, dir 0,1,0, output).
	if got := readI32(snap, row(2)+BufPortColNodeRow); got != 1 {
		t.Errorf("port2.NodeRow: got %d, want 1", got)
	}
	if got := readF32(snap, row(2)+BufPortColDY); got != 1 {
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
	snap = s.BuildSnapshot()
	if got := readF32(snap, row(0)+BufPortColDY); got != -1 {
		t.Errorf("port0.DY after re-emit: got %v, want -1", got)
	}
}

// TestLookupPortRow verifies the port-row resolution table the gesture FSM uses: a numeric
// buffer PORT-ROW index maps back to its (node, port, isInput) in the SAME flattened order
// the Port block is written (node-row order × each node's Ports order). No port name crosses
// the buffer — the table IS the row→(node,port) authority.
func TestLookupPortRow(t *testing.T) {
	s := NewSnapshotState(nil)

	// Out-of-range before any node registers.
	if _, _, _, ok := s.LookupPortRow(0); ok {
		t.Fatalf("LookupPortRow(0) before any port: want ok=false")
	}

	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "n0", Radius: 1,
		Ports: []T.PortGeom{
			{Name: "in", IsInput: true, DX: -1},
			{Name: "out", IsInput: false, DX: 1},
		},
	})
	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "n1", Radius: 1,
		Ports: []T.PortGeom{
			{Name: "in", IsInput: true, DY: 1},
		},
	})

	cases := []struct {
		row     int
		node    string
		port    string
		isInput bool
	}{
		{0, "n0", "in", true},
		{1, "n0", "out", false},
		{2, "n1", "in", true},
	}
	for _, c := range cases {
		node, port, isInput, ok := s.LookupPortRow(c.row)
		if !ok || node != c.node || port != c.port || isInput != c.isInput {
			t.Errorf("LookupPortRow(%d): got (%q,%q,%v,%v), want (%q,%q,%v,true)",
				c.row, node, port, isInput, ok, c.node, c.port, c.isInput)
		}
	}

	// Out-of-range row → ok=false.
	if _, _, _, ok := s.LookupPortRow(3); ok {
		t.Errorf("LookupPortRow(3) out of range: want ok=false")
	}
	if _, _, _, ok := s.LookupPortRow(-1); ok {
		t.Errorf("LookupPortRow(-1): want ok=false")
	}
}

// TestSelectMode verifies that KindSelect's Value carries the select mode into the
// overlay SelMode column (1 = own / secondary, 0 = surface / primary), and that
// clearing the selection resets SelMode to surface.
func TestSelectMode(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n0", Radius: 1})

	selModeOf := func() byte {
		snap := s.BuildSnapshot()
		nodeCount := readU32(snap, 8)
		edgeCount := readU32(snap, 12)
		ovOff := BufHeaderSize +
			int(readU32(snap, 4))*BufBeadStride +
			int(nodeCount)*BufNodeStride +
			int(nodeCount)*BufInteriorSlotsPerNode*BufInteriorStride +
			int(edgeCount)*BufEdgeStride +
			BufCameraStride
		return snap[ovOff+BufOverlayColSelMode]
	}

	// Primary click → surface (Value 0).
	s.Update(T.Event{Kind: T.KindSelect, Node: "n0", Value: 0})
	if got := selModeOf(); got != 0 {
		t.Errorf("surface select: SelMode got %d, want 0", got)
	}
	// Two-finger tap → own (Value 1).
	s.Update(T.Event{Kind: T.KindSelect, Node: "n0", Value: 1})
	if got := selModeOf(); got != 1 {
		t.Errorf("own select: SelMode got %d, want 1", got)
	}
	// Clear selection → SelMode resets to surface.
	s.Update(T.Event{Kind: T.KindSelect, Node: ""})
	if got := selModeOf(); got != 0 {
		t.Errorf("cleared select: SelMode got %d, want 0", got)
	}
}

// TestHoverColumn verifies that KindHover marks the Hovered column on the hovered node OR
// port (exclusive, cleared on others), and that an empty hover clears all hover flags.
func TestHoverColumn(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n0", Radius: 1,
		Ports: []T.PortGeom{{Name: "in", IsInput: true, DX: -1}}})
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n1", Radius: 1})

	nodeHovered := func(row int) byte {
		snap := s.BuildSnapshot()
		nodeOff := BufHeaderSize + int(readU32(snap, 4))*BufBeadStride
		return snap[nodeOff+row*BufNodeStride+BufNodeColHovered]
	}
	portHovered := func() byte {
		snap := s.BuildSnapshot()
		nodeCount := readU32(snap, 8)
		edgeCount := readU32(snap, 12)
		portOff := BufHeaderSize + int(readU32(snap, 4))*BufBeadStride +
			int(nodeCount)*BufNodeStride +
			int(nodeCount)*BufInteriorSlotsPerNode*BufInteriorStride +
			int(edgeCount)*BufEdgeStride
		return snap[portOff+BufPortColHovered] // port row 0 = n0/in
	}

	// Hover node n0 → node row 0 Hovered=1, node row 1 = 0.
	s.Update(T.Event{Kind: T.KindHover, Node: "n0"})
	if nodeHovered(0) != 1 || nodeHovered(1) != 0 {
		t.Fatalf("node hover: row0=%d row1=%d, want 1,0", nodeHovered(0), nodeHovered(1))
	}
	// Hover the port n0/in → port Hovered=1, node hover cleared.
	s.Update(T.Event{Kind: T.KindHover, Node: "n0", Port: "in", Value: 1})
	if portHovered() != 1 {
		t.Fatalf("port hover: got %d, want 1", portHovered())
	}
	if nodeHovered(0) != 0 {
		t.Fatalf("port hover should clear node hover: node0=%d, want 0", nodeHovered(0))
	}
	// Empty hover clears everything.
	s.Update(T.Event{Kind: T.KindHover, Node: ""})
	if nodeHovered(0) != 0 || portHovered() != 0 {
		t.Fatalf("cleared hover: node0=%d port=%d, want 0,0", nodeHovered(0), portHovered())
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
	if len(frames) != 3 {
		t.Fatalf("expected 3 frames (node-geometry + edge-geometry + position), got %d", len(frames))
	}

	// The last frame (position trigger) must carry 1 bead, 1 node, 1 edge.
	payload := frames[len(frames)-1]
	beadCount := readU32(payload, 4)
	nodeCount := readU32(payload, 8)
	edgeCount := readU32(payload, 12)

	if beadCount != 1 {
		t.Errorf("last frame beadCount: got %d, want 1", beadCount)
	}
	if nodeCount != 1 {
		t.Errorf("last frame nodeCount: got %d, want 1", nodeCount)
	}
	if edgeCount != 1 {
		t.Errorf("last frame edgeCount: got %d, want 1", edgeCount)
	}

	wantSize := BufHeaderSize +
		int(beadCount)*BufBeadStride +
		int(nodeCount)*BufNodeStride +
		int(nodeCount)*BufInteriorSlotsPerNode*BufInteriorStride +
		int(edgeCount)*BufEdgeStride +
		BufCameraStride + BufOverlayStride + BufSceneStride +
		int(readU32(payload, 24))*BufEventStride + // event block
		int(readU32(payload, 28)) + // port-name bytes
		int(readU32(payload, 32)) // edge-label bytes
	if len(payload) != wantSize {
		t.Errorf("last frame size: got %d, want %d", len(payload), wantSize)
	}
}

// TestTransientFlagsCleared verifies that transient event flags are reset to
// zero after each snapshot emit (they must not bleed into subsequent ticks).
func TestTransientFlagsCleared(t *testing.T) {
	s := NewSnapshotState(nil)

	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "n1",
		NX: 0, NY: 0, NZ: 0, Radius: 1, SphereR: 0.5,
	})

	// Set evFire on n1 and emit (via position).
	s.Update(T.Event{Kind: T.KindFire, Node: "n1"})
	s.Update(T.Event{
		Kind: T.KindPosition, Node: "n1", Port: "out",
		X: 0, Y: 0, Z: 0, Value: 0, F: 0.5, Bead: 1,
	})

	// After the position-triggered snapshot, transients should be cleared.
	// Build a second snapshot immediately (without setting any event flags).
	snap := s.BuildSnapshot()
	nodeOff := BufHeaderSize + BufBeadStride // 1 live bead
	if snap[nodeOff+BufNodeColEvFire] != 0 {
		t.Errorf("EvFire not cleared after snapshot: got %v, want 0", snap[nodeOff+BufNodeColEvFire])
	}
}

// TestInteriorBeadReachesSnapshot verifies that KindNodeBead events land in the
// Interior block: slot = row*2 + col, present/value/offset are packed at the right
// columns, empty slots stay present=0, and a present=false event clears a popped slot.
func TestInteriorBeadReachesSnapshot(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n1", Radius: 1})

	// (row 0, col 0) → slot 0: present value 1 at local offset (2,-3,4).
	s.Update(T.Event{Kind: T.KindNodeBead, Node: "n1", Row: 0, Col: 0, Present: true, Value: 1, X: 2, Y: -3, Z: 4})
	// (row 1, col 0) → slot 2: present value 0 (a valid black bead) at (1,1,1).
	s.Update(T.Event{Kind: T.KindNodeBead, Node: "n1", Row: 1, Col: 0, Present: true, Value: 0, X: 1, Y: 1, Z: 1})

	snap := s.BuildSnapshot()
	// Interior block starts right after the single node row (no beads).
	interiorOff := BufHeaderSize + 1*BufNodeStride
	slot := func(i int) int { return interiorOff + i*BufInteriorStride }

	if snap[slot(0)+BufInteriorColPresent] != 1 {
		t.Errorf("slot0.Present: got %d, want 1", snap[slot(0)+BufInteriorColPresent])
	}
	if v := readI32(snap, slot(0)+BufInteriorColValue); v != 1 {
		t.Errorf("slot0.Value: got %d, want 1", v)
	}
	if x := readF32(snap, slot(0)+BufInteriorColOX); x != 2 {
		t.Errorf("slot0.OX: got %v, want 2", x)
	}
	if y := readF32(snap, slot(0)+BufInteriorColOY); y != -3 {
		t.Errorf("slot0.OY: got %v, want -3", y)
	}
	if z := readF32(snap, slot(0)+BufInteriorColOZ); z != 4 {
		t.Errorf("slot0.OZ: got %v, want 4", z)
	}
	// Slot 1 (row 0, col 1) was never emitted → absent.
	if snap[slot(1)+BufInteriorColPresent] != 0 {
		t.Errorf("slot1.Present: got %d, want 0 (never emitted)", snap[slot(1)+BufInteriorColPresent])
	}
	// Slot 2 (row 1, col 0): present, value 0.
	if snap[slot(2)+BufInteriorColPresent] != 1 {
		t.Errorf("slot2.Present: got %d, want 1", snap[slot(2)+BufInteriorColPresent])
	}
	if v := readI32(snap, slot(2)+BufInteriorColValue); v != 0 {
		t.Errorf("slot2.Value: got %d, want 0", v)
	}

	// A present=false event on slot 0 clears it (popped bead disappears).
	s.Update(T.Event{Kind: T.KindNodeBead, Node: "n1", Row: 0, Col: 0, Present: false, Value: 0, X: 2, Y: -3, Z: 4})
	snap = s.BuildSnapshot()
	if snap[slot(0)+BufInteriorColPresent] != 0 {
		t.Errorf("slot0.Present after pop: got %d, want 0", snap[slot(0)+BufInteriorColPresent])
	}
}

// TestSelectionPersistsAndIsExclusive verifies KindSelect marks exactly one node's
// Selected column, that it PERSISTS across snapshots (unlike transient event flags),
// and that a later select on another node moves the flag (exclusive), while node=""
// clears it entirely.
func TestSelectionPersistsAndIsExclusive(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n1", Radius: 1})
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n2", Radius: 1})

	n1 := BufHeaderSize // no beads, no need to offset
	n2 := n1 + BufNodeStride

	// Select n1.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n1"})
	snap := s.BuildSnapshot()
	if snap[n1+BufNodeColSelected] != 1 || snap[n2+BufNodeColSelected] != 0 {
		t.Fatalf("after select n1: n1.Selected=%d n2.Selected=%d want 1,0", snap[n1+BufNodeColSelected], snap[n2+BufNodeColSelected])
	}

	// Persists: a fresh snapshot with no further select keeps n1 selected.
	snap = s.BuildSnapshot()
	if snap[n1+BufNodeColSelected] != 1 {
		t.Fatalf("selection did not persist: n1.Selected=%d want 1", snap[n1+BufNodeColSelected])
	}

	// Exclusive: selecting n2 clears n1.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n2"})
	snap = s.BuildSnapshot()
	if snap[n1+BufNodeColSelected] != 0 || snap[n2+BufNodeColSelected] != 1 {
		t.Fatalf("after select n2: n1.Selected=%d n2.Selected=%d want 0,1", snap[n1+BufNodeColSelected], snap[n2+BufNodeColSelected])
	}

	// Clear: node="" deselects all.
	s.Update(T.Event{Kind: T.KindSelect, Node: ""})
	snap = s.BuildSnapshot()
	if snap[n1+BufNodeColSelected] != 0 || snap[n2+BufNodeColSelected] != 0 {
		t.Fatalf("after clear: n1.Selected=%d n2.Selected=%d want 0,0", snap[n1+BufNodeColSelected], snap[n2+BufNodeColSelected])
	}
}

// TestLatchedSelPersistsThroughDeselect verifies the Go-owned LatchedSel column: it moves
// with Selected when a DIFFERENT node is selected, but — unlike Selected — does NOT clear
// when the node is deselected (Node=""). This is the replacement for the old TS-owned
// `latchedSel` React state in NavGuides.tsx (see Buffer/layout.go LatchedSel).
func TestLatchedSelPersistsThroughDeselect(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n1", Radius: 1})
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n2", Radius: 1})

	n1 := BufHeaderSize
	n2 := n1 + BufNodeStride

	// Select n1: both Selected and LatchedSel move to n1.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n1"})
	snap := s.BuildSnapshot()
	if snap[n1+BufNodeColSelected] != 1 || snap[n1+BufNodeColLatchedSel] != 1 {
		t.Fatalf("after select n1: Selected=%d LatchedSel=%d want 1,1", snap[n1+BufNodeColSelected], snap[n1+BufNodeColLatchedSel])
	}

	// Deselect (Node=""): Selected clears, but LatchedSel STAYS on n1.
	s.Update(T.Event{Kind: T.KindSelect, Node: ""})
	snap = s.BuildSnapshot()
	if snap[n1+BufNodeColSelected] != 0 {
		t.Fatalf("after deselect: n1.Selected=%d want 0", snap[n1+BufNodeColSelected])
	}
	if snap[n1+BufNodeColLatchedSel] != 1 {
		t.Fatalf("after deselect: n1.LatchedSel=%d want 1 (latch persists through deselect)", snap[n1+BufNodeColLatchedSel])
	}

	// Selecting a DIFFERENT node (n2) moves the latch to n2 and clears it on n1.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n2"})
	snap = s.BuildSnapshot()
	if snap[n2+BufNodeColSelected] != 1 || snap[n2+BufNodeColLatchedSel] != 1 {
		t.Fatalf("after select n2: n2.Selected=%d n2.LatchedSel=%d want 1,1", snap[n2+BufNodeColSelected], snap[n2+BufNodeColLatchedSel])
	}
	if snap[n1+BufNodeColLatchedSel] != 0 {
		t.Fatalf("after select n2: n1.LatchedSel=%d want 0 (latch moved off n1)", snap[n1+BufNodeColLatchedSel])
	}
}

// TestEdgeSelectionExclusiveWithNode verifies KindSelect with the Edge field marks exactly
// one edge's Selected column, persists it, moves it exclusively to another edge, and that
// node/edge selection is MUTUALLY exclusive (selecting an edge clears the node, and vice
// versa). Also exercises LookupEdgeRow: an edge row resolves to its label.
func TestEdgeSelectionExclusiveWithNode(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n1", Radius: 1})
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n2", Radius: 1})
	// Two edges (KindGeometry registers them in first-seen order → rows 0,1).
	s.Update(T.Event{Kind: T.KindGeometry, Edge: "e0", Node: "n1", Target: "n2"})
	s.Update(T.Event{Kind: T.KindGeometry, Edge: "e1", Node: "n2", Target: "n1"})

	// Edge-row table resolves rows → labels in stable order.
	if l, ok := s.LookupEdgeRow(0); !ok || l != "e0" {
		t.Fatalf("LookupEdgeRow(0)=%q,%v want e0,true", l, ok)
	}
	if l, ok := s.LookupEdgeRow(1); !ok || l != "e1" {
		t.Fatalf("LookupEdgeRow(1)=%q,%v want e1,true", l, ok)
	}
	if _, ok := s.LookupEdgeRow(2); ok {
		t.Fatalf("LookupEdgeRow(2) ok=true want false (out of range)")
	}

	nodeCount := 2
	edgeBase := BufHeaderSize +
		0*BufBeadStride +
		nodeCount*BufNodeStride +
		nodeCount*BufInteriorSlotsPerNode*BufInteriorStride
	e0Sel := edgeBase + 0*BufEdgeStride + BufEdgeColSelected
	e1Sel := edgeBase + 1*BufEdgeStride + BufEdgeColSelected
	n1Sel := BufHeaderSize + 0*BufNodeStride + BufNodeColSelected

	// Select node n1 first.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n1"})
	snap := s.BuildSnapshot()
	if snap[n1Sel] != 1 {
		t.Fatalf("after select n1: n1.Selected=%d want 1", snap[n1Sel])
	}

	// Select edge e0: sets e0, clears the node selection (exclusive).
	s.Update(T.Event{Kind: T.KindSelect, Edge: "e0"})
	snap = s.BuildSnapshot()
	if snap[e0Sel] != 1 || snap[e1Sel] != 0 {
		t.Fatalf("after select e0: e0.Selected=%d e1.Selected=%d want 1,0", snap[e0Sel], snap[e1Sel])
	}
	if snap[n1Sel] != 0 {
		t.Fatalf("after select e0: n1.Selected=%d want 0 (edge select clears node)", snap[n1Sel])
	}

	// Persists across snapshots.
	snap = s.BuildSnapshot()
	if snap[e0Sel] != 1 {
		t.Fatalf("edge selection did not persist: e0.Selected=%d want 1", snap[e0Sel])
	}

	// Exclusive among edges: selecting e1 clears e0.
	s.Update(T.Event{Kind: T.KindSelect, Edge: "e1"})
	snap = s.BuildSnapshot()
	if snap[e0Sel] != 0 || snap[e1Sel] != 1 {
		t.Fatalf("after select e1: e0.Selected=%d e1.Selected=%d want 0,1", snap[e0Sel], snap[e1Sel])
	}

	// Selecting a node again clears the edge selection (exclusive both ways).
	s.Update(T.Event{Kind: T.KindSelect, Node: "n2"})
	snap = s.BuildSnapshot()
	if snap[e1Sel] != 0 {
		t.Fatalf("after select node n2: e1.Selected=%d want 0 (node select clears edge)", snap[e1Sel])
	}
}

// TestNodeKindIDRoundTrip verifies that NodeKindID maps each known runtime kind to a
// stable index, and that the index is written into the buffer's KindId column and
// read back correctly via buildSnapshot. The expected indices match NODE_DEFS_ARRAY
// order (alphabetical by Go kind name) in node-defs.ts.
func TestNodeKindIDRoundTrip(t *testing.T) {
	// Verify the index produced by NodeKindID matches the known alphabetical order.
	want := map[string]uint8{
		"Hold":                      0,
		"HoldFlip":                  1,
		"HoldNewSendOld":            2,
		"Input":                     3,
		"Pacer":                     4,
		"Pulse":                     5,
		"WindowAndInhibitLeftGate":  6,
		"WindowAndInhibitRightGate": 7,
	}
	for kind, wantID := range want {
		if got := NodeKindID(kind); got != wantID {
			t.Errorf("NodeKindID(%q) = %d, want %d", kind, got, wantID)
		}
	}
	if got := NodeKindID("UnknownKind"); got != KindIDUnknown {
		t.Errorf("NodeKindID(%q) = %d, want KindIDUnknown (%d)", "UnknownKind", got, KindIDUnknown)
	}

	// Verify KindId is written into the buffer snapshot correctly.
	s := NewSnapshotState(nil)
	nodeGeom := func(id, kind string) T.Event {
		return T.Event{
			Kind: T.KindNodeGeometry, Node: id, NodeKind: kind,
			NX: 0, NY: 0, NZ: 0, Radius: 10, SphereR: 10,
			VRX: 0, VRY: 1, VRZ: 0, FRX: 1, FRY: 0, FRZ: 0,
		}
	}
	s.Update(nodeGeom("n0", "Input")) // KindId = 3
	s.Update(nodeGeom("n1", "Pulse")) // KindId = 5
	snap := s.BuildSnapshot()

	// Skip the 20-byte header. Node block starts there.
	nodeOff := BufHeaderSize
	n0KindId := snap[nodeOff+0*BufNodeStride+BufNodeColKindId]
	n1KindId := snap[nodeOff+1*BufNodeStride+BufNodeColKindId]
	if n0KindId != 3 {
		t.Errorf("n0 KindId = %d, want 3 (Input)", n0KindId)
	}
	if n1KindId != 5 {
		t.Errorf("n1 KindId = %d, want 5 (Pulse)", n1KindId)
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

	snap := s.BuildSnapshot()

	nodeCount := int(readU32(snap, 8))
	if nodeCount != 3 {
		t.Fatalf("nodeCount: got %d, want 3", nodeCount)
	}
	labelBytesCount := int(readU32(snap, 20))

	// Label section sits after every other block. Recompute its start the same way the
	// decoder does: header + bead + node + interior + edge + port + camera + overlay.
	beadCount := int(readU32(snap, 4))
	edgeCount := int(readU32(snap, 12))
	portCount := int(readU32(snap, 16))
	labelSecOff := BufHeaderSize +
		beadCount*BufBeadStride +
		nodeCount*BufNodeStride +
		nodeCount*BufInteriorSlotsPerNode*BufInteriorStride +
		edgeCount*BufEdgeStride +
		portCount*BufPortStride +
		BufCameraStride +
		BufOverlayStride +
		BufSceneStride
	// The label section is followed by the EVENT block + port-name + edge-label sections, so
	// its end is the start of the event block, not the snapshot end. Verify the full length.
	eventCount := int(readU32(snap, 24))
	portNameBytesCount := int(readU32(snap, 28))
	edgeLabelBytesCount := int(readU32(snap, 32))
	fullLen := labelSecOff + labelBytesCount +
		eventCount*BufEventStride + portNameBytesCount + edgeLabelBytesCount
	if fullLen != len(snap) {
		t.Fatalf("computed full length %d != snapshot len %d", fullLen, len(snap))
	}

	nodeOff := BufHeaderSize + beadCount*BufBeadStride
	want := []string{"alpha", "β-node", ""}
	for i := 0; i < nodeCount; i++ {
		off := int(readU32(snap, nodeOff+i*BufNodeStride+BufNodeColLabelOff))
		ln := int(readU32(snap, nodeOff+i*BufNodeStride+BufNodeColLabelLen))
		got := string(snap[labelSecOff+off : labelSecOff+off+ln])
		if got != want[i] {
			t.Errorf("node %d label: got %q, want %q", i, got, want[i])
		}
	}
	// β is 2 bytes: "β-node" = 2+1+4 = 7 bytes; total = 5 + 7 + 0 = 12.
	if labelBytesCount != 12 {
		t.Errorf("labelBytesCount: got %d, want 12", labelBytesCount)
	}
}
