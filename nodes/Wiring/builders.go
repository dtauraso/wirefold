// builders.go — reflection-driven port-manifest and node construction.
//
// Adding a kind: register one entry in kindRegistry. The struct fields
// determine the port manifest automatically:
//   - *Wiring.In       → PortIn
//   - *Wiring.Out      → PortOut
//   - Wiring.Broadcast  → PortBroadcast
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
	PortBroadcast         // slice output ([]chan<- int)
)

// PortSpec describes one port on a node kind.
type PortSpec struct {
	Name string
	Dir  PortDir
}

// PortBindings holds resolved PacedWires keyed by port name.
// For PortBroadcast ports, use AppendBroadcastWithHandle.
// A port name with no paced binding resolves to a dead-end chan wrapper
// (deadEndIn/deadEndOut/deadEndOutSlice) that neither sends nor receives.
type PortBindings struct {
	// singlePaced holds the resolved paced binding for each single In/Out port.
	// broadcastPaced holds the per-element bindings for each Broadcast fan-out port.
	// Consolidating the formerly-parallel per-edge maps into one struct keeps
	// every field of a binding together and impossible to index-mismatch.
	singlePaced map[string]singleBinding
	broadcastPaced  map[string][]broadcastBinding
	// outSink, when non-nil, collects every paced *Out built for this node keyed
	// by "node.handle" so the loader can index Outs by edge for node-move
	// travel-time updates. Render/run paths leave it nil.
	outSink map[string]*Out
	// clock is the loader's ORIGIN clock, read only by reflectBuild's injectClosures
	// (never by a port): it seeds a node's bare `Clock Wiring.Clock` field and the
	// `Tick func() int64` closure at construction. Per per-goroutine-clock.md API
	// demolition, ports/wires no longer hold or hand out a clock at all — a node's
	// own goroutine does exactly one Copy() of its Clock field at its own start.
	// Test builds without a loader leave this nil, and such nodes' Clock/Tick
	// fields simply stay unset (their own zero-value fallback, e.g. gatecommon's
	// defaultTick/defaultSleep).
	clock Clock
	// speedSinks accumulates the SEND end of every speed channel created for
	// this node during construction (one per clock-owning goroutine the node
	// spawns — see injectSpeedChans). It points at the loader's build-wide slice
	// (buildCtx.speedSinks) so every node's channels land in the one list
	// LoadTopology hands back to stdin_reader. nil in test builds with no
	// loader — injectSpeedChans then skips channel creation entirely (a node
	// with no speed channel just never hears a speed change, same as it never
	// had a clock to speed up before this plan).
	speedSinks *[]chan float64
	// md, when non-nil, gives injectClosures's interior-bead Emit* closures access to
	// this node's OWN dedicated interior fd (md.interiorOuts, keyed by node id) and the
	// injected interior-frame builder (md.buildInteriorFrame) — see
	// MoveDispatch.SetNodeStreams / memory/feedback_no_single_writer_bridge.md. Set once
	// per node at construction (loader.go's buildNodes: pb.md = b.md); nil in test builds
	// with no loader, in which case the Emit* closures just skip the dedicated-stream
	// write (tr.NodeBead alone, unchanged).
	md *MoveDispatch
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

// broadcastBinding is one fan-out element of an Broadcast port: its shared dest wire,
// the concrete source handle (e.g. "ToNext0"), per-edge send rule, and that
// edge's own travel-time / segment / TS label.
type broadcastBinding struct {
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
		singlePaced: map[string]singleBinding{},
		broadcastPaced:  map[string][]broadcastBinding{},
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

// AppendBroadcastWithHandle is like AppendMultiPaced but records the exact
// source handle (e.g. "ToNext0"), the per-edge send rule, that edge's own
// travel-time (arc length / sim latency), its straight-segment endpoints, and
// the TS edge id (label) so the node's EmitGeometry closure can stream the segment.
func (pb *PortBindings) AppendBroadcastWithHandle(name, handle string, pw *PacedWire, rule SendRule, arcLength, simLatencyMs float64, seg wireSegment, label string) {
	pb.broadcastPaced[name] = append(pb.broadcastPaced[name], broadcastBinding{
		pw: pw, handle: handle, rule: rule, arc: arcLength, latency: simLatencyMs, seg: seg, label: label,
	})
}

// deadEndIn returns a fresh unbuffered-in-effect receive-only chan for a port
// name with no paced binding. It is never fed a value; it exists only so an
// unwired In field has a non-nil channel to hold.
func (pb *PortBindings) deadEndIn(name string) <-chan int {
	return make(chan int, 1) // chan-name-ok: dead-end placeholder; wire identity is the port `name` (map key)
}

// deadEndOut is deadEndIn's send-only counterpart for an unwired Out field.
func (pb *PortBindings) deadEndOut(name string) chan<- int {
	return make(chan int, 1) // chan-name-ok: dead-end placeholder; wire identity is the port `name` (map key)
}

// deadEndOutSlice is deadEndOut's counterpart for an unwired Broadcast field:
// there is no fan-out recorded for this port name, so it resolves to an empty
// slice of dead-end sends.
func (pb *PortBindings) deadEndOutSlice(name string) []chan<- int {
	return nil
}

var (
	tInPtr              = reflect.TypeFor[*In]()
	tOutPtr             = reflect.TypeFor[*Out]()
	tBroadcast           = reflect.TypeFor[Broadcast]()
	tFireFunc           = reflect.TypeFor[func()]()
	tEmitBeadsFunc      = reflect.TypeFor[func(working, backup []int)]()
	tEmitHeldFunc       = reflect.TypeFor[func(held int)]()
	tEmitInputBeadsFunc = reflect.TypeFor[func(left, right int)]()
	tRefillSlideFunc    = reflect.TypeFor[func(clk Clock, speedCh <-chan float64, beads []int)]()
	tTickFunc           = reflect.TypeFor[func() int64]()
)

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
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortIn})
		case tOutPtr:
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortOut})
		case tBroadcast:
			ports = append(ports, PortSpec{Name: f.Name, Dir: PortBroadcast})
		}
	}
	return ports
}

