package Wiring

import (
	"bytes"
	"strings"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// overlay_gen_test.go — independent behavior check for the generated overlay
// Toggle/Emit wiring in overlay_gen.go. This is the trust frontier of the
// generator: it exercises the mechanical name-derivation (flag → field / method /
// trace Kind) that replaced the hand-written methods. For EACH overlay flag it
// asserts that Toggle flips the owned overlayState bool AND emits the correct trace
// event carrying the NEW value, and that Emit re-emits the CURRENT value WITHOUT
// flipping. The exception flags (scene/node poles breadcrumb, angleLabels accessor,
// tori/overlays method renames, doubleLinks default-off) are covered explicitly.

// overlayCase describes one generated overlay flag by its behavior, using closures
// so the test reads/writes the concrete overlayState field without reflection.
type overlayCase struct {
	name   string
	get    func(*overlayState) bool
	toggle func(*overlayState, *T.Trace)
	emit   func(*overlayState, *T.Trace)
	kind   string // trace Event.Kind emitted by toggle/emit
	crumb  string // non-empty => breadcrumb node arg expected on Toggle (poles)
}

var overlayCases = []overlayCase{
	{
		name:   "tori",
		get:    func(o *overlayState) bool { return o.sceneToriVisible },
		toggle: (*overlayState).ToggleSceneTori,
		emit:   (*overlayState).EmitSceneTori,
		kind:   T.KindSceneTori,
	},
	{
		name:   "scenePoles",
		get:    func(o *overlayState) bool { return o.scenePolesVisible },
		toggle: (*overlayState).ToggleScenePoles,
		emit:   (*overlayState).EmitScenePoles,
		kind:   T.KindScenePoles,
		crumb:  "scene",
	},
	{
		name:   "nodePoles",
		get:    func(o *overlayState) bool { return o.nodePolesVisible },
		toggle: (*overlayState).ToggleNodePoles,
		emit:   (*overlayState).EmitNodePoles,
		kind:   T.KindNodePoles,
		crumb:  "nodes",
	},
	{
		name:   "angleLabels",
		get:    func(o *overlayState) bool { return o.angleLabelsVisible },
		toggle: (*overlayState).ToggleAngleLabels,
		emit:   (*overlayState).EmitAngleLabels,
		kind:   T.KindAngleLabels,
	},
	{
		name:   "selSpherePoles",
		get:    func(o *overlayState) bool { return o.selSpherePolesVisible },
		toggle: (*overlayState).ToggleSelSpherePoles,
		emit:   (*overlayState).EmitSelSpherePoles,
		kind:   T.KindSelSpherePoles,
	},
	{
		name:   "handholds",
		get:    func(o *overlayState) bool { return o.handholdsVisible },
		toggle: (*overlayState).ToggleHandholds,
		emit:   (*overlayState).EmitHandholds,
		kind:   T.KindHandholds,
	},
	{
		name:   "labelsGlobal",
		get:    func(o *overlayState) bool { return o.labelsGlobalVisible },
		toggle: (*overlayState).ToggleLabelsGlobal,
		emit:   (*overlayState).EmitLabelsGlobal,
		kind:   T.KindLabelsGlobal,
	},
	{
		name:   "badgesGlobal",
		get:    func(o *overlayState) bool { return o.badgesGlobalVisible },
		toggle: (*overlayState).ToggleBadgesGlobal,
		emit:   (*overlayState).EmitBadgesGlobal,
		kind:   T.KindBadgesGlobal,
	},
	{
		name:   "overlays",
		get:    func(o *overlayState) bool { return o.overlaysVisible },
		toggle: (*overlayState).ToggleOverlaysVis,
		emit:   (*overlayState).EmitOverlaysVis,
		kind:   T.KindOverlaysVis,
	},
	{
		name:   "doubleLinks",
		get:    func(o *overlayState) bool { return o.doubleLinksVisible },
		toggle: (*overlayState).ToggleDoubleLinks,
		emit:   (*overlayState).EmitDoubleLinks,
		kind:   T.KindDoubleLinks,
	},
}

// lastVisEvent returns the Visible value of the last event of kind k, or fails.
func lastVisEvent(t *testing.T, evs []T.Event, k string) bool {
	t.Helper()
	found := false
	var vis bool
	for _, e := range evs {
		if e.Kind == k {
			vis = e.Visible
			found = true
		}
	}
	if !found {
		t.Fatalf("no event of kind %q emitted; got %v", k, evs)
	}
	return vis
}

func countKind(evs []T.Event, k string) int {
	n := 0
	for _, e := range evs {
		if e.Kind == k {
			n++
		}
	}
	return n
}

func TestOverlayToggleFlipsAndEmits(t *testing.T) {
	for _, c := range overlayCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			// Start from a known false, so a flip lands on true and the emitted
			// value must be the NEW (post-flip) value.
			var o overlayState
			var sink bytes.Buffer
			tr := T.NewWithSink(1024, &sink)
			c.toggle(&o, tr)
			tr.Close()

			if !c.get(&o) {
				t.Fatalf("%s: Toggle did not flip field false->true", c.name)
			}
			if vis := lastVisEvent(t, tr.Events(), c.kind); vis != true {
				t.Fatalf("%s: Toggle emitted %s visible=%v, want true (the new value)", c.name, c.kind, vis)
			}
			// Breadcrumb goes to the sink only (not buffered events); poles must
			// emit one naming their scope.
			if c.crumb != "" {
				s := sink.String()
				if !strings.Contains(s, "pole-toggle-go") || !strings.Contains(s, `"node":"`+c.crumb+`"`) {
					t.Fatalf("%s: Toggle did not emit breadcrumb for scope %q; sink=%q", c.name, c.crumb, s)
				}
			}
		})
	}
}

