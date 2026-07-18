package Wiring

import (
	"reflect"
	"testing"
)

// populate_data_held_seed_test.go — locks in: a `data.state` seed (e.g. Held on
// HoldNewSendOld/Hold/Pacer) is OPTIONAL. When the spec omits the key, populateData
// must leave the constructor's default untouched (the empty sentinel, NoValue = -1
// for held-bearing kinds) rather than overwriting it with Go's int zero-value 0 —
// 0 is a legitimate held bead value, so defaulting to 0 would emit a phantom bead.
// Only a key PRESENT in data.state overrides the constructor default.
type heldSeedFixture struct {
	Held int `wire:"data.state"`
}

func TestPopulateDataHeldSeedOptional(t *testing.T) {
	cases := []struct {
		name  string
		state map[string]int
		want  int
	}{
		{"absent key keeps constructor default (NoValue)", nil, NoValue},
		{"authored held:0 is honored, not treated as unset", map[string]int{"held": 0}, 0},
		{"authored held:-1 stays -1", map[string]int{"held": -1}, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := &heldSeedFixture{Held: NoValue} // mirrors the registered constructor default
			data := &NodeData{State: c.state}
			populateData(reflect.ValueOf(n).Elem(), n, data)
			if n.Held != c.want {
				t.Fatalf("Held = %d, want %d", n.Held, c.want)
			}
		})
	}
}