// injectFunc sets the named func-typed field on v to fn, but only when the field
// exists, is settable, and has exactly the expected type `want`. This is the one
// shape every closure injection in reflectBuild shares (Fire, EmitGeometry, the
// Emit* bead closures, Tick); structs lacking the field are left
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
//
// The three concerns are split into named helpers, each called in the same
// order the original monolithic function performed them (behavior unchanged):
//   - injectClosures: Fire/EmitGeometry/EmitNodeBeads/EmitHeldBead/EmitInputBeads/
//     EmitRefillSlide/Tick closure injection.
//   - wirePorts: tag-driven (struct-shape-driven) port wiring — In/Out/Broadcast
//     fields set from pb's resolved bindings.
//   - populateData: wire:"data.<key>" / wire:"data.state" tag-driven data
//     population.
func reflectBuild(ctx context.Context, name string, data *NodeData, pb PortBindings, e kindEntry, tr *T.Trace, geom nodeGeom, partnerCenter partnerCenterFn) (Node, error) {
	nodePtr := e.newNode()
	v := reflect.ValueOf(nodePtr).Elem()

	// getStream is THIS node's one shared interior-stream getter (lazy-cache-once — see
	// its doc comment): every closure/port that records a Fire/Recv/Send/NodeBead event
	// for this node calls the SAME func, so they all land on the SAME *interiorStream
	// instance (and share its cached bead-slot snapshot).
	getStream := newInteriorStreamGetter(name, pb)

	var sourceOuts []*Out
	injectClosures(ctx, v, name, pb, tr, geom, &sourceOuts, partnerCenter, getStream)
	wirePorts(ctx, v, nodePtr, name, pb, tr, &sourceOuts, getStream)
	populateData(v, nodePtr, data)

	node, ok := nodePtr.(Node)
	if !ok {
		return nil, fmt.Errorf("reflectBuild: %T does not implement Node", nodePtr)
	}
	return node, nil
}

