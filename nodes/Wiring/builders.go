// builders.go — reflection-driven port-manifest and node construction.
//
// Adding a kind: register one entry in kindRegistry. The struct fields
// determine the port manifest automatically:
//   - *Wiring.In       → PortIn
//   - *Wiring.Out      → PortOut
//   - Wiring.OutMulti  → PortOutMulti
//   - all other field types are ignored
//
// Non-channel fields can be populated from data.* JSON values via struct tags:
//   - wire:"data.<key>"  reads NodeData.<Key> where <Key> is <key> with its
//                        first letter uppercased. Any exported field on NodeData
//                        is reachable this way (e.g. data.init → NodeData.Init).
//                        Slice fields are copied, not aliased.
//                        Mismatched or absent fields are silently skipped.
//   - wire:"data.state"  reads NodeData.State[lowerFirst(fieldName)] (int).
//                        The map key is the struct field name with its first
//                        letter lowercased (e.g. field Held → key "held").

package Wiring

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// PortDir describes which direction a port flows.
type PortDir int

const (
	PortIn       PortDir = iota
	PortOut              // single output
	PortOutMulti         // slice output ([]chan<- int)
)

// PortSpec describes one port on a node kind.
type PortSpec struct {
	Name     string
	Dir      PortDir
	Required bool // true for PortIn ports; output ports are never required
}

// PortBindings holds resolved channels or PacedWires keyed by port name.
// For PortOutMulti ports, use AppendMulti / OutSlice.
// Paced variants (SetSinglePaced / AppendMultiPacedWithHandle) take precedence over
// chan variants when both are set; in practice the loader uses only one mode.
type PortBindings struct {
	single           map[string]chan int
	multi            map[string][]chan int
	singlePaced      map[string]*PacedWire
	singleRule       map[string]SendRule    // per-port send rule for singlePaced
	singleArc        map[string]float64     // per-edge arc length for singlePaced Out
	singleLatency    map[string]float64     // per-edge sim latency for singlePaced Out
	singleSegment    map[string]wireSegment // per-edge segment endpoints for singlePaced Out
	singleLabel      map[string]string      // per-edge TS edge id for singlePaced Out
	multiPaced       map[string][]*PacedWire
	multiPacedHandle map[string][]string      // per-element source handle for multiPaced
	multiRule        map[string][]SendRule    // per-element send rule for multiPaced
	multiArc         map[string][]float64     // per-element arc length for multiPaced Out
	multiLatency     map[string][]float64     // per-element sim latency for multiPaced Out
	multiSegment     map[string][]wireSegment // per-element segment endpoints for multiPaced Out
	multiLabel       map[string][]string      // per-element TS edge id for multiPaced Out
	// outSink, when non-nil, collects every paced *Out built for this node keyed
	// by "node.handle" so the loader can index Outs by edge for node-move
	// travel-time updates. Render/run paths leave it nil.
	outSink map[string]*Out
	// clock is the single monotonic clock the loader shares with every PacedWire.
	// reflectBuild injects it into nodes that need clock-paced interior animation
	// (the Input node's refill slide). Test builds without a loader leave it nil,
	// and such nodes fall back to an instant refill.
	clock Clock
}

func newPortBindings() PortBindings {
	return PortBindings{
		single:           map[string]chan int{},
		multi:            map[string][]chan int{},
		singlePaced:      map[string]*PacedWire{},
		singleRule:       map[string]SendRule{},
		singleArc:        map[string]float64{},
		singleLatency:    map[string]float64{},
		singleSegment:    map[string]wireSegment{},
		singleLabel:      map[string]string{},
		multiPaced:       map[string][]*PacedWire{},
		multiPacedHandle: map[string][]string{},
		multiRule:        map[string][]SendRule{},
		multiArc:         map[string][]float64{},
		multiLatency:     map[string][]float64{},
		multiSegment:     map[string][]wireSegment{},
		multiLabel:       map[string][]string{},
	}
}

func (pb *PortBindings) SetSinglePaced(name string, pw *PacedWire) { pb.singlePaced[name] = pw }

