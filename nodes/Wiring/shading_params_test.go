package Wiring

import "testing"

// Phase 4 verifier (docs/go-authoritative-clock/index.html, Verify row "4 ·
// Shading": "go test — shading params emitted"). Go owns the shading PARAMETER
// values; TS reads them via the generated shading-params.ts and binds them to
// GPU materials. This test asserts every shading param Go now owns is present
// and exactly the value the renderer must apply, so an accidental change to a
// material value can't slip through without turning this red (the deterministic
// half of the gate; pixel fidelity is the manual smoke-check).
//
// The expected values are written out independently here (not referencing the
// consts) so editing a const in shading_params.go without intent breaks this.

func TestShadingParamsFloat(t *testing.T) {
	cases := []struct {
		name string
		got  float64
		want float64
	}{
		// Node-body glass (MeshPhysicalMaterial).
		{"NodeTransmission", ShadingParamNodeTransmission, 1.0},
		{"NodeThickness", ShadingParamNodeThickness, 0.0},
		{"NodeRoughness", ShadingParamNodeRoughness, 0.12},
		{"NodeIor", ShadingParamNodeIor, 1.5},
		{"NodeMetalness", ShadingParamNodeMetalness, 0.0},
		{"NodeClearcoat", ShadingParamNodeClearcoat, 0.0},
		{"NodeClearcoatRoughness", ShadingParamNodeClearcoatRoughness, 0.1},
		{"NodeEnvMapIntensity", ShadingParamNodeEnvMapIntensity, 1.0},
		{"NodeOpacity", ShadingParamNodeOpacity, 0.92},
		{"NodeFadeOpacity", ShadingParamNodeFadeOpacity, 0.25},
		{"NodeFadeBodyMul", ShadingParamNodeFadeBodyMul, 0.6},
		// Procedural env map.
		{"EnvSkyTopR", ShadingParamEnvSkyTopR, 0.78},
		{"EnvSkyTopG", ShadingParamEnvSkyTopG, 0.77},
		{"EnvSkyTopB", ShadingParamEnvSkyTopB, 0.74},
		{"EnvSkyBottomR", ShadingParamEnvSkyBottomR, 1.0},
		{"EnvSkyBottomG", ShadingParamEnvSkyBottomG, 0.88},
		{"EnvSkyBottomB", ShadingParamEnvSkyBottomB, 0.75},
		{"EnvSkyRadius", ShadingParamEnvSkyRadius, 50.0},
		{"EnvAmbientIntensity", ShadingParamEnvAmbientIntensity, 0.9},
		{"EnvKeyIntensity", ShadingParamEnvKeyIntensity, 0.45},
		{"EnvRimIntensity", ShadingParamEnvRimIntensity, 0.3},
		{"EnvPmremBlur", ShadingParamEnvPmremBlur, 0.04},
		// Scene lights.
		{"SceneAmbientIntensity", ShadingParamSceneAmbientIntensity, 0.6},
		{"SceneDirIntensity", ShadingParamSceneDirIntensity, 0.8},
		// Wire tube + bead.
		{"TubeEmissiveIntensity", ShadingParamTubeEmissiveIntensity, 0.8},
		{"BeadEmissiveIntensity", ShadingParamBeadEmissiveIntensity, 2.5},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("ShadingParam%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestShadingParamsColor(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"EnvAmbientColor", ShadingParamEnvAmbientColor, "#ffffff"},
		{"EnvKeyColor", ShadingParamEnvKeyColor, "#ffeedd"},
		{"EnvRimColor", ShadingParamEnvRimColor, "#aabbff"},
		{"TubeColor", ShadingParamTubeColor, "#5599cc"},
		{"TubeEmissive", ShadingParamTubeEmissive, "#2255aa"},
		{"BeadColor", ShadingParamBeadColor, "#ffffff"},
		{"BeadEmissive", ShadingParamBeadEmissive, "#ffffff"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("ShadingParam%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}
