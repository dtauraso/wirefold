// shading_params.go — single source of truth for the scene's shading PARAMETERS
// (glass/material params, environment/lighting params, wire-tube and bead
// appearance) shared between the Go substrate and the TS visual layer.
//
// Substance vs. medium (MODEL.md, docs/go-authoritative-clock/index.html "tsgo"):
// the GPU render machinery (three.js materials, PMREMGenerator, mesh creation,
// env-map baking, binding) stays in TS — Go has no GPU. What lives here is the
// shading PARAMETER DATA: Go owns it authoritatively; TS reads these values and
// applies them to GPU materials / bakes the env map from them, sourcing no
// shading values of its own. Per-node fill/stroke already live in NODE_DEFS
// (generated from each nodes/<Kind>/SPEC.md ## View) and stay there; this file
// holds the scene-global shading constants that were previously hardcoded in
// tools/topology-vscode/src/webview/three/scene-content.tsx.
//
// Codegen: tools/gen-node-defs reads this file and emits
// tools/topology-vscode/src/schema/shading-params.ts. Constants are prefixed
// with ShadingParam so gen-node-defs can identify them via the name prefix
// (same mechanism as CurveParam* → curve-params.ts). After changing any
// constant here, regenerate with:
//   cd tools/topology-vscode && npm run gen:node-defs
//
// Colors are hex strings (consumed by THREE.Color); scalars are float/int.

package Wiring

// --- Node body: glass (MeshPhysicalMaterial) parameters -------------------
// The node sphere is rendered as transmissive glass. These mirror the
// meshPhysicalMaterial props on GraphNode in scene-content.tsx exactly.

// ShadingParamNodeTransmission is the glass transmission (1 = fully transmissive).
const ShadingParamNodeTransmission = 1.0

// ShadingParamNodeThickness is the refraction thickness of the node glass.
const ShadingParamNodeThickness = 0.0

// ShadingParamNodeRoughness is the surface roughness of the node glass.
const ShadingParamNodeRoughness = 0.12

// ShadingParamNodeIor is the index of refraction of the node glass.
const ShadingParamNodeIor = 1.5

// ShadingParamNodeMetalness is the metalness of the node glass (0 = dielectric).
const ShadingParamNodeMetalness = 0.0

// ShadingParamNodeClearcoat is the clearcoat layer strength of the node glass.
const ShadingParamNodeClearcoat = 0.0

// ShadingParamNodeClearcoatRoughness is the clearcoat roughness of the node glass.
const ShadingParamNodeClearcoatRoughness = 0.1

// ShadingParamNodeEnvMapIntensity scales the baked env-map reflection on the node glass.
const ShadingParamNodeEnvMapIntensity = 1.0

// ShadingParamNodeOpacity is the node-body opacity when not faded.
const ShadingParamNodeOpacity = 0.92

// ShadingParamNodeFadeOpacity is the base fade opacity for faded scene elements.
const ShadingParamNodeFadeOpacity = 0.25

// ShadingParamNodeFadeBodyMul multiplies the fade opacity for the node BODY
// specifically (faded body opacity = ShadingParamNodeFadeOpacity * this).
const ShadingParamNodeFadeBodyMul = 0.6

// --- Procedural environment map (ProceduralEnvProvider) -------------------
// A tiny gradient-sky scene baked into a PMREM env texture. The env-map vertex
// tint is interpolated between a top color and a bottom color over the sky
// hemisphere; these RGB components mirror the per-channel literals in
// ProceduralEnvProvider exactly (kept as components because the bake lerps them).

// ShadingParamEnvSkyTopR/G/B is the top-of-sky tint (cool neutral).
const (
	ShadingParamEnvSkyTopR = 0.78
	ShadingParamEnvSkyTopG = 0.77
	ShadingParamEnvSkyTopB = 0.74
)

// ShadingParamEnvSkyBottomR/G/B is the horizon tint (warm cream).
const (
	ShadingParamEnvSkyBottomR = 1.0
	ShadingParamEnvSkyBottomG = 0.88
	ShadingParamEnvSkyBottomB = 0.75
)

// ShadingParamEnvSkyRadius is the radius of the baked sky hemisphere.
const ShadingParamEnvSkyRadius = 50.0

// ShadingParamEnvAmbientColor / Intensity is the soft white fill light baked into the env.
const (
	ShadingParamEnvAmbientColor     = "#ffffff"
	ShadingParamEnvAmbientIntensity = 0.9
)

// ShadingParamEnvKeyColor / Intensity is the warm key directional light baked into the env.
const (
	ShadingParamEnvKeyColor     = "#ffeedd"
	ShadingParamEnvKeyIntensity = 0.45
)

// ShadingParamEnvRimColor / Intensity is the cool rim directional light baked into the env.
const (
	ShadingParamEnvRimColor     = "#aabbff"
	ShadingParamEnvRimIntensity = 0.3
)

// ShadingParamEnvPmremBlur is the PMREMGenerator.fromScene blur (sigma) applied
// when baking the env texture.
const ShadingParamEnvPmremBlur = 0.04

// --- Scene lights (Scene component) ---------------------------------------
// The two direct scene lights (separate from the baked env). Mirror the
// <ambientLight> / <directionalLight> in the Scene component.

// ShadingParamSceneAmbientIntensity is the scene ambient-light intensity.
const ShadingParamSceneAmbientIntensity = 0.6

// ShadingParamSceneDirIntensity is the scene directional-light intensity.
const ShadingParamSceneDirIntensity = 0.8

// --- Wire tube appearance (SingleEdgeTube) --------------------------------
// The always-lit base tube material. Mirrors the meshStandardMaterial on the
// base tube in SingleEdgeTube.

// ShadingParamTubeColor is the wire-tube base color.
const ShadingParamTubeColor = "#5599cc"

// ShadingParamTubeEmissive is the wire-tube emissive color.
const ShadingParamTubeEmissive = "#2255aa"

// ShadingParamTubeEmissiveIntensity is the wire-tube emissive intensity.
const ShadingParamTubeEmissiveIntensity = 0.8

// --- Bead appearance (PulseBead) ------------------------------------------
// The in-flight bead sphere. Mirrors the meshStandardMaterial on PulseBead.

// ShadingParamBeadColor is the in-flight bead color.
const ShadingParamBeadColor = "#ffffff"

// ShadingParamBeadEmissive is the in-flight bead emissive color.
const ShadingParamBeadEmissive = "#ffffff"

// ShadingParamBeadEmissiveIntensity is the in-flight bead emissive intensity.
const ShadingParamBeadEmissiveIntensity = 2.5