// injectClosures injects every func-typed closure field reflectBuild supports
// (Fire, EmitGeometry, the Emit* interior-bead closures, and — when a shared
// clock is present — EmitRefillSlide/Tick). Each injection is a no-op
// when the struct lacks the matching field (injectFunc's contract). Returns the
// sourceOuts slice that EmitGeometry's closure reads for per-edge segments;
// wirePorts appends to it as it resolves each Out/Broadcast binding, and the
// closure (which fires later, at node startup) sees the completed slice.
// sourceOuts is owned by the caller (reflectBuild) and shared with wirePorts,
// which appends to it as it resolves each Out/Broadcast binding; the EmitGeometry
// closure reads through the same pointer so it sees the completed slice.
func injectClosures(ctx context.Context, v reflect.Value, name string, pb PortBindings, tr *T.Trace, geom nodeGeom, sourceOuts *[]*Out, partnerCenter partnerCenterFn, getStream func() *interiorStream) {
	// Inject Fire closure if the struct has a `Fire func()` field. The closure
	// captures the node name so the node calls n.Fire() with no arguments and
	// cannot mis-name itself in the trace. The RowEvent flush below lands this Fire
	// on THIS node's OWN interior-stream frame (KindFire is fully decentralized — it
	// never rides the VIEW stream's fallback bucket) — this node's own Update goroutine
	// is the sole owner of when it fires, so it resolves its own NodeRow at the call
	// site (owner_events.go) via the shared interiorStream (getStream), never a shared
	// accumulator. writeEvents is nil-safe (no-op) when this node has no dedicated
	// interior fd (test builds without a loader).
	injectFunc(v, "Fire", tFireFunc, func() {
		if s := getStream(); s != nil {
			s.writeEvents([]RowEvent{{
				Kind: T.KindFire, NodeRow: s.nodeRow,
				PortRow: -1, TargetRow: -1, TargetPortRow: -1, EdgeRow: -1,
			}})
		}
	})

	// EmitGeometry is deliberately left UNINJECTED (the `EmitGeometry func()` field on
	// node structs stays nil, and Wiring.TryEmit(n.EmitGeometry) no-ops at node startup —
	// see node.go's TryEmit). It used to be the node's own Update-loop startup emit of
	// its node-geometry event AND each outgoing edge's segment (tr.NodeGeometry/
	// tr.Geometry), duplicating the identical values nodeMover/edgeMover's own
	// goroutine-start emit now produces (node_mover.go: nodeMover.run/edgeMover.run each
	// call their own emitGeometry/recomputeGeometry once before their loop). This node
	// struct field and sourceOuts (still populated by wirePorts, now otherwise unread by
	// this function) are kept only because deleting the struct fields themselves would
	// be a wider, unrelated churn across every node kind package; the field being
	// present-but-nil is equivalent to it not existing for every live code path
	// (geom/partnerCenter/sourceOuts are otherwise still referenced below/by callers,
	// so their params stay; nothing left in THIS function reads them for geometry).

	// Inject EmitNodeBeads closure if the struct has an `EmitNodeBeads
	// func(working, backup []int)` field (node 1's interior buffer). Emits one
	// node-bead event per present interior bead. The node's Update calls it with the
	// LIVE working/backup contents whenever the arrays change.
	injectFunc(v, "EmitNodeBeads", tEmitBeadsFunc, func(working, backup []int) {
		emitNodeBeads(tr, name, working, backup, getStream())
	})

	// Inject EmitHeldBead closure if the struct has an `EmitHeldBead func(held int)`
	// field (HoldNewSendOld's interior held-value bead): a SINGLE centered node-bead
	// (slot 0,0 at offset 0,0,0) colored by the held value; held == -1 →
	// present=false (empty interior).
	injectFunc(v, "EmitHeldBead", tEmitHeldFunc, func(held int) {
		emitHeldBead(tr, name, held, getStream())
	})

	// Inject EmitInputBeads closure if the struct has an `EmitInputBeads
	// func(left, right int)` field (a gate's two-sided held-input beads): LEFT input
	// on the left of the node, RIGHT on the right; -1 = not held → present=false.
	injectFunc(v, "EmitInputBeads", tEmitInputBeadsFunc, func(left, right int) {
		emitInputBeads(tr, name, left, right, getStream())
	})

	// EmitRefillSlide func(clk Clock, speedCh <-chan float64, beads []int): the
	// clock-paced refill slide (the OLD backup beads slide DOWN from row 0 into row
	// 1 at wire-bead speed; a paused clock freezes it). The clock AND speed channel
	// are parameters the CALLER supplies at invocation time (its own
	// already-Copy()'d clock and its own SpeedCh — see input.Node.Update, which
	// calls n.EmitRefillSlide(clk, n.SpeedCh, beads) with the same copy/channel its
	// own loop paces on) rather than values captured here from pb.clock: capturing
	// the loader's origin in this closure would hand every future call a read into
	// a clock this goroutine never Copy()'d for itself (per-goroutine-clock.md
	// flagged this as a residual — this closure no longer needs pb.clock at all, so
	// it is unconditional). The speed channel must be threaded through too: this
	// slide runs its OWN blocking SleepCycle loop separate from the caller's main
	// loop, so it must poll ApplySpeedNonBlocking itself each cycle or a speed
	// change sent mid-slide sits unapplied until the slide finishes.
	injectFunc(v, "EmitRefillSlide", tRefillSlideFunc, func(clk Clock, speedCh <-chan float64, beads []int) {
		emitRefillSlide(ctx, tr, name, clk, speedCh, beads)
	})

	// The remaining injections seed a node's OWN clock storage from the loader's
	// origin, once, at construction — a test build without a loader leaves pb.clock
	// nil and these fields stay unset (each node falls back to its own wall-clock/
	// no-loader behavior, e.g. gatecommon's defaultTick/defaultSleep).
	if pb.clock != nil {
		clk := pb.clock
		// Tick func() int64: current tick (pause-aware) off the origin clock. Used
		// only as a chan-mode/no-Out-yet fallback for "now" by gatecommon.GateNode;
		// the paced path takes its own Copy() of the Clock field below instead.
		injectFunc(v, "Tick", tTickFunc, func() int64 { return clk.Tick() })
		// Clock Wiring.Clock: the node's OWN clock storage, seeded from the loader's
		// origin so the node's goroutine can Copy() it exactly once at its own
		// start — this field is
		// never read repeatedly by anything outside the node's own goroutine, and it
		// is never reached through a port. Only fields typed exactly Wiring.Clock
		// (e.g. input.Node.Clock, gatecommon.GateNode.Clock) receive this; other
		// nodes are unaffected.
		tClockType := reflect.TypeFor[Clock]()
		injectFunc(v, "Clock", tClockType, clk)
	}

	injectSpeedChans(v, pb)
}

