// validate.go — parse-time spec validation for topology JSON.
//
// validateSpec runs immediately after JSON unmarshal in LoadTopology, before
// any substrate/graph construction.  It aggregates all spec-shape errors and
// returns them together so the caller sees every problem in one pass.
//
// Only checks decidable purely from the parsed spec are placed here.
// Genuinely runtime-only checks (e.g. wire allocation failures) stay in
// LoadTopology.

package Wiring

import (
	"fmt"
	"strings"
)

// exportedFieldName reconstructs the exported struct field name from a
// data.state key (inverse of lowerFirst): "held" → "Held".
func exportedFieldName(key string) string {
	if key == "" {
		return key
	}
	return strings.ToUpper(key[:1]) + key[1:]
}

// validateSpec checks the parsed topoSpec for shape errors that are
// decidable without constructing any substrate objects.  It returns a
// combined error listing every problem found, or nil if the spec is valid.
func validateSpec(spec *topoSpec) error {
	var errs []string

	// Build per-kind port sets needed by the handle and required-port checks.
	kindInPorts := map[string]map[string]bool{}
	kindOutPorts := map[string]map[string]bool{}
	kindOutMultiPorts := map[string]map[string]bool{}
	for kind, bind := range Registry {
		ins := map[string]bool{}
		outs := map[string]bool{}
		outMultis := map[string]bool{}
		for _, p := range bind.Ports {
			switch p.Dir {
			case PortIn:
				ins[p.Name] = true
			case PortOut:
				outs[p.Name] = true
			case PortOutMulti:
				outMultis[p.Name] = true
				outs[p.Name] = true
			}
		}
		kindInPorts[kind] = ins
		kindOutPorts[kind] = outs
		kindOutMultiPorts[kind] = outMultis
	}

	// Check 1: unknown node kinds.
	// also caught by TS parser; defense-in-depth
	nodeType := map[string]string{}
	for _, n := range spec.Nodes {
		nodeType[n.ID] = n.Type
		if _, ok := Registry[n.Type]; !ok {
			errs = append(errs, fmt.Sprintf("node %q: unknown type %q", n.ID, n.Type))
		}
	}

	// Check 2: empty edge labels.
	// also caught by TS parser; defense-in-depth
	for _, e := range spec.Edges {
		if e.Label == "" {
			errs = append(errs, fmt.Sprintf("edge %q→%q has empty label", e.Source, e.Target))
		}
	}

	// outMultiBaseName strips a trailing digit suffix from a sourceHandle when
	// the base name is an OutMulti port on the given kind.
	outMultiBaseName := func(handle, kind string) (string, bool) {
		if len(handle) == 0 {
			return handle, false
		}
		last := handle[len(handle)-1]
		if last < '0' || last > '9' {
			return handle, false
		}
		base := handle[:len(handle)-1]
		if kindOutMultiPorts[kind][base] {
			return base, true
		}
		return handle, false
	}

	// Check 3: port handle names must match declared ports on the node kind.
	for _, e := range spec.Edges {
		srcKind := nodeType[e.Source]
		srcHandle := e.SourceHandle
		if base, isMulti := outMultiBaseName(srcHandle, srcKind); isMulti {
			srcHandle = base
		}
		if !kindOutPorts[srcKind][srcHandle] {
			errs = append(errs, fmt.Sprintf("edge %q: sourceHandle %q is not an output port on kind %q", e.Label, e.SourceHandle, srcKind))
		}
		tgtKind := nodeType[e.Target]
		if !kindInPorts[tgtKind][e.TargetHandle] {
			errs = append(errs, fmt.Sprintf("edge %q: targetHandle %q is not an input port on kind %q", e.Label, e.TargetHandle, tgtKind))
		}
	}

	// Check 4: required input ports must have an inbound edge.
	// Build inbound map first.
	inbound := map[string]map[string]bool{}
	for _, e := range spec.Edges {
		if inbound[e.Target] == nil {
			inbound[e.Target] = map[string]bool{}
		}
		inbound[e.Target][e.TargetHandle] = true
	}
	for _, n := range spec.Nodes {
		bind, ok := Registry[n.Type]
		if !ok {
			continue // already reported in Check 1
		}
		for _, port := range bind.Ports {
			if !port.Required {
				continue
			}
			if !inbound[n.ID][port.Name] {
				errs = append(errs, fmt.Sprintf("node %q: required input port %q has no inbound edge", n.ID, port.Name))
			}
		}
	}

	// Check 5: required data.state keys must be present for each node kind.
	for _, n := range spec.Nodes {
		bind, ok := Registry[n.Type]
		if !ok {
			continue // already reported in Check 1
		}
		for _, key := range bind.StateKeys {
			if n.Data == nil || n.Data.State == nil {
				errs = append(errs, fmt.Sprintf("reflectBuild: node %q (kind %q): wire:\"data.state\" field %s requires data.state[%q] in topology JSON", n.ID, n.Type, exportedFieldName(key), key))
				continue
			}
			if _, ok := n.Data.State[key]; !ok {
				errs = append(errs, fmt.Sprintf("reflectBuild: node %q (kind %q): wire:\"data.state\" field %s requires data.state[%q] in topology JSON", n.ID, n.Type, exportedFieldName(key), key))
			}
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("LoadTopology: spec validation failed:\n  " + strings.Join(errs, "\n  "))
}
