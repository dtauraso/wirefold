// scene-env.tsx — Procedural PMREM environment texture provider.
import React, { useEffect, useState, createContext } from "react";
import { useThree } from "@react-three/fiber";
import * as THREE from "three";
import {
  SHADING_PARAM_ENV_SKY_TOP_R,
  SHADING_PARAM_ENV_SKY_TOP_G,
  SHADING_PARAM_ENV_SKY_TOP_B,
  SHADING_PARAM_ENV_SKY_BOTTOM_R,
  SHADING_PARAM_ENV_SKY_BOTTOM_G,
  SHADING_PARAM_ENV_SKY_BOTTOM_B,
  SHADING_PARAM_ENV_SKY_RADIUS,
  SHADING_PARAM_ENV_AMBIENT_COLOR,
  SHADING_PARAM_ENV_AMBIENT_INTENSITY,
  SHADING_PARAM_ENV_KEY_COLOR,
  SHADING_PARAM_ENV_KEY_INTENSITY,
  SHADING_PARAM_ENV_RIM_COLOR,
  SHADING_PARAM_ENV_RIM_INTENSITY,
  SHADING_PARAM_ENV_PMREM_BLUR,
} from "../../schema/shading-params";

/** Context carrying the procedurally generated PMREM texture (or null before ready). */
export const EnvTexContext = createContext<THREE.Texture | null>(null);


/**
 * Generates a PMREM env texture once and provides it via EnvTexContext.
 * No network requests — all geometry is inline. Does NOT touch scene.environment.
 */
export function ProceduralEnvProvider({ children }: { children: React.ReactNode }) {
  const { gl } = useThree();
  const [envTex, setEnvTex] = useState<THREE.Texture | null>(null);

  useEffect(() => {
    const pmrem = new THREE.PMREMGenerator(gl);
    pmrem.compileEquirectangularShader();

    // Build a tiny scene: gradient sky sphere, no textures.
    const envScene = new THREE.Scene();

    // Sky hemisphere — top neutral, horizon warm cream (Go-supplied tint params).
    const skyGeo = new THREE.SphereGeometry(SHADING_PARAM_ENV_SKY_RADIUS, 16, 8);
    const skyMat = new THREE.MeshBasicMaterial({
      side: THREE.BackSide,
      vertexColors: true,
    });
    const skyMesh = new THREE.Mesh(skyGeo, skyMat);
    // Tint vertices top→bottom by lerping Go's top/bottom sky colors. t=1 at the
    // top, t=0 at the horizon; channel = top + (bottom - top) * (1 - t).
    const posAttr = skyGeo.attributes.position as THREE.BufferAttribute;
    const count = posAttr.count;
    const colors = new Float32Array(count * 3);
    for (let i = 0; i < count; i++) {
      const y = posAttr.getY(i);
      const t = Math.max(0, Math.min(1, (y / SHADING_PARAM_ENV_SKY_RADIUS + 1) / 2)); // 0 bottom → 1 top
      colors[i * 3 + 0] = SHADING_PARAM_ENV_SKY_TOP_R + (SHADING_PARAM_ENV_SKY_BOTTOM_R - SHADING_PARAM_ENV_SKY_TOP_R) * (1 - t); // r
      colors[i * 3 + 1] = SHADING_PARAM_ENV_SKY_TOP_G + (SHADING_PARAM_ENV_SKY_BOTTOM_G - SHADING_PARAM_ENV_SKY_TOP_G) * (1 - t); // g
      colors[i * 3 + 2] = SHADING_PARAM_ENV_SKY_TOP_B + (SHADING_PARAM_ENV_SKY_BOTTOM_B - SHADING_PARAM_ENV_SKY_TOP_B) * (1 - t); // b
    }
    skyGeo.setAttribute("color", new THREE.BufferAttribute(colors, 3));
    envScene.add(skyMesh);

    // Soft white fill light — bakes into env (Go-supplied color + intensity).
    const fill = new THREE.AmbientLight(new THREE.Color(SHADING_PARAM_ENV_AMBIENT_COLOR), SHADING_PARAM_ENV_AMBIENT_INTENSITY);
    envScene.add(fill);
    const key = new THREE.DirectionalLight(new THREE.Color(SHADING_PARAM_ENV_KEY_COLOR), SHADING_PARAM_ENV_KEY_INTENSITY);
    key.position.set(1, 2, 1);
    envScene.add(key);
    const rim = new THREE.DirectionalLight(new THREE.Color(SHADING_PARAM_ENV_RIM_COLOR), SHADING_PARAM_ENV_RIM_INTENSITY);
    rim.position.set(-2, 1, -1);
    envScene.add(rim);

    const tex = pmrem.fromScene(envScene, SHADING_PARAM_ENV_PMREM_BLUR).texture;
    // Store in state — does NOT assign to scene.environment.
    setEnvTex(tex);

    return () => {
      tex.dispose();
      skyGeo.dispose();
      skyMat.dispose();
      pmrem.dispose();
    };
  // Intentionally empty deps: this builds the PMREM texture ONCE on mount (a fixed
  // procedural sky, not derived from props/state), and disposes it on unmount. Re-running
  // per render would leak GPU textures/PMREM generators without changing the output.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <EnvTexContext.Provider value={envTex}>
      {children}
    </EnvTexContext.Provider>
  );
}
