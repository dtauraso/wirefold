// parseSpec entry point. Composes node/edge/meta parsers and runs the
// validatePorts pass. Legacy `timing.steps` is silently dropped — it
// was the SVG-era master script, replaced by per-node handlers + seed
// events in Phase 5.5.

import type { Spec } from "./types-graph";
import { arr, obj } from "./parse-primitives";
import { parseEdge, parseNode } from "./parse-nodes-edges";
import { validatePorts } from "./parse-meta";
import { TOPOLOGY_META_FIELDS } from "./meta-field-defs";
import { REQUIRED_INPUTS } from "./node-defs";

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

// DIAGNOSTIC / RENDER-ONLY — must NEVER gate substrate execution.
// This fixpoint propagates "dead node" status for editor display (red nodes,
// parse-spec diagnostics). It deliberately mirrors the shape of substrate
// firing-rule logic but carries none of it — the Go substrate does NOT reject
// graphs with missing required inputs (see commit 0e8d843 and memory:
// feedback_enforce_required_inputs). REQUIRED_INPUTS is generated from Go AST
// so the port list cannot drift; only this editor-side propagation can.
//
// Returns a map of nodeId → reason string for every node that is missing a
// required inbound edge or whose required inputs are fed only by dead nodes.
// Uses a fixpoint: newly-dead nodes can kill downstream nodes transitively.
export function requiredInputDiagnostics(spec: Spec): Map<string, string> {
  // Build feeders map: (targetNodeId, targetHandle) → [sourceNodeId, ...]
  const feeders = new Map<string, string[]>();
  for (const edge of spec.edges) {
    const key = `${edge.target}:${edge.targetHandle}`;
    let arr = feeders.get(key);
    if (!arr) { arr = []; feeders.set(key, arr); }
    arr.push(edge.source);
  }

  // Collect only nodes that have required inputs
  const requiredNodes = spec.nodes.filter((n) => !!REQUIRED_INPUTS[n.type]);

  // Fixpoint: iterate until no new dead nodes are added
  const dead = new Set<string>();
  let changed = true;
  while (changed) {
    changed = false;
    for (const node of requiredNodes) {
      if (dead.has(node.id)) continue;
      const required = REQUIRED_INPUTS[node.type];
      let isDead = false;
      for (const port of required) {
        const sources = feeders.get(`${node.id}:${port}`) ?? [];
        const hasLiveFeeder = sources.some((s) => !dead.has(s));
        if (sources.length === 0 || !hasLiveFeeder) { isDead = true; break; }
      }
      if (isDead) { dead.add(node.id); changed = true; }
    }
  }

  // Build result map with per-port reason fragments
  const result = new Map<string, string>();
  for (const node of requiredNodes) {
    if (!dead.has(node.id)) continue;
    const required = REQUIRED_INPUTS[node.type];
    const reasons: string[] = [];
    for (const port of required) {
      const sources = feeders.get(`${node.id}:${port}`) ?? [];
      if (sources.length === 0) {
        reasons.push(`missing required input "${port}"`);
      } else {
        const deadSources = sources.filter((s) => dead.has(s));
        if (deadSources.length === sources.length) {
          reasons.push(`required input "${port}" only fed by dead node(s): ${deadSources.join(", ")}`);
        }
      }
    }
    if (reasons.length > 0) result.set(node.id, reasons.join("; "));
  }
  return result;
}