// SetSinglePacedRule binds a single paced output with its per-edge send rule,
// that edge's own travel-time (arc length / sim latency), its straight-segment
// endpoints (so the bead's position stream evaluates the exact drawn segment), and
// the TS edge id (label) so the node's EmitGeometry closure can stream the segment.
func (pb *PortBindings) SetSinglePacedRule(name string, pw *PacedWire, rule SendRule, arcLength, simLatencyMs float64, seg wireSegment, label string) {
	pb.singlePaced[name] = pw
	pb.singleRule[name] = rule
	pb.singleArc[name] = arcLength
	pb.singleLatency[name] = simLatencyMs
	pb.singleSegment[name] = seg
	pb.singleLabel[name] = label
}

// AppendMultiPacedWithHandle is like AppendMultiPaced but records the exact
// source handle (e.g. "ToNext0"), the per-edge send rule, that edge's own
// travel-time (arc length / sim latency), its straight-segment endpoints, and
// the TS edge id (label) so the node's EmitGeometry closure can stream the segment.
func (pb *PortBindings) AppendMultiPacedWithHandle(name, handle string, pw *PacedWire, rule SendRule, arcLength, simLatencyMs float64, seg wireSegment, label string) {
	pb.multiPaced[name] = append(pb.multiPaced[name], pw)
	pb.multiPacedHandle[name] = append(pb.multiPacedHandle[name], handle)
	pb.multiRule[name] = append(pb.multiRule[name], rule)
	pb.multiArc[name] = append(pb.multiArc[name], arcLength)
	pb.multiLatency[name] = append(pb.multiLatency[name], simLatencyMs)
	pb.multiSegment[name] = append(pb.multiSegment[name], seg)
	pb.multiLabel[name] = append(pb.multiLabel[name], label)
}

func (pb *PortBindings) In(name string) <-chan int {
	ch := pb.single[name]
	if ch == nil {
		ch = make(chan int, 1)
	}
	return ch
}

func (pb *PortBindings) Out(name string) chan<- int {
	ch := pb.single[name]
	if ch == nil {
		ch = make(chan int, 1)
	}
	return ch
}

func (pb *PortBindings) OutSlice(name string) []chan<- int {
	chs := pb.multi[name]
	result := make([]chan<- int, len(chs))
	for i, c := range chs {
		result[i] = c
	}
	return result
}

var (
	tInPtr              = reflect.TypeFor[*In]()
	tOutPtr             = reflect.TypeFor[*Out]()
	tOutMulti           = reflect.TypeFor[OutMulti]()
	tFireFunc           = reflect.TypeFor[func()]()
	tEmitBeadsFunc      = reflect.TypeFor[func(working, backup []int)]()
	tEmitHeldFunc       = reflect.TypeFor[func(held int)]()
	tEmitInputBeadsFunc = reflect.TypeFor[func(left, right int)]()
	tRefillSlideFunc    = reflect.TypeFor[func(beads []int)]()
	tNowFunc            = reflect.TypeFor[func() time.Duration]()
)

// lowerFirst returns s with its first byte lowercased.
// Used for wire:"data.state" key derivation (field Held → key "held").
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// reflectStateKeys returns the data.state map keys required by sample's
// wire:"data.state" struct tags.
func reflectStateKeys(sample any) []string {
	t := reflect.TypeOf(sample).Elem()
	var keys []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Tag.Get("wire") == "data.state" {
			keys = append(keys, lowerFirst(f.Name))
		}
	}
	return keys
}

// reflectPorts walks the exported fields of the struct pointed to by sample
// and returns a PortSpec for each channel field that carries int.
// Chan-of-chan fields and non-channel fields are silently skipped.
func reflectPorts(sample any) []PortSpec {
	t := reflect.TypeOf(sample).Elem()
	var ports []PortSpec
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		switch f.Type {
		case tInPtr:
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortIn, Required: true})
		case tOutPtr:
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortOut})
		case tOutMulti:
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortOutMulti})
		}
	}
	return ports
}

