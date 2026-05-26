// ThreeView — exact 2D replica of the RF graph rendered via react-three-fiber.
// No gestures, no controls, no animation. Nodes at z=0.
// Reads RF node/edge state via rfGetNodes/rfGetEdges + subscribeRFState.

import { useEffect, useRef, useState, useCallback } from "react";
import { Canvas, useThree } from "@react-three/fiber";
import * as THREE from "three";
import type { Node as RFNode, Edge as RFEdge } from "reactflow";
import { rfGetNodes, rfGetEdges, subscribeRFState } from "../rf-imperative";
import type { NodeData, EdgeData } from "../types";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function boundingBox(nodes: RFNode<NodeData>[]) {
  if (nodes.length === 0) return { minX: -200, maxX: 200, minY: -200, maxY: 200 };
  let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
  for (const n of nodes) {
    const w = (n.data?.width ?? 110) / 2;
    const h = (n.data?.height ?? 60) / 2;
    minX = Math.min(minX, n.position.x - w);
    maxX = Math.max(maxX, n.position.x + w);
    minY = Math.min(minY, n.position.y - h);
    maxY = Math.max(maxY, n.position.y + h);
  }
  return { minX, maxX, minY, maxY };
}

// ---------------------------------------------------------------------------
// Camera setter: ortho camera sized to the graph bounds + padding.
// ---------------------------------------------------------------------------

function CameraFitter({ nodes }: { nodes: RFNode<NodeData>[] }) {
  const { camera, size } = useThree();
  useEffect(() => {
    const ortho = camera as THREE.OrthographicCamera;
    const PAD = 80;
    const { minX, maxX, minY, maxY } = boundingBox(nodes);
    const gw = (maxX - minX) + 2 * PAD;
    const gh = (maxY - minY) + 2 * PAD;
    const cx = (minX + maxX) / 2;
    const cy = (minY + maxY) / 2;
    // Fit to canvas aspect, showing the whole graph.
    const aspect = size.width / size.height;
    const frustumH = Math.max(gh, gw / aspect);
    const frustumW = frustumH * aspect;
    ortho.left   = -frustumW / 2;
    ortho.right  =  frustumW / 2;
    ortho.top    =  frustumH / 2;
    ortho.bottom = -frustumH / 2;
    // RF y is down, three y is up → negate cy.
    ortho.position.set(cx, -cy, 500);
    ortho.lookAt(cx, -cy, 0);
    ortho.near = 0.1;
    ortho.far = 2000;
    ortho.updateProjectionMatrix();
  }, [nodes, camera, size]);
  return null;
}

// ---------------------------------------------------------------------------
// Single node: sphere + optional border ring
// ---------------------------------------------------------------------------

function GraphNode({ node }: { node: RFNode<NodeData> }) {
  const x = node.position.x + (node.data?.width ?? 110) / 2;
  const y = -(node.position.y + (node.data?.height ?? 60) / 2); // RF y-down → negate
  const r = Math.min((node.data?.width ?? 110), (node.data?.height ?? 60)) / 4;
  const fillHex = node.data?.fill ?? "#ffffff";
  const strokeHex = node.data?.stroke ?? "#888888";
  const fillColor = new THREE.Color(fillHex);
  const strokeColor = new THREE.Color(strokeHex);

  return (
    <group position={[x, y, 0]}>
      <mesh>
        <sphereGeometry args={[r, 16, 16]} />
        <meshStandardMaterial color={fillColor} />
      </mesh>
      {/* border ring */}
      <mesh>
        <torusGeometry args={[r, r * 0.08, 8, 32]} />
        <meshStandardMaterial color={strokeColor} />
      </mesh>
    </group>
  );
}

// ---------------------------------------------------------------------------
// Edges: lines between node centers
// ---------------------------------------------------------------------------

function GraphEdges({
  edges,
  nodeMap,
}: {
  edges: RFEdge<EdgeData>[];
  nodeMap: Map<string, RFNode<NodeData>>;
}) {
  const points: THREE.Vector3[] = [];
  for (const e of edges) {
    const s = nodeMap.get(e.source);
    const t = nodeMap.get(e.target);
    if (!s || !t) continue;
    const sx = s.position.x + (s.data?.width ?? 110) / 2;
    const sy = -(s.position.y + (s.data?.height ?? 60) / 2);
    const tx = t.position.x + (t.data?.width ?? 110) / 2;
    const ty = -(t.position.y + (t.data?.height ?? 60) / 2);
    points.push(new THREE.Vector3(sx, sy, 0), new THREE.Vector3(tx, ty, 0));
  }
  if (points.length === 0) return null;

  const geo = new THREE.BufferGeometry().setFromPoints(points);
  return (
    <lineSegments geometry={geo}>
      <lineBasicMaterial color="#888888" />
    </lineSegments>
  );
}

