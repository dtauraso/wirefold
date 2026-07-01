package Wiring

// overlay_state.go — overlayState owns the 10 per-toggle overlay-visibility booleans
// plus the Toggle*/Emit* methods that flip and stream them, the AngleLabels accessor,
// and the wholesale SetGuideVisibility push. It is owned as a field by MoveDispatch
// (md.ov), which exposes thin delegating methods so the stdin reader's method-expression
// table (overlayToggles) and call sites are unchanged. Extracting it here keeps
// node_move.go focused on the dispatch registry.

import (
	"fmt"

	T "github.com/dtauraso/wirefold/Trace"
)

// overlayState groups the 10 per-toggle visibility booleans and their flip/emit logic.
// The zero value has all bools false; newMoveDispatch initializes it with 9 true
// defaults (doubleLinksVisible stays false).
type overlayState struct {
	// sceneToriVisible is the current polar-guide tori visibility. true by default
	// (tori shown on startup). Toggled by ToggleSceneTori; emitted via EmitSceneTori.
	sceneToriVisible bool
	// scenePolesVisible is the current scene-center pole frame visibility. true by default.
	// Toggled by ToggleScenePoles; emitted via EmitScenePoles.
	scenePolesVisible bool
	// nodePolesVisible is the current per-node pole frame visibility. true by default.
	// Toggled by ToggleNodePoles; emitted via EmitNodePoles.
	nodePolesVisible bool
	// angleLabelsVisible is the current θ/φ angle arc+label visibility. true by default.
	// Toggled by ToggleAngleLabels; emitted via EmitAngleLabels.
	angleLabelsVisible bool
	// selSpherePolesVisible is the current selection-sphere pole axis visibility. true by default.
	// Toggled by ToggleSelSpherePoles; emitted via EmitSelSpherePoles.
	selSpherePolesVisible bool
	// handholdsVisible is the current rotation-handhold grab-sphere visibility. true by default.
	// Toggled by ToggleHandholds; emitted via EmitHandholds.
	handholdsVisible bool
	// labelsGlobalVisible is the current global node-label visibility. true by default.
	// Toggled by ToggleLabelsGlobal; emitted via EmitLabelsGlobal.
	labelsGlobalVisible bool
	// badgesGlobalVisible is the current global occlusion-badge visibility. true by default.
	// Toggled by ToggleBadgesGlobal; emitted via EmitBadgesGlobal.
	badgesGlobalVisible bool
	// overlaysVisible is the master overlays visibility. true by default (all overlays shown).
	// Toggled by ToggleOverlaysVis; emitted via EmitOverlaysVis.
	overlaysVisible bool
	// doubleLinksVisible is the double-link overlay visibility. false by default (overlay hidden).
	// Toggled by ToggleDoubleLinks; emitted via EmitDoubleLinks.
	doubleLinksVisible bool
}

// setFlag flips *field and emits the new value via emit. Shared body of the uniform
// Toggle* visibility methods (those that are just flip-then-emit) so each stays a single
// self-documenting line. The two flags that also drop a breadcrumb (scene/node poles)
// keep their bodies inline.
func (o *overlayState) setFlag(field *bool, emit func(bool)) {
	*field = !*field
	emit(*field)
}

// ToggleSceneTori flips the polar-guide tori visibility and emits a scene-tori event.
// Called from applyEdit on op="tori-vis"; fire-and-forget from TS.
func (o *overlayState) ToggleSceneTori(tr *T.Trace) {
	o.setFlag(&o.sceneToriVisible, tr.SceneTori)
}

// EmitSceneTori emits the current tori visibility without toggling it. Use this on
// startup or geometry-resend to seed the renderer's initial state.
func (o *overlayState) EmitSceneTori(tr *T.Trace) {
	tr.SceneTori(o.sceneToriVisible)
}

// ToggleScenePoles flips the scene-center pole frame visibility and emits a scene-poles event.
// Called from applyEdit on op="scene-poles"; fire-and-forget from TS.
func (o *overlayState) ToggleScenePoles(tr *T.Trace) {
	o.scenePolesVisible = !o.scenePolesVisible
	tr.Breadcrumb("pole-toggle-go", "scene", "", fmt.Sprintf("visible=%v", o.scenePolesVisible))
	tr.ScenePoles(o.scenePolesVisible)
}

// EmitScenePoles emits the current scene pole frame visibility without toggling it.
func (o *overlayState) EmitScenePoles(tr *T.Trace) {
	tr.ScenePoles(o.scenePolesVisible)
}

// ToggleNodePoles flips the per-node pole frame visibility and emits a node-poles event.
// Called from applyEdit on op="node-poles"; fire-and-forget from TS.
func (o *overlayState) ToggleNodePoles(tr *T.Trace) {
	o.nodePolesVisible = !o.nodePolesVisible
	tr.Breadcrumb("pole-toggle-go", "nodes", "", fmt.Sprintf("visible=%v", o.nodePolesVisible))
	tr.NodePoles(o.nodePolesVisible)
}