func TestOverlayEmitDoesNotFlip(t *testing.T) {
	for _, c := range overlayCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			// Emit must report the CURRENT value unchanged. Test both values.
			for _, want := range []bool{true, false} {
				var o overlayState
				setOverlayField(&o, c.name, want)
				tr := T.New(1024)
				c.emit(&o, tr)
				tr.Close()

				if c.get(&o) != want {
					t.Fatalf("%s: Emit mutated field to %v, want unchanged %v", c.name, c.get(&o), want)
				}
				if got := lastVisEvent(t, tr.Events(), c.kind); got != want {
					t.Fatalf("%s: Emit emitted %s visible=%v, want current %v", c.name, c.kind, got, want)
				}
				if n := countKind(tr.Events(), c.kind); n != 1 {
					t.Fatalf("%s: Emit produced %d %s events, want exactly 1", c.name, n, c.kind)
				}
			}
		})
	}
}

// setOverlayField sets one named overlay field, so the Emit test can seed a value
// without threading a setter closure through every case.
func setOverlayField(o *overlayState, name string, v bool) {
	switch name {
	case "tori":
		o.sceneToriVisible = v
	case "scenePoles":
		o.scenePolesVisible = v
	case "nodePoles":
		o.nodePolesVisible = v
	case "angleLabels":
		o.angleLabelsVisible = v
	case "selSpherePoles":
		o.selSpherePolesVisible = v
	case "handholds":
		o.handholdsVisible = v
	case "labelsGlobal":
		o.labelsGlobalVisible = v
	case "badgesGlobal":
		o.badgesGlobalVisible = v
	case "overlays":
		o.overlaysVisible = v
	case "doubleLinks":
		o.doubleLinksVisible = v
	default:
		panic("unknown overlay field " + name)
	}
}

// TestAngleLabelsAccessor covers the one generated bare-bool accessor.
func TestAngleLabelsAccessor(t *testing.T) {
	var o overlayState
	if o.AngleLabels() {
		t.Fatal("AngleLabels() should be false on zero overlayState")
	}
	o.angleLabelsVisible = true
	if !o.AngleLabels() {
		t.Fatal("AngleLabels() should mirror angleLabelsVisible=true")
	}
}

// TestDefaultOverlayState pins the startup snapshot: every flag defaults ON except
// doubleLinks (the one defaultOff override).
func TestDefaultOverlayState(t *testing.T) {
	d := defaultOverlayState()
	on := []bool{
		d.sceneToriVisible, d.scenePolesVisible, d.nodePolesVisible,
		d.angleLabelsVisible, d.selSpherePolesVisible, d.handholdsVisible,
		d.labelsGlobalVisible, d.badgesGlobalVisible, d.overlaysVisible,
	}
	for i, v := range on {
		if !v {
			t.Fatalf("defaultOverlayState field #%d should default ON", i)
		}
	}
	if d.doubleLinksVisible {
		t.Fatal("defaultOverlayState doubleLinksVisible should default OFF")
	}
}