// reflectBuild wires pb into the struct pointed to by nodePtr via reflection,
// then returns it cast to Node. ctx is required when pb contains PacedWire
// bindings (paced mode); it is passed into the In/Out wrappers.
func reflectBuild(ctx context.Context, name string, data *NodeData, pb PortBindings, e kindEntry, tr *T.Trace, geom nodeGeom) (Node, error) {
	nodePtr := e.newNode()
	v := reflect.ValueOf(nodePtr).Elem()

	// Inject Fire closure if the struct has a `Fire func()` field.
	// The closure captures the node name so the node calls n.Fire()
	// with no arguments and cannot mis-name itself in the trace.
	if f := v.FieldByName("Fire"); f.IsValid() && f.CanSet() && f.Type() == tFireFunc {
		nodeName := name
		f.Set(reflect.ValueOf(func() { tr.Fire(nodeName) }))
	}

	// Inject EmitGeometry closure if the struct has an `EmitGeometry func()` field
	// (item 1). The closure captures this node's id + nodeGeom and emits the node's
	// authoritative center + per-port world positions/dirs as a node-geometry event,
	// computed with the existing port_geometry.go helpers (no duplicated math). Each
	// node's goroutine calls it once on startup, so the node owns its geometry emission.
	// sourceOuts is populated during port wiring below; the closure fires later (at
	// node startup), so it sees the completed slice.
	var sourceOuts []*Out
	if f := v.FieldByName("EmitGeometry"); f.IsValid() && f.CanSet() && f.Type() == tFireFunc {
		nodeName := name
		g := geom
		f.Set(reflect.ValueOf(func() {
			emitNodeGeometry(tr, nodeName, g)
			// Emit each outgoing edge's authoritative segment (per-goroutine, item 2).
			// sourceOuts is populated just below during port wiring; by the time this
			// closure fires the slice is complete.
			for _, o := range sourceOuts {
				if o != nil && o.EdgeLabel != "" {
					tr.Geometry(o.EdgeLabel,
						o.Start.X, o.Start.Y, o.Start.Z,
						o.End.X, o.End.Y, o.End.Z)
				}
			}
		}))
	}

	// Inject EmitNodeBeads closure if the struct has an `EmitNodeBeads
	// func(working, backup []int)` field (node 1's interior buffer). The closure
	// captures this node's id + nodeGeom and emits one node-bead event per present
	// interior bead, computing each slot position from the node center + grid gaps.
	// Same per-goroutine pattern as EmitGeometry: the node's Update calls it with the
	// LIVE working/backup contents whenever the arrays change.
	if f := v.FieldByName("EmitNodeBeads"); f.IsValid() && f.CanSet() && f.Type() == tEmitBeadsFunc {
		nodeName := name
		g := geom
		f.Set(reflect.ValueOf(func(working, backup []int) {
			emitNodeBeads(tr, nodeName, g, working, backup)
		}))
	}

	// Inject EmitHeldBead closure if the struct has an `EmitHeldBead func(held int)`
	// field (HoldNewSendOld's interior held-value bead). The closure captures this
	// node's id and emits a SINGLE centered node-bead (slot 0,0 at offset 0,0,0)
	// colored by the held value; held == -1 → present=false (empty interior).
	if f := v.FieldByName("EmitHeldBead"); f.IsValid() && f.CanSet() && f.Type() == tEmitHeldFunc {
		nodeName := name
		f.Set(reflect.ValueOf(func(held int) {
			emitHeldBead(tr, nodeName, held)
		}))
	}

	// Inject EmitInputBeads closure if the struct has an `EmitInputBeads
	// func(left, right int)` field (WindowAndGate's two-sided held-input beads).
	// The closure captures this node's id and emits the LEFT input on the left of
	// the node and the RIGHT input on the right; -1 = not held → present=false.
	if f := v.FieldByName("EmitInputBeads"); f.IsValid() && f.CanSet() && f.Type() == tEmitInputBeadsFunc {
		nodeName := name
		f.Set(reflect.ValueOf(func(left, right int) {
			emitInputBeads(tr, nodeName, left, right)
		}))
	}

	// Inject EmitRefillSlide closure if the struct has an `EmitRefillSlide
	// func(beads []int)` field AND a clock is available (loader path). The closure
	// captures this node's id + the shared clock and runs the clock-paced refill
	// slide: the OLD backup beads slide DOWN from the top row (row 0) into the
	// working row (row 1) at human (wire-bead) speed. Without a clock (test build
	// with no loader) the field stays nil and the node falls back to instant refill.
	if f := v.FieldByName("EmitRefillSlide"); f.IsValid() && f.CanSet() && f.Type() == tRefillSlideFunc && pb.clock != nil {
		nodeName := name
		clk := pb.clock
		f.Set(reflect.ValueOf(func(beads []int) {
			emitRefillSlide(ctx, tr, nodeName, clk, beads)
		}))
	}

	// Inject Now closure if the struct has a `Now func() time.Duration` field AND
	// a clock is available (loader path). The closure reads active-elapsed sim time
	// (pause-aware) off the same shared clock the PacedWires use, so a node that
	// times a coincidence window / fire dwell (WindowAndGate) freezes on pause and resumes
	// on resume instead of timing out mid-pause. Without a clock (test build with no
	// loader) the field stays nil and the node falls back to a monotonic wall-clock.
	if f := v.FieldByName("Now"); f.IsValid() && f.CanSet() && f.Type() == tNowFunc && pb.clock != nil {
		clk := pb.clock
		f.Set(reflect.ValueOf(func() time.Duration { return clk.Now() }))
	}

	// Inject WaitUntil closure if the struct has a `WaitUntil func(context.Context,
	// time.Duration) error` field AND a clock is available (loader path). The closure
	// parks on the same shared clock the PacedWires use, so poll loops freeze on
	// pause and resume on resume instead of advancing on wall-clock time. Without a
	// clock (test build with no loader) the field stays nil and the node falls back
	// to a wall-clock time.After park.
	tWaitFunc := reflect.TypeFor[func(context.Context, time.Duration) error]()
	if f := v.FieldByName("WaitUntil"); f.IsValid() && f.CanSet() && f.Type() == tWaitFunc && pb.clock != nil {
		clk := pb.clock
		f.Set(reflect.ValueOf(func(ctx context.Context, target time.Duration) error {
			return clk.WaitUntil(ctx, target)
		}))
	}

	// Wire port fields with traced wrappers.
	ports := reflectPorts(nodePtr)
	for _, port := range ports {
		f := v.FieldByName(port.Name)
		if !f.IsValid() || !f.CanSet() {
			continue
		}
		switch port.Dir {
		case PortIn:
			if pw := pb.singlePaced[port.Name]; pw != nil {
				f.Set(reflect.ValueOf(NewInPaced(pw, ctx, name, port.Name, tr)))
			} else {
				ch := pb.In(port.Name)
				f.Set(reflect.ValueOf(&In{ch: ch, node: name, port: port.Name, trace: tr}))
			}
		case PortOut:
			if pw := pb.singlePaced[port.Name]; pw != nil {
				o := NewOutPaced(pw, ctx, name, port.Name, tr, pb.singleRule[port.Name], pb.singleArc[port.Name], pb.singleLatency[port.Name], pb.singleSegment[port.Name], pb.singleLabel[port.Name])
				sourceOuts = append(sourceOuts, o)
				if pb.outSink != nil {
					pb.outSink[name+"."+port.Name] = o
				}
				f.Set(reflect.ValueOf(o))
			} else {
				ch := pb.Out(port.Name)
				f.Set(reflect.ValueOf(&Out{ch: ch, node: name, port: port.Name, trace: tr}))
			}
		case PortOutMulti:
			if pws := pb.multiPaced[port.Name]; len(pws) > 0 {
				handles := pb.multiPacedHandle[port.Name]
				rules := pb.multiRule[port.Name]
				arcs := pb.multiArc[port.Name]
				lats := pb.multiLatency[port.Name]
				segs := pb.multiSegment[port.Name]
				labels := pb.multiLabel[port.Name]
				outs := make(OutMulti, len(pws))
				for i, pw := range pws {
					handle := port.Name
					if i < len(handles) {
						handle = handles[i]
					}
					var rule SendRule
					if i < len(rules) {
						rule = rules[i]
					}
					var arc, lat float64
					if i < len(arcs) {
						arc = arcs[i]
					}
					if i < len(lats) {
						lat = lats[i]
					}
					var seg wireSegment
					if i < len(segs) {
						seg = segs[i]
					}
					var lbl string
					if i < len(labels) {
						lbl = labels[i]
					}
					outs[i] = NewOutPaced(pw, ctx, name, handle, tr, rule, arc, lat, seg, lbl)
					sourceOuts = append(sourceOuts, outs[i])
					if pb.outSink != nil {
						pb.outSink[name+"."+handle] = outs[i]
					}
				}
				f.Set(reflect.ValueOf(outs))
			} else {
				chs := pb.OutSlice(port.Name)
				outs := make(OutMulti, len(chs))
				for i, c := range chs {
					outs[i] = &Out{ch: c, node: name, port: port.Name, trace: tr}
				}
				f.Set(reflect.ValueOf(outs))
			}
		}
	}

	// Tag-driven data population: wire:"data.<key>" or wire:"data.state".
	t := reflect.TypeOf(nodePtr).Elem()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("wire")
		if tag == "" {
			continue
		}
		if data == nil {
			continue
		}
		fv := v.Field(i)
		if !fv.CanSet() {
			continue
		}
		const dataPrefix = "data."
		const stateTag = "data.state"
		if tag == stateTag {
			// key is field name with first letter lowercased
			key := lowerFirst(f.Name)
			if data.State == nil {
				return nil, fmt.Errorf("reflectBuild: node %q (kind %q): wire:\"data.state\" field %s requires data.state[%q] in topology JSON", name, reflect.TypeOf(nodePtr).Elem().Name(), f.Name, key)
			}
			val, ok := data.State[key]
			if !ok {
				return nil, fmt.Errorf("reflectBuild: node %q (kind %q): wire:\"data.state\" field %s requires data.state[%q] in topology JSON", name, reflect.TypeOf(nodePtr).Elem().Name(), f.Name, key)
			}
			fv.Set(reflect.ValueOf(val))
		} else if len(tag) > len(dataPrefix) && tag[:len(dataPrefix)] == dataPrefix {
			key := tag[len(dataPrefix):]
			if len(key) == 0 {
				continue
			}
			exportedKey := strings.ToUpper(key[:1]) + key[1:]
			src := reflect.ValueOf(data).Elem().FieldByName(exportedKey)
			if !src.IsValid() || src.Type() != fv.Type() {
				continue
			}
			if src.Kind() == reflect.Slice {
				if src.IsNil() {
					continue
				}
				cp := reflect.MakeSlice(src.Type(), src.Len(), src.Len())
				reflect.Copy(cp, src)
				fv.Set(cp)
			} else {
				fv.Set(src)
			}
		}
	}

	node, ok := nodePtr.(Node)
	if !ok {
		return nil, fmt.Errorf("reflectBuild: %T does not implement Node", nodePtr)
	}
	return node, nil
}