// EmitNodePoles emits the current per-node pole frame visibility without toggling it.
func (o *overlayState) EmitNodePoles(tr *T.Trace) {
	tr.NodePoles(o.nodePolesVisible)
}

// ToggleAngleLabels flips the θ/φ angle arc+label visibility and emits an angle-labels event.
// Called from applyEdit on op="angle-labels"; fire-and-forget from TS.
func (o *overlayState) ToggleAngleLabels(tr *T.Trace) {
	o.setFlag(&o.angleLabelsVisible, tr.AngleLabels)
}

// EmitAngleLabels emits the current angle arc+label visibility without toggling it.
func (o *overlayState) EmitAngleLabels(tr *T.Trace) {
	tr.AngleLabels(o.angleLabelsVisible)
}

// AngleLabels returns the current angle arc+label visibility.
func (o *overlayState) AngleLabels() bool {
	return o.angleLabelsVisible
}

// ToggleSelSpherePoles flips the selection-sphere pole axis visibility and emits a sel-sphere-poles event.
// Called from applyEdit on op="sel-sphere-poles"; fire-and-forget from TS.
func (o *overlayState) ToggleSelSpherePoles(tr *T.Trace) {
	o.setFlag(&o.selSpherePolesVisible, tr.SelSpherePoles)
}

// EmitSelSpherePoles emits the current selection-sphere pole axis visibility without toggling it.
func (o *overlayState) EmitSelSpherePoles(tr *T.Trace) {
	tr.SelSpherePoles(o.selSpherePolesVisible)
}

// ToggleHandholds flips the rotation-handhold grab-sphere visibility and emits a handholds event.
// Called from applyEdit on op="handholds-vis"; fire-and-forget from TS.
func (o *overlayState) ToggleHandholds(tr *T.Trace) {
	o.setFlag(&o.handholdsVisible, tr.Handholds)
}

// EmitHandholds emits the current handhold visibility without toggling it.
func (o *overlayState) EmitHandholds(tr *T.Trace) {
	tr.Handholds(o.handholdsVisible)
}

// ToggleLabelsGlobal flips the global node-label visibility and emits a labels-global event.
// Called from applyEdit on op="labels-vis"; fire-and-forget from TS.
func (o *overlayState) ToggleLabelsGlobal(tr *T.Trace) {
	o.setFlag(&o.labelsGlobalVisible, tr.LabelsGlobal)
}

// EmitLabelsGlobal emits the current global label visibility without toggling it.
func (o *overlayState) EmitLabelsGlobal(tr *T.Trace) {
	tr.LabelsGlobal(o.labelsGlobalVisible)
}

// ToggleBadgesGlobal flips the global occlusion-badge visibility and emits a badges-global event.
// Called from applyEdit on op="badges-vis"; fire-and-forget from TS.
func (o *overlayState) ToggleBadgesGlobal(tr *T.Trace) {
	o.setFlag(&o.badgesGlobalVisible, tr.BadgesGlobal)
}

// EmitBadgesGlobal emits the current global badge visibility without toggling it.
func (o *overlayState) EmitBadgesGlobal(tr *T.Trace) {
	tr.BadgesGlobal(o.badgesGlobalVisible)
}

// ToggleOverlaysVis flips the master overlays visibility and emits an overlays-vis event.
// Called from applyEdit on op="overlays-vis"; fire-and-forget from TS.
func (o *overlayState) ToggleOverlaysVis(tr *T.Trace) {
	o.setFlag(&o.overlaysVisible, tr.OverlaysVis)
}

// EmitOverlaysVis emits the current master overlays visibility without toggling it.
func (o *overlayState) EmitOverlaysVis(tr *T.Trace) {
	tr.OverlaysVis(o.overlaysVisible)
}

// ToggleDoubleLinks flips the double-link overlay visibility and emits a double-links event.
// Called from applyEdit on op="double-links"; fire-and-forget from TS.
func (o *overlayState) ToggleDoubleLinks(tr *T.Trace) {
	o.setFlag(&o.doubleLinksVisible, tr.DoubleLinks)
}

// EmitDoubleLinks emits the current double-link overlay visibility without toggling it.
func (o *overlayState) EmitDoubleLinks(tr *T.Trace) {
	tr.DoubleLinks(o.doubleLinksVisible)
}

// SetGuideVisibility sets all polar-guide visibilities to explicit values (the TS startup
// push so settings survive a Go respawn on window reload) and emits each so the renderer
// reflects them. Set-to-value, unlike the flip-style Toggle* methods.
func (o *overlayState) SetGuideVisibility(ov overlayState, tr *T.Trace) {
	*o = ov
	o.EmitSceneTori(tr)
	o.EmitScenePoles(tr)
	o.EmitNodePoles(tr)
	o.EmitAngleLabels(tr)
	o.EmitSelSpherePoles(tr)
	o.EmitHandholds(tr)
	o.EmitDoubleLinks(tr)
	o.EmitLabelsGlobal(tr)
	o.EmitBadgesGlobal(tr)
	o.EmitOverlaysVis(tr)
}
