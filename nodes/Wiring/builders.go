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
	single map[string]chan int
	multi  map[string][]chan int
	// singlePaced holds the resolved paced binding for each single In/Out port.
	// multiPaced holds the per-element bindings for each OutMulti fan-out port.
	// Consolidating the formerly-parallel per-edge maps into one struct keeps
	// every field of a binding together and impossible to index-mismatch.
	singlePaced map[string]singleBinding
	multiPaced  map[string][]multiBinding
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

// singleBinding is the resolved paced binding for one single port. For an INPUT
// port only pw is set (SetSinglePaced); an OUTPUT port also carries its per-edge
// send rule and own geometry (SetSinglePacedRule). The zero value (pw == nil)
// means "no paced binding — fall back to a dead-end chan".
type singleBinding struct {
	pw      *PacedWire
	rule    SendRule
	arc     float64
	latency float64
	seg     wireSegment
	label   string
}

// multiBinding is one fan-out element of an OutMulti port: its shared dest wire,
// the concrete source handle (e.g. "ToNext0"), per-edge send rule, and that
// edge's own travel-time / segment / TS label.
type multiBinding struct {
	pw      *PacedWire
	handle  string
	rule    SendRule
	arc     float64
	latency float64
	seg     wireSegment
	label   string
}

func newPortBindings() PortBindings {
	return PortBindings{
		single:      map[string]chan int{},
		multi:       map[string][]chan int{},
		singlePaced: map[string]singleBinding{},
		multiPaced:  map[string][]multiBinding{},
	}
}

func (pb *PortBindings) SetSinglePaced(name string, pw *PacedWire) {
	pb.singlePaced[name] = singleBinding{pw: pw}
}

// SetSinglePacedRule binds a single paced output with its per-edge send rule,
// that edge's own travel-time (arc length / sim latency), its straight-segment
// endpoints (so the bead's position stream evaluates the exact drawn segment), and
// the TS edge id (label) so the node's EmitGeometry closure can stream the segment.
func (pb *PortBindings) SetSinglePacedRule(name string, pw *PacedWire, rule SendRule, arcLength, simLatencyMs float64, seg wireSegment, label string) {
	pb.singlePaced[name] = singleBinding{pw: pw, rule: rule, arc: arcLength, latency: simLatencyMs, seg: seg, label: label}
}

// AppendMultiPacedWithHandle is like AppendMultiPaced but records the exact
// source handle (e.g. "ToNext0"), the per-edge send rule, that edge's own
// travel-time (arc length / sim latency), its straight-segment endpoints, and
// the TS edge id (label) so the node's EmitGeometry closure can stream the segment.
func (pb *PortBindings) AppendMultiPacedWithHandle(name, handle string, pw *PacedWire, rule SendRule, arcLength, simLatencyMs float64, seg wireSegment, label string) {
	pb.multiPaced[name] = append(pb.multiPaced[name], multiBinding{
		pw: pw, handle: handle, rule: rule, arc: arcLength, latency: simLatencyMs, seg: seg, label: label,
	})
}

func (pb *PortBindings) In(name string) <-chan int {
	ch := pb.single[name]
	if ch == nil {
		ch = make(chan int, 1) // chan-name-ok: local placeholder accessor; wire identity is the port `name` (map key)
	}
	return ch
}

func (pb *PortBindings) Out(name string) chan<- int {
	ch := pb.single[name]
	if ch == nil {
		ch = make(chan int, 1) // chan-name-ok: local placeholder accessor; wire identity is the port `name` (map key)
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
	tNodeStatusFunc     = reflect.TypeFor[func(torusRed bool, missedValue int)]()
	tRefillSlideFunc    = reflect.TypeFor[func(beads []int)]()
	tTickFunc           = reflect.TypeFor[func() int64]()
)

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
// Anonymous (embedded) struct fields are recursed so port fields promoted
// from an embedded struct (e.g. gatecommon.GateNode) are discovered.
func reflectPorts(sample any) []PortSpec {
	t := reflect.TypeOf(sample).Elem()
	return collectPorts(t)
}

func collectPorts(t reflect.Type) []PortSpec {
	var ports []PortSpec
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			ft := f.Type
			if ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				ports = append(ports, collectPorts(ft)...)
			}
			continue
		}
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

