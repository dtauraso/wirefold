package Wiring

// port_torus_enforcement_test.go — STAGE 3 of the `port ∈ torus` lock: when a lock is
// ACTIVE, the constrained port's world position is ring-projected (portWorldPosAimed /
// segmentBetweenPortsAimed in aimed_ports.go) so it sits ON its own node's border ring
// (the world z=0 plane through the node's center, radius nodeRadius(kind)), even when
// the connected node it aims at sits at a different z. Inactive locks fall back to the
// plain aimed position (regression, matching STAGE 1/2 behavior).

import "testing"

// TestPortWorldPosAimed_ActiveTorusLock_ProjectsOntoRing verifies that an ACTIVE
// eqPortTorus lock on a port whose aimed direction leaves the ring plane (the
// connected node sits at a different z) is corrected onto the node's own ring: the
// resolved port position lies in the node's z=center.Z plane at distance
// nodeRadius(kind) from center, and the edge endpoint (segmentBetweenPortsAimed)
// equals that same point (marker == endpoint stays true).
func TestPortWorldPosAimed_ActiveTorusLock_ProjectsOntoRing(t *testing.T) {
	sCenter := vec3{X: 0, Y: 0, Z: 5}
	dCenter := vec3{X: 10, Y: 3, Z: 40} // very different z — aim dir is NOT in S's ring plane

	gS := buildGeom("HoldNewSendOld", sCenter)
	gD := buildGeom("HoldNewSendOld", dCenter)

	registry := AimedPortRegistry{
		{NodeID: "S", PortName: "ToHoldNewSendOld", IsInput: false}:          "D",
		{NodeID: "D", PortName: "FromPrevHoldNewSendOldNode", IsInput: true}: "S",
	}
	centers := map[string]vec3{"S": sCenter, "D": dCenter}
	centerOf := func(id string) (vec3, bool) { v, ok := centers[id]; return v, ok }

	// Only S's port carries an ACTIVE eqPortTorus lock; D's does not.
	locked := func(nodeID, portName string, isInput bool) bool {
		return nodeID == "S" && portName == "ToHoldNewSendOld" && !isInput
	}

	sPort := portWorldPosAimed(gS, "ToHoldNewSendOld", false, "S", registry, centerOf, locked)

	if sPort.Z != sCenter.Z {
		t.Fatalf("locked port S.port.z=%v, want S_center.z=%v (ring plane)", sPort.Z, sCenter.Z)
	}
	dist := sPort.sub(sCenter).length()
	wantR := nodeRadius(gS.Kind)
	const eps = 1e-9
	if dist < wantR-eps || dist > wantR+eps {
		t.Fatalf("locked port distance from center = %v, want nodeRadius(kind) = %v", dist, wantR)
	}

	// The edge endpoint computed via segmentBetweenPortsAimed must be the SAME point
	// as the streamed port marker — both come from portWorldPosAimed with the same
	// locked check.
	seg := segmentBetweenPortsAimed(gS, "ToHoldNewSendOld", "S", gD, "FromPrevHoldNewSendOldNode", "D", registry, centerOf, locked)
	if !approxVec3(seg.Start, sPort, eps) {
		t.Fatalf("edge segment Start %+v != locked port marker position %+v", seg.Start, sPort)
	}
}

// TestPortWorldPosAimed_PartnerTorusLock_ProjectsInputOntoRing verifies that when
// only the OUT-port of a bidirectional connection carries an ACTIVE eqPortTorus lock,
// the paired IN-port on the same node (aiming at the same neighbor — the opposite
// direction of the same connection) also ring-projects, so the two directed edges
// stay coincident instead of splitting into two visible lines.
func TestPortWorldPosAimed_PartnerTorusLock_ProjectsInputOntoRing(t *testing.T) {
	xCenter := vec3{X: 0, Y: 0, Z: 5}
	tCenter := vec3{X: 10, Y: 3, Z: 40} // very different z — aim dir leaves X's ring plane

	gX := buildGeom("HoldNewSendOld", xCenter)

	registry := AimedPortRegistry{
		{NodeID: "X", PortName: "ToHoldNewSendOld", IsInput: false}:          "T",
		{NodeID: "X", PortName: "FromPrevHoldNewSendOldNode", IsInput: true}: "T",
	}
	centers := map[string]vec3{"X": xCenter, "T": tCenter}
	centerOf := func(id string) (vec3, bool) { v, ok := centers[id]; return v, ok }

	// Only X's OUT-port carries an ACTIVE eqPortTorus lock; X's IN-port (the
	// partner, same node, same neighbor T) does not.
	locked := func(nodeID, portName string, isInput bool) bool {
		return nodeID == "X" && portName == "ToHoldNewSendOld" && !isInput
	}

	outPort := portWorldPosAimed(gX, "ToHoldNewSendOld", false, "X", registry, centerOf, locked)
	inPort := portWorldPosAimed(gX, "FromPrevHoldNewSendOldNode", true, "X", registry, centerOf, locked)

	const eps = 1e-9

	if inPort.Z != xCenter.Z {
		t.Fatalf("partner-locked in-port z=%v, want X_center.z=%v (ring plane)", inPort.Z, xCenter.Z)
	}
	dist := inPort.sub(xCenter).length()
	wantR := nodeRadius(gX.Kind)
	if dist < wantR-eps || dist > wantR+eps {
		t.Fatalf("partner-locked in-port distance from center = %v, want nodeRadius(kind) = %v", dist, wantR)
	}

	// Goal: the in/out pair coincide — the lock on one direction must not split
	// the connection into two separate points.
	if !approxVec3(inPort, outPort, eps) {
		t.Fatalf("in-port %+v != out-port %+v; partner lock did not keep the pair coincident", inPort, outPort)
	}
}

// TestPortWorldPosAimed_InactiveTorusLock_UsesPlainAimed is the regression case: when
// locked reports false (e.g. the lock exists but is deactivated), the port uses the
// plain aimed position — unaffected by the ring-plane, potentially off it.
func TestPortWorldPosAimed_InactiveTorusLock_UsesPlainAimed(t *testing.T) {
	sCenter := vec3{X: 0, Y: 0, Z: 5}
	dCenter := vec3{X: 10, Y: 3, Z: 40}

	gS := buildGeom("HoldNewSendOld", sCenter)

	registry := AimedPortRegistry{
		{NodeID: "S", PortName: "ToHoldNewSendOld", IsInput: false}: "D",
	}
	centers := map[string]vec3{"S": sCenter, "D": dCenter}
	centerOf := func(id string) (vec3, bool) { v, ok := centers[id]; return v, ok }

	notLocked := func(nodeID, portName string, isInput bool) bool { return false }

	gotLocked := portWorldPosAimed(gS, "ToHoldNewSendOld", false, "S", registry, centerOf, nil)
	gotUnlocked := portWorldPosAimed(gS, "ToHoldNewSendOld", false, "S", registry, centerOf, notLocked)

	const eps = 1e-9
	if !approxVec3(gotLocked, gotUnlocked, eps) {
		t.Fatalf("nil locked fn and always-false locked fn diverged: %+v vs %+v", gotLocked, gotUnlocked)
	}
	// Plain aimed position: direction toward D has non-zero Z component (D.z=40,
	// S.z=5), so the port should NOT land in S's z=center.Z ring plane.
	if gotUnlocked.Z == sCenter.Z {
		t.Fatalf("unlocked port unexpectedly landed in the ring plane (z=%v); test fixture must aim off-plane", gotUnlocked.Z)
	}
}
