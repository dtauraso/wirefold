// loader_tree.go — directory-tree topology reader.
//
// loadTree reads a topology laid out as a directory tree:
//
//	<root>/nodes/<id>/meta.json        — id, type
//	<root>/nodes/<id>/data.json        — NodeData (optional)
//	<root>/nodes/<id>/inputs/<name>.json  — specPort
//	<root>/nodes/<id>/outputs/<name>.json — specPort
//	<root>/edges/<label>.json          — specEdge
//	<root>/view/nodes/<id>.json        — specPosition
//
// It returns a topoSpec equivalent to what json.Unmarshal would produce from
// the monolithic topology.json, enabling LoadTopology to accept either form.

package Wiring

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// jsonPort is a local unmarshal helper for port JSON files (lowercase keys).
// specPort already has json tags with the same lowercase names, so we reuse
// specPort directly; this type documents the expected shape.
// (kept as a comment — see readPort below which unmarshals directly into specPort)

// jsonMeta is the shape of nodes/<id>/meta.json.
type jsonMeta struct {
	ID   string   `json:"id"`
	Type string   `json:"type"`
	R    *float64 `json:"r,omitempty"` // optional per-node sphere radius; nil → defaultNodeR (see nodeR)
	// Scene polar (polar-frame-rewrite.md) — the node's position as (r,θ,φ) about the scene
	// sphere center. This is the authoritative stored position. These MUST be carried through
	// to specNode below: dropping them collapses every node to the origin (the blob bug).
	ScenePolarR     *float64 `json:"scenePolarR,omitempty"`
	ScenePolarTheta *float64 `json:"scenePolarTheta,omitempty"`
	ScenePolarPhi   *float64 `json:"scenePolarPhi,omitempty"`
	// Quantized polar offset (quantized_layout.go PHASE 3) — see specNode's field doc.
	QuantITheta *int `json:"quantITheta,omitempty"`
	QuantIPhi   *int `json:"quantIPhi,omitempty"`
	QuantIR     *int `json:"quantIR,omitempty"`
	// Per-node step constants — see specNode's field doc (loader.go).
	StepTheta *float64 `json:"stepTheta,omitempty"`
	StepPhi   *float64 `json:"stepPhi,omitempty"`
	StepR     *float64 `json:"stepR,omitempty"`
}

// loadTree reads the directory-tree topology rooted at root and assembles a
// topoSpec.  All subdirectory entries are sorted so the result is deterministic.
func loadTree(root string) (topoSpec, error) {
	var spec topoSpec

	// ── nodes ────────────────────────────────────────────────────────────────
	nodesDir := filepath.Join(root, "nodes")
	nodeDirs, err := readDirNames(nodesDir)
	if err != nil {
		return spec, fmt.Errorf("loadTree: list nodes dir %s: %w", nodesDir, err)
	}
	sort.Strings(nodeDirs)

	for _, nodeID := range nodeDirs {
		nodeDir := filepath.Join(nodesDir, nodeID)

		// meta.json — required
		metaPath := filepath.Join(nodeDir, "meta.json")
		metaRaw, err := os.ReadFile(metaPath)
		if err != nil {
			return spec, fmt.Errorf("loadTree: node %q meta: %w", nodeID, err)
		}
		var meta jsonMeta
		if err := json.Unmarshal(metaRaw, &meta); err != nil {
			return spec, fmt.Errorf("loadTree: node %q meta parse: %w", nodeID, err)
		}

		sn := specNode{
			ID:              meta.ID,
			Type:            meta.Type,
			R:               meta.R,
			ScenePolarR:     meta.ScenePolarR,
			ScenePolarTheta: meta.ScenePolarTheta,
			ScenePolarPhi:   meta.ScenePolarPhi,
			QuantITheta:     meta.QuantITheta,
			QuantIPhi:       meta.QuantIPhi,
			QuantIR:         meta.QuantIR,
			StepTheta:       meta.StepTheta,
			StepPhi:         meta.StepPhi,
			StepR:           meta.StepR,
		}

		// data.json — optional
		dataPath := filepath.Join(nodeDir, "data.json")
		if raw, err := os.ReadFile(dataPath); err == nil {
			var nd NodeData
			if err := json.Unmarshal(raw, &nd); err != nil {
				return spec, fmt.Errorf("loadTree: node %q data parse: %w", nodeID, err)
			}
			sn.Data = &nd
		}

		// inputs/ — optional subdir
		sn.Inputs, err = readPorts(filepath.Join(nodeDir, "inputs"))
		if err != nil {
			return spec, fmt.Errorf("loadTree: node %q inputs: %w", nodeID, err)
		}

		// outputs/ — optional subdir
		sn.Outputs, err = readPorts(filepath.Join(nodeDir, "outputs"))
		if err != nil {
			return spec, fmt.Errorf("loadTree: node %q outputs: %w", nodeID, err)
		}

		spec.Nodes = append(spec.Nodes, sn)
	}

	// ── edges ────────────────────────────────────────────────────────────────
	edgesDir := filepath.Join(root, "edges")
	edgeFiles, err := readDirNames(edgesDir)
	if err != nil {
		return spec, fmt.Errorf("loadTree: list edges dir %s: %w", edgesDir, err)
	}
	sort.Strings(edgeFiles)

	for _, fname := range edgeFiles {
		if !strings.HasSuffix(fname, ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(edgesDir, fname))
		if err != nil {
			return spec, fmt.Errorf("loadTree: read edge file %s: %w", fname, err)
		}
		var e specEdge
		if err := json.Unmarshal(raw, &e); err != nil {
			return spec, fmt.Errorf("loadTree: parse edge file %s: %w", fname, err)
		}
		spec.Edges = append(spec.Edges, e)
	}

	// ── view/nodes ───────────────────────────────────────────────────────────
	viewNodesDir := filepath.Join(root, "view", "nodes")
	viewFiles, err := readDirNames(viewNodesDir)
	if err != nil {
		return spec, fmt.Errorf("loadTree: list view/nodes dir %s: %w", viewNodesDir, err)
	}
	sort.Strings(viewFiles)

	spec.View.Nodes = map[string]specPosition{}
	for _, fname := range viewFiles {
		if !strings.HasSuffix(fname, ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(viewNodesDir, fname))
		if err != nil {
			return spec, fmt.Errorf("loadTree: read view/nodes/%s: %w", fname, err)
		}
		var jp specPosition
		if err := json.Unmarshal(raw, &jp); err != nil {
			return spec, fmt.Errorf("loadTree: parse view/nodes/%s: %w", fname, err)
		}
		id := strings.TrimSuffix(fname, ".json")
		spec.View.Nodes[id] = jp
	}

	return spec, nil
}

// readPorts reads all *.json files from dir (which may not exist) and returns
// the parsed []specPort sorted by slot (nil last) then name.
func readPorts(dir string) ([]specPort, error) {
	names, err := readDirNames(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Strings(names)

	var ports []specPort
	for _, fname := range names {
		if !strings.HasSuffix(fname, ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, fname))
		if err != nil {
			return nil, err
		}
		var p specPort
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("parse %s: %w", fname, err)
		}
		ports = append(ports, p)
	}

	sort.Slice(ports, func(i, j int) bool {
		return ports[i].Name < ports[j].Name
	})
	return ports, nil
}

// readDirNames returns the names (not full paths) of all entries in dir.
func readDirNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}
