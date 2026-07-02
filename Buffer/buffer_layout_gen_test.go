// Unit tests for Buffer/buffer_layout_gen.go — typed writer round-trips.
//
// Each test writes known values via the generated Set*Row helpers and asserts
// the correct bytes appear at the expected offsets in a raw []byte buffer.
// This is the trust frontier of the schema: it exercises column offset math
// (offset, stride, endianness) for every block.

package Buffer

import (
	"encoding/binary"
	"math"
	"testing"
)

// assertF32At asserts that buf[offset:offset+4] equals the LE encoding of want.
func assertF32At(t *testing.T, buf []byte, offset int, want float32, label string) {
	t.Helper()
	got := math.Float32frombits(binary.LittleEndian.Uint32(buf[offset:]))
	if got != want {
		t.Errorf("%s: got %v, want %v (at byte offset %d)", label, got, want, offset)
	}
}

// assertI32At asserts that buf[offset:offset+4] equals the LE encoding of want.
func assertI32At(t *testing.T, buf []byte, offset int, want int32, label string) {
	t.Helper()
	got := int32(binary.LittleEndian.Uint32(buf[offset:]))
	if got != want {
		t.Errorf("%s: got %v, want %v (at byte offset %d)", label, got, want, offset)
	}
}

// assertU32At asserts that buf[offset:offset+4] equals the LE encoding of want.
func assertU32At(t *testing.T, buf []byte, offset int, want uint32, label string) {
	t.Helper()
	got := binary.LittleEndian.Uint32(buf[offset:])
	if got != want {
		t.Errorf("%s: got %v, want %v (at byte offset %d)", label, got, want, offset)
	}
}

// assertU8At asserts that buf[offset] equals want.
func assertU8At(t *testing.T, buf []byte, offset int, want uint8, label string) {
	t.Helper()
	if buf[offset] != want {
		t.Errorf("%s: got %v, want %v (at byte offset %d)", label, buf[offset], want, offset)
	}
}

func TestSetBeadRow(t *testing.T) {
	buf := make([]byte, BufBeadStride*2)
	// Write row 0.
	SetBeadRow(buf, 0, 1.5, -2.25, 3.0, -7, 0.75, 42, 1)
	// Write row 1 with different values to verify stride independence.
	SetBeadRow(buf, 1, 10.0, 20.0, 30.0, 99, 0.5, 1, 0)

	// Row 0 assertions.
	assertF32At(t, buf, BufBeadColX, 1.5, "row0.X")
	assertF32At(t, buf, BufBeadColY, -2.25, "row0.Y")
	assertF32At(t, buf, BufBeadColZ, 3.0, "row0.Z")
	assertI32At(t, buf, BufBeadColValue, -7, "row0.Value")
	assertF32At(t, buf, BufBeadColFrac, 0.75, "row0.Frac")
	assertU32At(t, buf, BufBeadColBeadID, 42, "row0.BeadID")
	assertU8At(t, buf, BufBeadColLive, 1, "row0.Live")

	// Row 1 assertions (offset by BufBeadStride = 25 bytes).
	base := BufBeadStride
	assertF32At(t, buf, base+BufBeadColX, 10.0, "row1.X")
	assertF32At(t, buf, base+BufBeadColY, 20.0, "row1.Y")
	assertF32At(t, buf, base+BufBeadColZ, 30.0, "row1.Z")
	assertI32At(t, buf, base+BufBeadColValue, 99, "row1.Value")
	assertF32At(t, buf, base+BufBeadColFrac, 0.5, "row1.Frac")
	assertU32At(t, buf, base+BufBeadColBeadID, 1, "row1.BeadID")
	assertU8At(t, buf, base+BufBeadColLive, 0, "row1.Live")
}

func TestSetNodeRow(t *testing.T) {
	buf := make([]byte, BufNodeStride)
	SetNodeRow(buf, 0,
		1.0, 2.0, 3.0, // cx, cy, cz
		0.5, 0.25, // radius, sphereR
		1,             // torusRed
		-42,           // missVal
		4.0, 5.0, 6.0, // mx, my, mz
		1, 0, 1, 0, 1, // evRecv, evFire, evSend, evArrive, evDone
		1, // selected
	)

	assertF32At(t, buf, BufNodeColCX, 1.0, "CX")
	assertF32At(t, buf, BufNodeColCY, 2.0, "CY")
	assertF32At(t, buf, BufNodeColCZ, 3.0, "CZ")
	assertF32At(t, buf, BufNodeColRadius, 0.5, "Radius")
	assertF32At(t, buf, BufNodeColSphereR, 0.25, "SphereR")
	assertU8At(t, buf, BufNodeColTorusRed, 1, "TorusRed")
	assertI32At(t, buf, BufNodeColMissVal, -42, "MissVal")
	assertF32At(t, buf, BufNodeColMX, 4.0, "MX")
	assertF32At(t, buf, BufNodeColMY, 5.0, "MY")
	assertF32At(t, buf, BufNodeColMZ, 6.0, "MZ")
	assertU8At(t, buf, BufNodeColEvRecv, 1, "EvRecv")
	assertU8At(t, buf, BufNodeColEvFire, 0, "EvFire")
	assertU8At(t, buf, BufNodeColEvSend, 1, "EvSend")
	assertU8At(t, buf, BufNodeColEvArrive, 0, "EvArrive")
	assertU8At(t, buf, BufNodeColEvDone, 1, "EvDone")
	assertU8At(t, buf, BufNodeColSelected, 1, "Selected")
}