// verticalRingNormal and flatRingNormal are the two great-circle ring normals
// streamed on every node-geometry event so TS never hardcodes ring orientation.
// vertical: ring stands upright (normal points along +Z world axis).
// flat: ring lies flat (normal points along +Y world axis, Three y-up convention).
const (
	verticalRingNormalX, verticalRingNormalY, verticalRingNormalZ = 0.0, 0.0, 1.0
	flatRingNormalX, flatRingNormalY, flatRingNormalZ             = 0.0, 1.0, 0.0
)

// emitNodeGeometry streams a node's authoritative center + per-port world
// positions/dirs as a node-geometry event, computed with the port_geometry.go
// helpers (no duplicated math). Called from each node's EmitGeometry closure on
// startup AND from the node's own move-handler goroutine (nodeMover) when its held
// position changes, so the renderer always draws the node body + ports from Go's stream.
func emitNodeGeometry(tr *T.Trace, nodeName string, g nodeGeom) {
	center := nodeWorldPos(g)
	ports := make([]T.PortGeom, 0, len(g.Inputs)+len(g.Outputs))
	appendPort := func(name string, isInput bool) {
		pos := portWorldPos(g, name, isInput)
		dir, _ := portDir(g, name, isInput)
		ports = append(ports, T.PortGeom{
			Name: name, IsInput: isInput,
			PX: pos.X, PY: pos.Y, PZ: pos.Z,
			DX: dir.X, DY: dir.Y, DZ: dir.Z,
		})
	}
	for _, p := range g.Inputs {
		appendPort(p.Name, true)
	}
	for _, p := range g.Outputs {
		appendPort(p.Name, false)
	}
	// sphereR streams the REACH radius (max distance to a surface child) so the TS
	// SphereRing sizes correctly without recomputing geometry. Childless nodes
	// (ReachR == 0) fall back to nodeR so the value stays sane.
	sphereR := nodeR(g)
	if g.ReachR > 0 {
		sphereR = g.ReachR
	}
	tr.NodeGeometry(nodeName, center.X, center.Y, center.Z, nodeRadius(g.Kind), sphereR, ports,
		verticalRingNormalX, verticalRingNormalY, verticalRingNormalZ,
		flatRingNormalX, flatRingNormalY, flatRingNormalZ)
}