// injectFunc sets the named func-typed field on v to fn, but only when the field
// exists, is settable, and has exactly the expected type `want`. This is the one
// shape every closure injection in reflectBuild shares (Fire, EmitGeometry, the
// Emit* bead closures, Tick, WaitTick); structs lacking the field are left
// untouched. Returns whether the field was set.
func injectFunc(v reflect.Value, name string, want reflect.Type, fn any) bool {
	f := v.FieldByName(name)
	if !f.IsValid() || !f.CanSet() || f.Type() != want {
		return false
	}
	f.Set(reflect.ValueOf(fn))
	return true
}

// reflectBuild wires pb into the struct pointed to by nodePtr via reflection,
// then returns it cast to Node. ctx is required when pb contains PacedWire
// bindings (paced mode); it is passed into the In/Out wrappers.
func reflectBuild(ctx context.Context, name string, data *NodeData, pb PortBindings, e kindEntry, tr *T.Trace, geom nodeGeom) (Node, error) {
	nodePtr := e.newNode()
	v := reflect.ValueOf(nodePtr).Elem()

	// Inject Fire closure if the struct has a `Fire func()` field. The closure
	// captures the node name so the node calls n.Fire() with no arguments and
	// cannot mis-name itself in the trace.
	injectFunc(v, "Fire", tFireFunc, func() { tr.Fire(name) })

	// Inject EmitGeometry closure if the struct has an `EmitGeometry func()` field.
	// The closure emits the node's authoritative center + per-port world
	// positions/dirs as a node-geometry event (port_geometry.go helpers, no
	// duplicated math), then each outgoing edge's segment. Each node's goroutine
	// calls it once on startup, so the node owns its geometry emission. sourceOuts
	// is populated during port wiring below; the closure fires later (at node
	// startup), so it sees the completed slice.
	var sourceOuts []*Out
	injectFunc(v, "EmitGeometry", tFireFunc, func() {
		emitNodeGeometry(tr, name, geom)
		for _, o := range sourceOuts {
			if o != nil && o.EdgeLabel != "" {
				g := o.Geom()
				tr.Geometry(o.EdgeLabel,
					g.Start.X, g.Start.Y, g.Start.Z,
					g.End.X, g.End.Y, g.End.Z)
			}
		}
	})

	// Inject EmitNodeBeads closure if the struct has an `EmitNodeBeads
	// func(working, backup []int)` field (node 1's interior buffer). Emits one
	// node-bead event per present interior bead. The node's Update calls it with the
	// LIVE working/backup contents whenever the arrays change.
	injectFunc(v, "EmitNodeBeads", tEmitBeadsFunc, func(working, backup []int) {
		emitNodeBeads(tr, name, working, backup)
	})

	// Inject EmitHeldBead closure if the struct has an `EmitHeldBead func(held int)`
	// field (HoldNewSendOld's interior held-value bead): a SINGLE centered node-bead
	// (slot 0,0 at offset 0,0,0) colored by the held value; held == -1 →
	// present=false (empty interior).
	injectFunc(v, "EmitHeldBead", tEmitHeldFunc, func(held int) {
		emitHeldBead(tr, name, held)
	})

	// Inject EmitInputBeads closure if the struct has an `EmitInputBeads
	// func(left, right int)` field (a gate's two-sided held-input beads): LEFT input
	// on the left of the node, RIGHT on the right; -1 = not held → present=false.
	injectFunc(v, "EmitInputBeads", tEmitInputBeadsFunc, func(left, right int) {
		emitInputBeads(tr, name, left, right)
	})

	// Inject EmitNodeStatus closure if the struct has an
	// `EmitNodeStatus func(torusRed bool, missedValue int)` field (the shared
	// processing-window error reporting). The closure owns the geometry: it derives a
	// world position just OUTSIDE the node from geom and emits the node-status event.
	injectFunc(v, "EmitNodeStatus", tNodeStatusFunc, func(torusRed bool, missedValue int) {
		emitNodeStatus(tr, name, geom, torusRed, missedValue)
	})

	// The remaining injections require the loader's shared clock; a test build
	// without a loader leaves pb.clock nil and these fields stay nil (each node
	// falls back to its wall-clock / instant behavior).
	if pb.clock != nil {
		clk := pb.clock
		// EmitRefillSlide func(beads []int): the clock-paced refill slide (the OLD
		// backup beads slide DOWN from row 0 into row 1 at wire-bead speed; a paused
		// clock freezes it).
		injectFunc(v, "EmitRefillSlide", tRefillSlideFunc, func(beads []int) {
			emitRefillSlide(ctx, tr, name, clk, beads)
		})
		// Tick func() int64: current tick (pause-aware) off the shared human-speed
		// clock, so a node timing a window/dwell in ticks freezes on pause.
		injectFunc(v, "Tick", tTickFunc, func() int64 { return clk.Tick() })
		// WaitTick func(context.Context, int64) error: park on the shared clock so
		// poll loops freeze on pause instead of advancing on wall-clock time.
		tWaitFunc := reflect.TypeFor[func(context.Context, int64) error]()
		injectFunc(v, "WaitTick", tWaitFunc, func(ctx context.Context, k int64) error {
			return clk.WaitTick(ctx, k)
		})
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
			if b := pb.singlePaced[port.Name]; b.pw != nil {
				f.Set(reflect.ValueOf(NewInPaced(b.pw, ctx, name, port.Name, tr)))
			} else {
				ch := pb.In(port.Name)
				f.Set(reflect.ValueOf(&In{ch: ch, node: name, port: port.Name, trace: tr}))
			}
		case PortOut:
			if b := pb.singlePaced[port.Name]; b.pw != nil {
				o := NewOutPaced(b.pw, ctx, name, port.Name, tr, b.rule, b.arc, b.latency, b.seg, b.label)
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
			if bs := pb.multiPaced[port.Name]; len(bs) > 0 {
				outs := make(OutMulti, len(bs))
				for i, b := range bs {
					outs[i] = NewOutPaced(b.pw, ctx, name, b.handle, tr, b.rule, b.arc, b.latency, b.seg, b.label)
					sourceOuts = append(sourceOuts, outs[i])
					if pb.outSink != nil {
						pb.outSink[name+"."+b.handle] = outs[i]
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
			// key is field name with first letter lowercased.
			// validateSpec (Check 5) already verified data.State != nil and the key
			// is present before LoadTopology calls reflectBuild, so no error check needed.
			key := lowerFirst(f.Name)
			val := data.State[key]
			fv.Set(reflect.ValueOf(val))
		} else if len(tag) > len(dataPrefix) && tag[:len(dataPrefix)] == dataPrefix {
			key := tag[len(dataPrefix):]
			if len(key) == 0 {
				continue
			}
			exportedKey := exportedFieldName(key)
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
	emitNodeGeometryWith(tr, nodeName, g, func(name string, isInput bool) (vec3, vec3) {
		pos := portWorldPos(g, name, isInput)
		dir, _ := portDir(g, name, isInput)
		return pos, dir
	})
}

// emitNodeGeometryAimed is like emitNodeGeometry but uses portDirAimed for
// every port, so registered ports point dynamically toward their connected
// node's current center. Non-registered ports fall back to portDir.
func emitNodeGeometryAimed(tr *T.Trace, nodeName string, g nodeGeom, registry AimedPortRegistry, centerOf func(string) (vec3, bool)) {
	emitNodeGeometryWith(tr, nodeName, g, func(name string, isInput bool) (vec3, vec3) {
		pos := portWorldPosAimed(g, name, isInput, nodeName, registry, centerOf)
		dir, _ := portDirAimed(g, name, isInput, nodeName, registry, centerOf)
		return pos, dir
	})
}

// emitNodeGeometryWith streams a node-geometry event for g, deriving each port's
// world position + direction from portPosDir. The two public variants differ
// ONLY in that function (static portDir vs aimed portDirAimed); everything else —
// center, the input-then-output port order, the reach-radius fallback, and the
// ring normals — is identical and lives here.
// effectiveRadius returns the node's REACH radius (max distance to a surface child),
// falling back to nodeR for childless nodes (ReachR == 0) so the value stays sane.
// Shared by emitNodeGeometryWith (sphereR) and emitNodeStatus (missed-bead offset).
func effectiveRadius(g nodeGeom) float64 {
	if g.ReachR > 0 {
		return g.ReachR
	}
	return nodeR(g)
}

func emitNodeGeometryWith(tr *T.Trace, nodeName string, g nodeGeom, portPosDir func(name string, isInput bool) (pos, dir vec3)) {
	center := nodeWorldPos(g)
	ports := make([]T.PortGeom, 0, len(g.Inputs)+len(g.Outputs))
	appendPort := func(name string, isInput bool) {
		pos, dir := portPosDir(name, isInput)
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
	sphereR := effectiveRadius(g)
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
// the node's injected EmitNodeBeads closure whenever the arrays change. Offsets are
// node-local, so no node geometry is needed.
func emitNodeBeads(tr *T.Trace, nodeName string, working, backup []int) {
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

// nodeStatusMissedOffsetMul scales the node's reach radius to place the "missed"
// bead marker JUST OUTSIDE the node body, so the renderer can show the discarded
// different-color bead clearly separated from the node sphere.
const nodeStatusMissedOffsetMul = 1.5

// emitNodeStatus streams a node-status event: torusRed marks the node's torus RED on a
// processing error (a different-color bead missed mid-processing), missedValue is that
// discarded bead's value, and the position is computed JUST OUTSIDE the node center
// (world units) so the renderer can show the missed bead. torusRed=false reverts to
// normal (missedValue/pos are unused by the renderer then). Go owns the geometry here —
// the node body computes no positions, matching emitHeldBead/emitNodeBeads.
func emitNodeStatus(tr *T.Trace, nodeName string, g nodeGeom, torusRed bool, missedValue int) {
	center := nodeWorldPos(g)
	off := effectiveRadius(g) * nodeStatusMissedOffsetMul
	tr.NodeStatus(nodeName, torusRed, missedValue, center.X+off, center.Y, center.Z)
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

// emitRefillSlide runs the clock-paced animated refill for the Input node's
// interior buffer: the OLD backup row (row 0, top) slides DOWN into the working
// row (row 1, bottom) at human speed (the same wire-bead pulse speed), so a paused
// clock freezes the slide just like every wire. beads is the OLD backup contents
// that are becoming the new working row.
//
// Geometry: each bead animates from its row-0 slot offset to its row-1 slot offset
// — a downward translation of rowPitch = row0.y − row1.y in local y. Duration at
// human speed = rowPitch / PulseSpeedWuPerTick ticks. The clock loops from t=0 to
// t=1 one tick per step via WaitTick (pause-aware). Each frame:
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
	// Slide runs at the base pulse speed — the same constant speed as the wire
	// beads; the clock is still pause-aware. Duration is a tick count.
	durationTicks := rowPitch / PulseSpeedWuPerTick

	start := clk.Tick()
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
	for target := int64(1); ; target++ {
		if err := clk.WaitTick(ctx, start+target); err != nil {
			return
		}
		t := float64(clk.Tick()-start) / durationTicks
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