// speedChanFieldNames lists every field name a node kind may declare to receive
// a speed-delivery channel. Most kinds (input/hold/holdnewsendold/pacer,
// gatecommon.GateNode) run exactly one clock-owning goroutine and declare only
// SpeedCh. Pulse/HoldFlip split into a main loop plus one-or-two
// gatecommon.DriveHeld goroutines (one per driven Out) — each is an
// INDEPENDENT clock copy, so each needs its OWN channel: sharing one channel
// across two goroutines would silently starve whichever one loses a given
// receive, which is exactly the "no goroutine left behind" failure item 3 of
// per-goroutine-clock.md guards against. DriveSpeedCh/Out1SpeedCh/Out2SpeedCh
// are those extra per-drive-goroutine channels; a struct that doesn't declare
// a given name simply doesn't get one (injectSpeedChans is a no-op per name
// when the field is absent, same contract as injectFunc).
var speedChanFieldNames = []string{"SpeedCh", "DriveSpeedCh", "Out1SpeedCh", "Out2SpeedCh"}

// injectSpeedChans creates one fresh buffered-1 speed channel per field name in
// speedChanFieldNames that the struct pointed to by v actually declares (typed
// exactly `<-chan float64`), injects its RECEIVE end into that field, and
// appends its SEND end to *pb.speedSinks — the loader's build-wide accumulator
// of every goroutine's speed channel, broadcast to on a speed change. A no-op
// when pb.speedSinks is nil (test builds with no loader): such a node's
// goroutines simply have no speed channel to poll, exactly like they had no
// shared clock to receive a speed change on before this plan either.
func injectSpeedChans(v reflect.Value, pb PortBindings) {
	if pb.speedSinks == nil {
		return
	}
	tSpeedChan := reflect.TypeFor[<-chan float64]()
	for _, fname := range speedChanFieldNames {
		f := v.FieldByName(fname)
		if !f.IsValid() || !f.CanSet() || f.Type() != tSpeedChan {
			continue
		}
		speedCh := make(chan float64, 1)
		f.Set(reflect.ValueOf((<-chan float64)(speedCh)))
		*pb.speedSinks = append(*pb.speedSinks, speedCh)
	}
}

