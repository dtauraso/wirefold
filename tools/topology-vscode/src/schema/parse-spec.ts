// parseSpec entry point. Composes node/edge/meta parsers and runs the
// validatePorts pass. Legacy `timing.steps` is silently dropped — it
// was the SVG-era master script, replaced by per-node handlers + seed
// events in Phase 5.5.

import type { Spec } from "./types-graph";
import { arr, obj } from "./parse-primitives";
import { parseEdge, parseNode } from "./parse-nodes-edges";
import { validatePorts } from "./parse-meta";
import { TOPOLOGY_META_FIELDS } from "./meta-field-defs";
import { REQUIRED_INPUTS } from "../webview/rf/nodes/node-defs";

export function parseSpec(input: unknown, view?: { edges?: Record<string, unknown> }): Spec {
  const o = obj(input, "spec");
  const spec: Spec = {
    nodes: arr(o.nodes, "spec.nodes").map((n, i) =>
      parseNode(n, `spec.nodes[${i}]`),
    ),
    edges: arr(o.edges, "spec.edges").map((e, i) =>
      parseEdge(e, `spec.edges[${i}]`),
    ),
  };
  for (const key of Object.keys(TOPOLOGY_META_FIELDS) as (keyof typeof TOPOLOGY_META_FIELDS)[]) {
    const def = TOPOLOGY_META_FIELDS[key];
    const value = def.parse((o as Record<string, unknown>)[key]);
    if (value !== undefined) (spec as Record<string, unknown>)[key] = value;
  }
  validatePorts(spec);
  // Validate required inputs: each node with required ports must have an inbound
  // edge targeting that port, or an edgeSeeds entry for it (ring topologies).
  const inboundPorts = new Set(spec.edges.map((e) => `${e.target}:${e.targetHandle}`));
  for (const node of spec.nodes) {
    const required = REQUIRED_INPUTS[node.type];
    if (!required) continue;
    for (const port of required) {
      const hasEdge = inboundPorts.has(`${node.id}:${port}`);
      const hasEdgeSeed = (node.edgeSeeds as Record<string, unknown> | undefined)?.[port] !== undefined;
      if (!hasEdge && !hasEdgeSeed) {
        throw new Error(
          `parseSpec: node "${node.id}" (${node.type}) is missing required inbound edge for port "${port}". ` +
          `Connect an edge to this port or add edgeSeeds["${port}"] for ring topologies.`,
        );
      }
    }
  }
  if (view?.edges) {
    const knownIds = new Set(spec.edges.map((e) => e.id));
    for (const key of Object.keys(view.edges)) {
      if (!knownIds.has(key)) {
        const known = [...knownIds].join(", ") || "(none)";
        throw new Error(
          `parseSpec: view edge key "${key}" has no matching edge in spec; known: ${known}`,
        );
      }
    }
  }
  return spec;
}
