package Wiring

import "testing"

func TestMovementLinksRegistered(t *testing.T) {
	md := &MoveDispatch{}
	all := map[string]bool{"1": true, "2": true, "3": true, "5": true, "6": true,
		"7": true, "8": true, "9": true, "10": true, "11": true}
	md.registerMovementLinks(func(id string) bool { return all[id] })
	if len(md.links) != 11 {
		t.Fatalf("expected 11 double links, got %d", len(md.links))
	}
	// Partial topology: links touching a missing node are skipped.
	md2 := &MoveDispatch{}
	noEleven := map[string]bool{}
	for k := range all {
		noEleven[k] = true
	}
	delete(noEleven, "11")
	md2.registerMovementLinks(func(id string) bool { return noEleven[id] })
	if len(md2.links) != 9 { // drops 10↔11 and 6↔11
		t.Fatalf("expected 9 links without node 11, got %d", len(md2.links))
	}
}