// emitNodeBeads streams node 1's interior 2x2 buffer as a 4-SLOT SNAPSHOT: one
// node-bead event per fixed slot (rows {0,1} × cols {0,1}). The event's x/y/z
// carry the NODE-LOCAL OFFSET (interiorSlotOffset, relative to the node center —
// NOT a world position); TS renders each bead as a child of the node group, so
// the node center is composed by the scene graph and the beads ride the node on
// move (no re-emit needed). backup is the top row (row 0), working is the bottom
// row (row 1); a slot is PRESENT when its row's slice is at least col+1 long,
// ABSENT (popped) otherwise. Absent slots are emitted with present=false (and
// value 0) so TS can clear them — absence can't be rendered, but an explicit empty
// slot can. Discrete positions only (beads snap to slots; no slide yet). Called from
// the node's injected EmitNodeBeads closure whenever the arrays change. g is no
// longer used for geometry (offsets are node-local) but kept for signature parity.
func emitNodeBeads(tr *T.Trace, nodeName string, g nodeGeom, working, backup []int) {
	_ = g
	const cols = 2
	emitRow := func(row int, slice []int) {
		for col := 0; col < cols; col++ {
			p := interiorSlotOffset(row, col)
			if col < len(slice) {
				tr.NodeBead(nodeName, row, col, true, slice[col], p.X, p.Y, p.Z)
			} else {
				tr.NodeBead(nodeName, row, col, false, 0, p.X, p.Y, p.Z)
			}
		}
	}
	emitRow(0, backup)  // top row = backup
	emitRow(1, working) // bottom row = working
}

