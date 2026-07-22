// validate.go — parse-time spec validation for topology JSON.
//
// validateSpec runs immediately after JSON unmarshal in LoadTopology, before
// any graph construction.  It aggregates all spec-shape errors and
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

// validateSpec checks the parsed topoSpec for shape errors that are
// decidable without constructing any Go objects.  It returns a
// combined error listing every problem found, or nil if the spec is valid.
func validateSpec(spec *topoSpec) error {
	var errs []string

	// Build per-kind port sets needed by the handle and required-port checks.
	kindInPorts := map[string]map[string]bool{}
	kindOutPorts := map[string]map[string]bool{}
	kindBroadcastPorts := map[string]map[string]bool{}
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
			case PortBroadcast:
				outMultis[p.Name] = true
				outs[p.Name] = true
			}
		}
		kindInPorts[kind] = ins
		kindOutPorts[kind] = outs
		kindBroadcastPorts[kind] = outMultis
	}

	// Check 1: unknown node kinds.
	// also caught by TS parser; defense-in-depth
	nodeType := map[string]string{}
	seenID := map[string]bool{}
	for _, n := range spec.Nodes {
		if seenID[n.ID] {
			// A duplicate id would silently overwrite nodeType[n.ID] (last-wins), so
			// edges then validate against the wrong kind. Reject it explicitly.
			errs = append(errs, fmt.Sprintf("duplicate node id %q", n.ID))
			continue
		}
		seenID[n.ID] = true
		nodeType[n.ID] = n.Type
		if _, ok := Registry[n.Type]; !ok {
			errs = append(errs, fmt.Sprintf("node %q: unknown type %q", n.ID, n.Type))
		}
	}

	// Check 1b: node ids and port names must be safe single path segments — they are
	// later filepath.Join'd into per-entity persistence paths (node meta.json, port anchor
	// files); a value like "../../x" would otherwise escape the tree root (path traversal).
	for _, n := range spec.Nodes {
		if !safeTreePathComponent(n.ID) {
			errs = append(errs, fmt.Sprintf("node id %q is not a safe path component", n.ID))
		}
		for _, p := range n.Inputs {
			if !safeTreePathComponent(p.Name) {
				errs = append(errs, fmt.Sprintf("node %q: input port name %q is not a safe path component", n.ID, p.Name))
			}
		}
		for _, p := range n.Outputs {
			if !safeTreePathComponent(p.Name) {
				errs = append(errs, fmt.Sprintf("node %q: output port name %q is not a safe path component", n.ID, p.Name))
			}
		}
	}

	// Check 2: empty edge labels.
	// also caught by TS parser; defense-in-depth
	for _, e := range spec.Edges {
		if e.Label == "" {
			errs = append(errs, fmt.Sprintf("edge %q→%q has empty label", e.Source, e.Target))
		}
	}

	// Check 3: port handle names must match declared ports on the node kind.
	// A dangling endpoint (edge referencing a node id absent from spec.Nodes) is
	// caught first with a clear message — otherwise nodeType[...] returns "" and
	// the port check below would misdirect with "not an output port on kind \"\"".
	for _, e := range spec.Edges {
		srcKind, srcKnown := nodeType[e.Source]
		if !srcKnown {
			errs = append(errs, fmt.Sprintf("edge %q references unknown node id %q as its source", e.Label, e.Source))
		} else {
			srcHandle := e.SourceHandle
			if base, isMulti := broadcastBaseName(srcHandle, srcKind, kindBroadcastPorts); isMulti {
				srcHandle = base
			}
			if !kindOutPorts[srcKind][srcHandle] {
				errs = append(errs, fmt.Sprintf("edge %q: sourceHandle %q is not an output port on kind %q", e.Label, e.SourceHandle, srcKind))
			}
		}
		tgtKind, tgtKnown := nodeType[e.Target]
		if !tgtKnown {
			errs = append(errs, fmt.Sprintf("edge %q references unknown node id %q as its target", e.Label, e.Target))
		} else if !kindInPorts[tgtKind][e.TargetHandle] {
			errs = append(errs, fmt.Sprintf("edge %q: targetHandle %q is not an input port on kind %q", e.Label, e.TargetHandle, tgtKind))
		}
	}

	// (No required-inbound-edge check: a node with an unfed required port loads and stays
	// inert by precondition-gating — it paces on the shared clock and polls a port that
	// never delivers, so it simply never fires. Nothing flags it: the editor's red-node
	// diagnostic for a missing required input was removed, so an unfed port is silent by
	// design, not merely unenforced here. What makes "loads and stays inert" TRUE rather
	// than aspirational is that an unwired In holds a real clock and a placeholder channel
	// — see In.Clock / wireInPort. It used to panic instead.)

	// Check 4: sendRules values must be recognised SendRule constants.
	for _, n := range spec.Nodes {
		if n.Data == nil || n.Data.SendRules == nil {
			continue
		}
		for port, raw := range n.Data.SendRules {
			if _, err := ParseSendRule(raw); err != nil {
				errs = append(errs, fmt.Sprintf("node %q port %q: %v", n.ID, port, err))
			}
		}
	}

	// data.state seed keys are OPTIONAL: an absent key defaults to the kind's
	// constructor value (the empty sentinel for held-bearing kinds), so there is
	// nothing to require here — a missing seed means "start empty," not an error.

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("LoadTopology: spec validation failed:\n  " + strings.Join(errs, "\n  "))
}