func TestSetEdgeRow(t *testing.T) {
	buf := make([]byte, BufEdgeStride*2)
	SetEdgeRow(buf, 0, 1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 2, 5)
	SetEdgeRow(buf, 1, 7.0, 8.0, 9.0, 10.0, 11.0, 12.0, -1, -1)

	assertF32At(t, buf, BufEdgeColSX, 1.0, "row0.SX")
	assertF32At(t, buf, BufEdgeColSY, 2.0, "row0.SY")
	assertF32At(t, buf, BufEdgeColSZ, 3.0, "row0.SZ")
	assertF32At(t, buf, BufEdgeColEX, 4.0, "row0.EX")
	assertF32At(t, buf, BufEdgeColEY, 5.0, "row0.EY")
	assertF32At(t, buf, BufEdgeColEZ, 6.0, "row0.EZ")
	assertI32At(t, buf, BufEdgeColSrcNodeRow, 2, "row0.SrcNodeRow")
	assertI32At(t, buf, BufEdgeColDstNodeRow, 5, "row0.DstNodeRow")

	base := BufEdgeStride
	assertF32At(t, buf, base+BufEdgeColSX, 7.0, "row1.SX")
	assertF32At(t, buf, base+BufEdgeColEZ, 12.0, "row1.EZ")
	assertI32At(t, buf, base+BufEdgeColSrcNodeRow, -1, "row1.SrcNodeRow")
	assertI32At(t, buf, base+BufEdgeColDstNodeRow, -1, "row1.DstNodeRow")
}

func TestSetCameraRow(t *testing.T) {
	buf := make([]byte, BufCameraStride)
	SetCameraRow(buf, 1.0, 2.0, 3.0, 10.0, 0.5, 1.0, 0.25, 0.75)

	assertF32At(t, buf, BufCameraColPX, 1.0, "PX")
	assertF32At(t, buf, BufCameraColPY, 2.0, "PY")
	assertF32At(t, buf, BufCameraColPZ, 3.0, "PZ")
	assertF32At(t, buf, BufCameraColR, 10.0, "R")
	assertF32At(t, buf, BufCameraColPosTheta, 0.5, "PosTheta")
	assertF32At(t, buf, BufCameraColPosPhi, 1.0, "PosPhi")
	assertF32At(t, buf, BufCameraColUpTheta, 0.25, "UpTheta")
	assertF32At(t, buf, BufCameraColUpPhi, 0.75, "UpPhi")
}

func TestSetOverlayRow(t *testing.T) {
	buf := make([]byte, BufOverlayStride)
	SetOverlayRow(buf, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0)

	assertU8At(t, buf, BufOverlayColSceneTori, 1, "SceneTori")
	assertU8At(t, buf, BufOverlayColScenePoles, 0, "ScenePoles")
	assertU8At(t, buf, BufOverlayColNodePoles, 1, "NodePoles")
	assertU8At(t, buf, BufOverlayColAngleLabels, 0, "AngleLabels")
	assertU8At(t, buf, BufOverlayColSelSpherePoles, 1, "SelSpherePoles")
	assertU8At(t, buf, BufOverlayColHandholds, 0, "Handholds")
	assertU8At(t, buf, BufOverlayColLabelsGlobal, 1, "LabelsGlobal")
	assertU8At(t, buf, BufOverlayColBadgesGlobal, 0, "BadgesGlobal")
	assertU8At(t, buf, BufOverlayColOverlaysVis, 1, "OverlaysVis")
	assertU8At(t, buf, BufOverlayColDoubleLinks, 0, "DoubleLinks")
}

func TestBeadStrideIsPackedSize(t *testing.T) {
	// Bead block: 3×f32 + i32 + f32 + u32 + u8 = 5×4 + 4 + 4 + 1 = 6×4+1 = 25
	want := 3*4 + 4 + 4 + 4 + 1
	if BufBeadStride != want {
		t.Errorf("BufBeadStride = %d, want %d (packed size)", BufBeadStride, want)
	}
}

func TestNodeStrideIsPackedSize(t *testing.T) {
	// Node block: 5×f32 + u8 + i32 + 3×f32 + 5×u8 (events) + 1×u8 (selected)
	//           = (5+3)×4 + 1 + 4 + 5 + 1 = 32+11 = 43
	want := 5*4 + 1 + 4 + 3*4 + 5*1 + 1*1
	if BufNodeStride != want {
		t.Errorf("BufNodeStride = %d, want %d (packed size)", BufNodeStride, want)
	}
}

func TestEdgeStrideIsPackedSize(t *testing.T) {
	// Edge block: 6×f32 + 2×i32 = 32
	want := 6*4 + 2*4
	if BufEdgeStride != want {
		t.Errorf("BufEdgeStride = %d, want %d (packed size)", BufEdgeStride, want)
	}
}

func TestCameraStrideIsPackedSize(t *testing.T) {
	// Camera block: 8×f32 = 32
	want := 8 * 4
	if BufCameraStride != want {
		t.Errorf("BufCameraStride = %d, want %d (packed size)", BufCameraStride, want)
	}
}

func TestOverlayStrideIsPackedSize(t *testing.T) {
	// Overlay block: 10×u8 = 10
	want := 10
	if BufOverlayStride != want {
		t.Errorf("BufOverlayStride = %d, want %d (packed size)", BufOverlayStride, want)
	}
}

func TestEventEnumValues(t *testing.T) {
	// Event enum ids must be 0-based contiguous to match the TS side.
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"Recv", BufEventRecvID, 0},
		{"Fire", BufEventFireID, 1},
		{"Send", BufEventSendID, 2},
		{"Arrive", BufEventArriveID, 3},
		{"Done", BufEventDoneID, 4},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("BufEvent%sID = %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestVersionGenerated(t *testing.T) {
	if BufLayoutVersionGenerated != BufLayoutVersion {
		t.Errorf("BufLayoutVersionGenerated (%d) != BufLayoutVersion (%d) — regenerate", BufLayoutVersionGenerated, BufLayoutVersion)
	}
}