// emitHeldBead streams the HoldNewSendOld node's interior as a SINGLE centered
// bead (row 0, col 0) at the node center (offset 0,0,0). The bead is PRESENT when
// held != -1 and colored by the held value (0 = white, 1 = black per the existing
// node-bead convention); held == -1 (no value seen yet) → present=false so the
// interior renders empty. Called from the node's injected EmitHeldBead closure
// only when the held value changes.
func emitHeldBead(tr *T.Trace, nodeName string, held int) {
	tr.NodeBead(nodeName, 0, 0, held != -1, held, 0, 0, 0)
}

// emitInputBeads streams a gate's two held inputs as interior beads: the LEFT
// input on the left of the node (negative x), the RIGHT input on the right
// (positive x), vertically centered. -1 = not held → present=false. Slot keys
// (0,0)=left, (0,1)=right. Offsets use interiorSlot so they sit inside the sphere.
func emitInputBeads(tr *T.Trace, nodeName string, left, right int) {
	s := interiorSlot
	tr.NodeBead(nodeName, 0, 0, left != -1, left, -s, 0, 0)
	tr.NodeBead(nodeName, 0, 1, right != -1, right, s, 0, 0)
}

// interiorSlideDurationMul scales the refill-slide duration relative to raw
// pulse speed. At 1.0 the slide runs at the base pulse speed with no extra
// multiplier — the same constant speed as the wire beads. (Knob retained so
// the slide speed can still be tuned independently if needed.)
const interiorSlideDurationMul = 1.0