// ---------------------------------------------------------------------------
// Scene: consumes nodes + edges state
// ---------------------------------------------------------------------------

function Scene({
  nodes,
  edges,
}: {
  nodes: RFNode<NodeData>[];
  edges: RFEdge<EdgeData>[];
}) {
  const nodeMap = new Map(nodes.map((n) => [n.id, n]));
  return (
    <>
      <CameraFitter nodes={nodes} />
      <ambientLight intensity={0.6} />
      <directionalLight position={[0, 0, 10]} intensity={0.8} />
      {nodes.map((n) => (
        <GraphNode key={n.id} node={n} />
      ))}
      <GraphEdges edges={edges} nodeMap={nodeMap} />
    </>
  );
}

// ---------------------------------------------------------------------------
// ThreeView: Canvas wrapper + label overlay
// ---------------------------------------------------------------------------

export function ThreeView() {
  const [nodes, setNodes] = useState<RFNode<NodeData>[]>(() => rfGetNodes() as RFNode<NodeData>[]);
  const [edges, setEdges] = useState<RFEdge<EdgeData>[]>(() => rfGetEdges() as RFEdge<EdgeData>[]);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const [canvasSize, setCanvasSize] = useState({ w: 800, h: 600 });

  // Subscribe to RF state changes
  useEffect(() => {
    return subscribeRFState((ns, es) => {
      setNodes(ns as RFNode<NodeData>[]);
      setEdges(es as RFEdge<EdgeData>[]);
    });
  }, []);

  // Observe canvas size for label projection
  const containerRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const obs = new ResizeObserver(() => {
      setCanvasSize({ w: el.clientWidth, h: el.clientHeight });
    });
    obs.observe(el);
    setCanvasSize({ w: el.clientWidth, h: el.clientHeight });
    return () => obs.disconnect();
  }, []);

  // Compute ortho frustum the same way CameraFitter does, then project each node center to pixels.
  const projectNode = useCallback(
    (node: RFNode<NodeData>): { px: number; py: number } | null => {
      const PAD = 80;
      const { minX, maxX, minY, maxY } = boundingBox(nodes);
      const gw = (maxX - minX) + 2 * PAD;
      const gh = (maxY - minY) + 2 * PAD;
      const cx = (minX + maxX) / 2;
      const cy = (minY + maxY) / 2;
      const aspect = canvasSize.w / canvasSize.h;
      const frustumH = Math.max(gh, gw / aspect);
      const frustumW = frustumH * aspect;

      const wx = node.position.x + (node.data?.width ?? 110) / 2;
      const wy = -(node.position.y + (node.data?.height ?? 60) / 2);

      // NDC: [-1,1] range
      const ndcX = (wx - cx) / (frustumW / 2);
      const ndcY = (wy - cy) / (frustumH / 2);
      // Screen pixels (y flipped: NDC +1 = top)
      const px = (ndcX + 1) / 2 * canvasSize.w;
      const py = (1 - (ndcY + 1) / 2) * canvasSize.h;
      return { px, py };
    },
    [nodes, canvasSize],
  );

  return (
    <div ref={containerRef} style={{ position: "absolute", inset: 0 }}>
      <Canvas
        orthographic
        camera={{ near: 0.1, far: 2000, position: [0, 0, 500] }}
        gl={{ antialias: true }}
        onCreated={({ gl }) => { canvasRef.current = gl.domElement; }}
        style={{ position: "absolute", inset: 0 }}
      >
        <Scene nodes={nodes} edges={edges} />
      </Canvas>

      {/* Label overlay — absolutely positioned HTML divs projected to screen */}
      {nodes.map((n) => {
        const pos = projectNode(n);
        if (!pos) return null;
        return (
          <div
            key={n.id}
            style={{
              position: "absolute",
              left: pos.px,
              top: pos.py + 4,
              transform: "translateX(-50%)",
              fontSize: 11,
              fontFamily: "monospace",
              color: "#e0e0e0",
              textShadow: "0 0 3px #000",
              pointerEvents: "none",
              whiteSpace: "nowrap",
              zIndex: 10,
            }}
          >
            {n.data?.label ?? n.id}
            {n.data?.sublabel ? (
              <span style={{ opacity: 0.7 }}> · {n.data.sublabel}</span>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}