// bufInteriorSlotsPerNode is a local copy of Buffer.BufInteriorSlotsPerNode's value
// (4 — the fixed interior-bead slot count per node), kept here rather than importing
// Buffer (see boolU8's doc comment for the existing precedent of this package
// duplicating a small Buffer constant to stay Buffer-independent). Used only to size
// newInteriorStreamGetter's initial all-absent bead-slot cache.
const bufInteriorSlotsPerNode = 4

// newInteriorStreamGetter returns a func() *interiorStream that lazily builds
// (exactly once) and thereafter always returns THIS node's one dedicated
// interior-stream instance from pb.md.interiorOuts — so every closure/port
// belonging to this node (EmitNodeBeads/EmitHeldBead/EmitInputBeads via
// injectClosures, and Fire/Recv/Send via the Fire closure and In/Out — see
// wirePorts) shares the SAME instance, and therefore the same cached last-known
// bead-slot snapshot (interiorStream.lastPresent's doc comment) a Fire/Recv/Send
// event needs to flush a valid frame between bead-state changes.
//
// Lazy because pb.md.interiorOuts is only populated by main.go AFTER LoadTopology
// returns (i.e. after this node's own construction runs) — see the prior
// buildInteriorStream doc comment this replaces. The returned func's first REAL
// call is always made from this node's OWN Update goroutine (after node-goroutine
// launch, by which point interiorOuts is fully populated and never mutated again),
// so no lock is needed: exactly one goroutine ever calls this closure, matching
// every other single-writer-per-goroutine field in this package.
func newInteriorStreamGetter(name string, pb PortBindings) func() *interiorStream {
	var built bool
	var stream *interiorStream
	return func() *interiorStream {
		if built {
			return stream
		}
		built = true
		if pb.md == nil || pb.md.interiorOuts == nil {
			return nil
		}
		out, ok := pb.md.interiorOuts[name]
		if !ok || out == nil || pb.md.buildInteriorFrame == nil {
			return nil
		}
		nodeRow := int32(-1)
		if r, ok := pb.md.NodeRowFor(name); ok {
			nodeRow = r
		}
		absent := make([]uint8, bufInteriorSlotsPerNode)
		zeroI := make([]int32, bufInteriorSlotsPerNode)
		zeroF := make([]float32, bufInteriorSlotsPerNode)
		stream = &interiorStream{
			out: out, buildFrame: pb.md.buildInteriorFrame, nodeRow: nodeRow,
			lastPresent: absent, lastValue: zeroI,
			lastOx: zeroF, lastOy: append([]float32{}, zeroF...), lastOz: append([]float32{}, zeroF...),
		}
		return stream
	}
}

// wirePorts wires every port field (In/Out/Broadcast) discovered by reflectPorts
// with traced wrappers, resolving each from pb's paced bindings when present and
// falling back to a dead-end chan/slice otherwise. sourceOuts accumulates every
// paced Out built (for EmitGeometry's closure, injected by injectClosures) and
// pb.outSink (when non-nil) is populated so the loader can index Outs by edge.
// getStream is this node's shared interior-stream getter (newInteriorStreamGetter),
// threaded through so Recv/Send can flush their own RowEvent onto the same frame
// Fire/EmitNodeBeads use.
func wirePorts(ctx context.Context, v reflect.Value, nodePtr any, name string, pb PortBindings, tr *T.Trace, sourceOuts *[]*Out, getStream func() *interiorStream) {
	ports := reflectPorts(nodePtr)
	for _, port := range ports {
		f := v.FieldByName(port.Name)
		if !f.IsValid() || !f.CanSet() {
			continue
		}
		switch port.Dir {
		case PortIn:
			wireInPort(f, port.Name, ctx, name, pb, tr, getStream)
		case PortOut:
			wireOutPort(f, port.Name, ctx, name, pb, tr, sourceOuts, getStream)
		case PortBroadcast:
			wireBroadcastPort(f, port.Name, ctx, name, pb, tr, sourceOuts, getStream)
		}
	}
}

