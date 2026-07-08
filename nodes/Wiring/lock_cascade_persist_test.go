package Wiring

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// lock_cascade_persist_test.go — regression guard for the "lock survives run" bug: a polar
// r-lock cascade correctly updates a FOLLOWER node's held geometry in memory (lockRecalc),
// but that lock-adjusted position must ALSO reach disk on its OWN (nodeMover.persistPos →
// MoveDispatch.persistNodePos), not just the dragged node's (RootMove's emit-loop persist).
// Before the fix, a Go respawn (run) reloaded the follower's STALE pre-cascade meta.json
// position and LoadPolarEqs reinstalled the lock without re-running the cascade, so the
// follower rendered off-constraint after reload.

// writeLockCascadeTree lays down a 3-node directory-tree topology (n1 Center, n2, n3) with
// per-node meta.json, so EnableEditPersist has a real <root>/nodes/<id>/meta.json to write.
func writeLockCascadeTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	mk("nodes/n1/meta.json", `{"id":"n1","type":"AimedSink","r":100,"scenePolarR":30,"scenePolarTheta":1.0,"scenePolarPhi":0.3}`)
	mk("nodes/n1/inputs/In.json", `{"name":"In"}`)
	mk("nodes/n2/meta.json", `{"id":"n2","type":"AimedSink","r":100,"scenePolarR":60,"scenePolarTheta":1.2,"scenePolarPhi":0.5}`)
	mk("nodes/n2/inputs/In.json", `{"name":"In"}`)
	mk("nodes/n3/meta.json", `{"id":"n3","type":"AimedSink","r":100,"scenePolarR":45,"scenePolarTheta":0.8,"scenePolarPhi":-0.4}`)
	mk("nodes/n3/inputs/In.json", `{"name":"In"}`)
	if err := os.MkdirAll(filepath.Join(root, "edges"), 0o755); err != nil {
		t.Fatalf("mkdir edges: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "view", "nodes"), 0o755); err != nil {
		t.Fatalf("mkdir view/nodes: %v", err)
	}
	return root
}

// TestLockCascadeFollowerSelfPersists drags n2 under an (n2,r)=(n3,r) eqNodeNode lock about
// Center n1, lets the decentralized cascade move follower n3, and asserts n3's OWN
// lock-adjusted scene polar reaches n3's meta.json on disk — not just n2's (the dragged
// node). This pins the fix in nodeMover.handle (moveMsgKindLockUpdate): each mover persists
// its own cascade-derived position via persistPos, since RootMove's emit-loop only ever
// covers the dragged node.
func TestLockCascadeFollowerSelfPersists(t *testing.T) {
	root := writeLockCascadeTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := T.New(1024)
	_, _, md, err := LoadTopology(ctx, root, tr, NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	md.EnableEditPersist(root)

	md.setPolarEqs([]polarEq{
		{Center: "n1", A: polarTerm{"n2", compR, 1}, B: polarTerm{"n3", compR, 1}, Active: true},
	})
	md.Start(ctx)

	n1Before, ok := md.centerOfNode("n1")
	if !ok {
		t.Fatalf("n1 has no center before move")
	}

	// Drag n2 away from n1 so the cascade must move n3's r to re-equalize.
	md.RootMove("n2", vec3{X: 20, Y: 55, Z: -10})
	time.Sleep(80 * time.Millisecond)

	n1, ok := md.centerOfNode("n1")
	if !ok {
		t.Fatalf("n1 has no center after move")
	}
	n2, ok := md.centerOfNode("n2")
	if !ok {
		t.Fatalf("n2 has no center after move")
	}
	n3, ok := md.centerOfNode("n3")
	if !ok {
		t.Fatalf("n3 has no center after move")
	}
	_ = n1Before
	distN2 := n2.sub(n1).length()
	distN3 := n3.sub(n1).length()
	if math.Abs(distN2-distN3) > 1e-6 {
		t.Fatalf("edge length about n1 not equalized in memory: |n2-n1|=%g |n3-n1|=%g", distN2, distN3)
	}

	// Force the debounced writers to flush now (the test doesn't want to wait out the real
	// debounce interval). Both n2 (dragged, from RootMove) and n3 (follower, self-persisted
	// from within its own mover goroutine) must have scheduled a write.
	md.posPersist.flush()

	readScenePolar := func(id string) polar {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(root, "nodes", id, "meta.json"))
		if err != nil {
			t.Fatalf("read %s meta.json: %v", id, err)
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatalf("unmarshal %s meta.json: %v", id, err)
		}
		var r, theta, phi float64
		_ = json.Unmarshal(obj["scenePolarR"], &r)
		_ = json.Unmarshal(obj["scenePolarTheta"], &theta)
		_ = json.Unmarshal(obj["scenePolarPhi"], &phi)
		return polar{R: r, Theta: theta, Phi: phi}
	}

	n3Disk := readScenePolar("n3")
	origN3 := polar{R: 45, Theta: 0.8, Phi: -0.4}
	if n3Disk == origN3 {
		t.Fatalf("n3's meta.json still holds its STALE pre-cascade polar %+v — the lock-adjusted follower position was never persisted", n3Disk)
	}

	// Reload from disk (simulating the Go respawn a "run" click triggers) and confirm the
	// reloaded n3 position matches the in-memory cascade result, i.e. the lock survives.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	tr2 := T.New(1024)
	_, _, md2, err := LoadTopology(ctx2, root, tr2, NewFakeClock())
	if err != nil {
		t.Fatalf("reload LoadTopology: %v", err)
	}
	n3Reloaded, ok := md2.centerOfNode("n3")
	if !ok {
		t.Fatalf("reloaded n3 has no center")
	}
	if got, want := n3Reloaded, n3; got.sub(want).length() > 1e-6 {
		t.Fatalf("reloaded n3 center=%+v want %+v (cascade-adjusted position must survive reload)", got, want)
	}
}
