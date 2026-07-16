package main

import (
	"testing"

	B "github.com/dtauraso/wirefold/Buffer"
	Wiring "github.com/dtauraso/wirefold/nodes/Wiring"
)

// TestRegistryMatchesGeneratedKindTable pins the kind name that each node package
// registers at init() against the generated kind table in Buffer/node_kind_id_gen.go.
//
// This test lives in package main on purpose: main is the only package that imports
// every node package (via the generated kinds_generated.go blank imports), so it is the
// only place where Wiring.Registry is fully populated. Wiring's own tests register
// throwaway fixture kinds (AimedSrc, FanInSink, …) into a separate test binary's
// registry and would give a different answer.
//
// It closes two gaps that nothing else covered:
//
//  1. A typo'd Register string for a kind that no topology/ fixture instantiates.
//     Integration tests catch a typo in Hold ("unknown type") only because the fixture
//     uses Hold. HoldFlip and Pacer are registered but absent from topology/, so a typo
//     in either was previously silent.
//
//  2. A stale kinds_generated.go. A node package reaches the registry ONLY through that
//     file's blank import, so a missing import means init() never runs, Register is never
//     called, and the kind silently does not exist in the binary. The ID-set check below
//     catches that as a hole in the contiguous range.
func TestRegistryMatchesGeneratedKindTable(t *testing.T) {
	if len(Wiring.Registry) == 0 {
		t.Fatal("Wiring.Registry is empty — no node package registered; " +
			"kinds_generated.go is missing its blank imports (run: go run ./tools/gen-kind-imports)")
	}

	// Every registered kind must be in the generated table. A typo'd Register string
	// lands here: the generator reads SPEC.md dirs, so it never learns the typo.
	seen := map[uint8]string{}
	for kind := range Wiring.Registry {
		id := B.NodeKindID(kind)
		if id == B.KindIDUnknown {
			t.Errorf("kind %q is registered but absent from the generated kind table.\n"+
				"  Either the Register(%q) string is a typo, or nodes/<Kind>/SPEC.md was not "+
				"regenerated (run: cd tools/topology-vscode && npm run gen:node-defs).", kind, kind)
			continue
		}
		if prev, dup := seen[id]; dup {
			t.Errorf("kinds %q and %q share KindID %d — the generated table is corrupt", prev, kind, id)
			continue
		}
		seen[id] = kind
	}

	// IDs are a 0-based contiguous index over the generated table. If every table entry
	// is registered, the IDs collected above are exactly 0..len(Registry)-1. A hole means
	// a kind exists in the table but never registered — the stale-kinds_generated.go case.
	for want := 0; want < len(Wiring.Registry); want++ {
		if _, ok := seen[uint8(want)]; !ok {
			t.Errorf("no registered kind has KindID %d — a kind is in the generated table but "+
				"never registered.\n  Its package is probably missing from kinds_generated.go "+
				"(run: go run ./tools/gen-kind-imports).", want)
		}
	}
}