// wireInPort resolves a single PortIn field: a paced binding (NewInPaced) when
// pb has one for this port name, otherwise a dead-end chan wrapper.
//
// Neither branch carries a clock (per-goroutine-clock.md API demolition item 1: port
// accessors are gone) — an unwired In just polls a dead-end channel that never
// delivers, staying inert by precondition-gating (validate.go) exactly like a wired
// node whose peer never sends; its owning node paces off its OWN Clock field/Copy(),
// never off this port.
//
// A paced In's portRow (its own buffer PORT-ROW, isInput=true) is resolved once here
// from pb.md's row table (populated at MoveDispatch construction, before any node's
// own construction — see PortRowFor's doc comment), and stream is this node's shared
// interior-stream getter: both are read later by In.PollRecv, on this node's own
// Update goroutine, to flush a KindRecv RowEvent (owner_events.go).
func wireInPort(f reflect.Value, portName string, ctx context.Context, name string, pb PortBindings, tr *T.Trace, getStream func() *interiorStream) {
	if b := pb.singlePaced[portName]; b.pw != nil {
		in := NewInPaced(b.pw, ctx, name, portName, tr)
		in.stream = getStream
		in.portRow = -1
		if pb.md != nil {
			if r, ok := pb.md.PortRowFor(name, portName, true); ok {
				in.portRow = r
			}
		}
		f.Set(reflect.ValueOf(in))
	} else {
		ch := pb.deadEndIn(portName)
		f.Set(reflect.ValueOf(&In{ch: ch, node: name, port: portName, trace: tr, stream: getStream, portRow: -1}))
	}
}

// wireOutPort resolves a single PortOut field: a paced binding
// (NewOutPaced, with the edge's own send rule/arc/latency/segment/label) when pb
// has one for this port name, otherwise a dead-end chan wrapper. The resolved
// paced Out is appended to sourceOuts and (when pb.outSink is non-nil) recorded
// under "node.port" for the loader's node-move travel-time updates.
//
// A paced Out's own portRow (isInput=false) plus its destination's targetRow/
// targetPortRow are resolved once here from pb.md's row tables (same timing as
// wireInPort's portRow) — the destination is static (b.pw.Target/TargetHandle never
// change after wiring), so resolving it once at construction and reading it later on
// this node's own Update goroutine (Out.PlaceDrivenAt/placeDrivenNoWalker) matches
// edgeMover's existing static-field-resolved-once discipline (edgeRow).
func wireOutPort(f reflect.Value, portName string, ctx context.Context, name string, pb PortBindings, tr *T.Trace, sourceOuts *[]*Out, getStream func() *interiorStream) {
	if b := pb.singlePaced[portName]; b.pw != nil {
		o := NewOutPaced(b.pw, ctx, name, portName, tr, b.rule, b.arc, b.latency, b.seg, b.label)
		o.stream = getStream
		o.portRow, o.targetRow, o.targetPortRow = -1, -1, -1
		if pb.md != nil {
			if r, ok := pb.md.PortRowFor(name, portName, false); ok {
				o.portRow = r
			}
			if b.pw.Target != "" {
				if r, ok := pb.md.NodeRowFor(b.pw.Target); ok {
					o.targetRow = r
				}
				if r, ok := pb.md.PortRowFor(b.pw.Target, b.pw.TargetHandle, true); ok {
					o.targetPortRow = r
				}
			}
		}
		*sourceOuts = append(*sourceOuts, o)
		if pb.outSink != nil {
			pb.outSink[name+"."+portName] = o
		}
		f.Set(reflect.ValueOf(o))
	} else {
		ch := pb.deadEndOut(portName)
		f.Set(reflect.ValueOf(&Out{ch: ch, node: name, port: portName, trace: tr}))
	}
}