// emitRefillSlide runs the clock-paced animated refill for the Input node's
// interior buffer: the OLD backup row (row 0, top) slides DOWN into the working
// row (row 1, bottom) at human speed (the same wire-bead pulse speed), so a paused
// clock freezes the slide just like every wire. beads is the OLD backup contents
// that are becoming the new working row.
//
// Geometry: each bead animates from its row-0 slot offset to its row-1 slot offset
// — a downward translation of rowPitch = row0.y − row1.y in local y. Duration at
// human speed = rowPitch / PulseSpeedWuPerMs ms. The clock loops from t=0 to t=1 in
// positionEmitIntervalMs (16ms) steps via WaitUntil (pause-aware). Each frame:
//   - row 1, every col: present, value = beads[col], offset = lerp(row0,row1,t)
//     (keyed to the DESTINATION bottom slot, sliding down from the top position).
//   - row 0, every col: present=false (the top row is empty during the slide).
//
// At t=1 the bottom beads sit exactly at their row-1 offset.
func emitRefillSlide(ctx context.Context, tr *T.Trace, nodeName string, clk Clock, beads []int) {
	if clk == nil || len(beads) == 0 {
		return
	}
	row0Y := interiorSlotOffset(0, 0).Y
	row1Y := interiorSlotOffset(1, 0).Y
	rowPitch := row0Y - row1Y // downward translation distance (local y, positive)
	// Slide runs at the base pulse speed (interiorSlideDurationMul = 1.0), the
	// same constant speed as the wire beads; the clock is still pause-aware.
	durationMs := rowPitch / PulseSpeedWuPerMs * interiorSlideDurationMul
	duration := time.Duration(durationMs * float64(time.Millisecond))
	step := time.Duration(positionEmitIntervalMs * float64(time.Millisecond))

	start := clk.Now()
	emitFrame := func(t float64) {
		for col := 0; col < len(beads); col++ {
			a := interiorSlotOffset(0, col)
			b := interiorSlotOffset(1, col)
			tr.NodeBead(nodeName, 1, col, true, beads[col],
				a.X+(b.X-a.X)*t, a.Y+(b.Y-a.Y)*t, a.Z+(b.Z-a.Z)*t)
		}
		for col := 0; col < len(beads); col++ {
			p := interiorSlotOffset(0, col)
			tr.NodeBead(nodeName, 0, col, false, 0, p.X, p.Y, p.Z)
		}
	}

	emitFrame(0) // initial frame: beads at the top, top row cleared
	for target := step; ; target += step {
		if err := clk.WaitUntil(ctx, start+target); err != nil {
			return
		}
		t := float64(clk.Now()-start) / float64(duration)
		if t >= 1 {
			emitFrame(1) // land exactly on the bottom row
			return
		}
		emitFrame(t)
	}
}

// NodeBuilder is the public-facing type consumed by the loader.
// Ports is derived lazily from reflection; Build delegates to reflectBuild.
// StateKeys lists the data.state map keys required by this kind's
// wire:"data.state" struct fields; used by validateSpec for parse-time checks.
type NodeBuilder struct {
	Ports     []PortSpec
	StateKeys []string // required keys in NodeData.State; nil means none required
	Build     func(ctx context.Context, name string, data *NodeData, pb PortBindings, tr *T.Trace, geom nodeGeom) (Node, error)
}

// Registry is the loader-facing map, built once at init from kindRegistry.
var Registry map[string]NodeBuilder

func init() {
	Registry = make(map[string]NodeBuilder, len(kindRegistry))
	for kind, e := range kindRegistry {
		sample := e.newNode()
		ports := reflectPorts(sample)
		stateKeys := reflectStateKeys(sample)
		Registry[kind] = NodeBuilder{
			Ports:     ports,
			StateKeys: stateKeys,
			Build: func(ctx context.Context, name string, data *NodeData, pb PortBindings, tr *T.Trace, geom nodeGeom) (Node, error) {
				return reflectBuild(ctx, name, data, pb, e, tr, geom)
			},
		}
	}
}
