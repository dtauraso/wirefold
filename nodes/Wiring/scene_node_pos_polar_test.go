package Wiring

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestWriteNodePositionDualWritesScenePolar: writeNodePosition records x/y/z AND the scene
// polar about the given scene center, and that polar reconstructs the world position.
func TestWriteNodePositionDualWritesScenePolar(t *testing.T) {
	root := t.TempDir()
	metaDir := filepath.Join(root, "nodes", "n1")
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(metaDir, "meta.json")
	if err := os.WriteFile(metaPath, []byte(`{"id":"n1","type":"Pulse","r":15}`), 0o600); err != nil {
		t.Fatal(err)
	}

	world := vec3{X: 120, Y: -40, Z: 60}
	sceneCenter := vec3{X: 20, Y: 5, Z: -10}
	if err := writeNodePosition(root, "n1", world, sceneCenter, true); err != nil {
		t.Fatalf("writeNodePosition: %v", err)
	}

	raw, _ := os.ReadFile(metaPath)
	var m map[string]float64
	if err := json.Unmarshal(raw, &m); err != nil {
		// id/type are strings — decode loosely instead.
		var mm map[string]any
		_ = json.Unmarshal(raw, &mm)
		get := func(k string) float64 { f, _ := mm[k].(float64); return f }
		m = map[string]float64{
			"x": get("x"), "y": get("y"), "z": get("z"),
			"scenePolarR": get("scenePolarR"), "scenePolarTheta": get("scenePolarTheta"), "scenePolarPhi": get("scenePolarPhi"),
		}
		if _, ok := mm["scenePolarR"]; !ok {
			t.Fatalf("no scenePolarR written: %s", raw)
		}
		if _, ok := mm["type"]; !ok {
			t.Fatalf("dual-write clobbered type: %s", raw)
		}
	}
	// Cartesian preserved.
	if m["x"] != world.X || m["y"] != world.Y || m["z"] != world.Z {
		t.Fatalf("x/y/z = (%v,%v,%v), want (%v,%v,%v)", m["x"], m["y"], m["z"], world.X, world.Y, world.Z)
	}
	// Scene polar reconstructs the world position: sceneCenter + polar2cart(polar) ≈ world.
	sp := polar{R: m["scenePolarR"], Theta: m["scenePolarTheta"], Phi: m["scenePolarPhi"]}
	recon := polar2cart(sp).add(sceneCenter)
	if recon.sub(world).length() > 1e-6 {
		t.Fatalf("scene polar reconstructs %+v, want %+v", recon, world)
	}
}