// wireBroadcastPort resolves a PortBroadcast field: one paced Out per fan-out
// element recorded in pb.broadcastPaced (each with its own handle/rule/arc/
// latency/segment/label) when present, otherwise a dead-end chan slice. Each
// resolved paced Out is appended to sourceOuts and (when pb.outSink is
// non-nil) recorded under "node.handle". Row resolution mirrors wireOutPort's,
// per fan-out element.
func wireBroadcastPort(f reflect.Value, portName string, ctx context.Context, name string, pb PortBindings, tr *T.Trace, sourceOuts *[]*Out, getStream func() *interiorStream) {
	if bs := pb.broadcastPaced[portName]; len(bs) > 0 {
		outs := make(Broadcast, len(bs))
		for i, b := range bs {
			o := NewOutPaced(b.pw, ctx, name, b.handle, tr, b.rule, b.arc, b.latency, b.seg, b.label)
			o.stream = getStream
			o.portRow, o.targetRow, o.targetPortRow = -1, -1, -1
			if pb.md != nil {
				if r, ok := pb.md.PortRowFor(name, b.handle, false); ok {
					o.portRow = r
				}
				if b.pw.Target != "" {
					if r, ok := pb.md.NodeRowFor(b.pw.Target); ok {
						o.targetRow = r
					}
					if r, ok := pb.md.PortRowFor(b.pw.Target, b.pw.TargetHandle, true); ok {
						o.targetPortRow = r
					}
				}
			}
			outs[i] = o
			*sourceOuts = append(*sourceOuts, o)
			if pb.outSink != nil {
				pb.outSink[name+"."+b.handle] = o
			}
		}
		f.Set(reflect.ValueOf(outs))
	} else {
		chs := pb.deadEndOutSlice(portName)
		outs := make(Broadcast, len(chs))
		for i, c := range chs {
			outs[i] = &Out{ch: c, node: name, port: portName, trace: tr}
		}
		f.Set(reflect.ValueOf(outs))
	}
}

// populateData performs tag-driven data population: wire:"data.<key>" or
// wire:"data.state" struct tags on nodePtr's fields, read from data (a nil
// data leaves every tagged field untouched, matching the original guard).
func populateData(v reflect.Value, nodePtr any, data *NodeData) {
	if data == nil {
		return
	}
	t := reflect.TypeOf(nodePtr).Elem()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("wire")
		if tag == "" {
			continue
		}
		fv := v.Field(i)
		if !fv.CanSet() {
			continue
		}
		const dataPrefix = "data."
		const stateTag = "data.state"
		if tag == stateTag {
			// key is field name with first letter lowercased. The seed is
			// OPTIONAL: an absent key leaves the constructor default untouched
			// (the empty sentinel for held-bearing kinds), so "unset" can never
			// collide with a legitimately-held 0. Only a present key — a real
			// authored starting value — overrides the default.
			key := lowerFirst(f.Name)
			if val, ok := data.State[key]; ok {
				fv.Set(reflect.ValueOf(val))
			}
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
}

// verticalRingNormal and flatRingNormal are the two great-circle ring normals
// streamed on every node-geometry event so TS never hardcodes ring orientation.
// vertical: ring stands upright (normal points along +Z world axis).
// flat: ring lies flat (normal points along +Y world axis, Three y-up convention).
const (
	verticalRingNormalX, verticalRingNormalY, verticalRingNormalZ = 0.0, 0.0, 1.0
	flatRingNormalX, flatRingNormalY, flatRingNormalZ             = 0.0, 1.0, 0.0
)

// NodeBuilder is the public-facing type consumed by the loader.
// Ports is derived lazily from reflection; Build delegates to reflectBuild.
type NodeBuilder struct {
	Ports []PortSpec
	Build func(ctx context.Context, name string, data *NodeData, pb PortBindings, tr *T.Trace, geom nodeGeom, partnerCenter partnerCenterFn) (Node, error)
}

// Registry is the loader-facing map, populated one kind at a time by
// Register (registry.go) as each node package's init() runs.
var Registry map[string]NodeBuilder

func init() {
	// Register needs a non-nil map to write into; kindRegistry is always
	// empty at this point because package Wiring's init runs before the
	// importing packages' inits populate it via Register.
	Registry = make(map[string]NodeBuilder)
}
