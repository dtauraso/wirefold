// sphere_layout.go — graph-level node-position relaxation for the non-rooted
// layout. Node centers are stored absolutely (meta.json x/y/z); a drag/resize
// relaxes the centers so each edge's endpoints sit at the source node's R apart.
//
// MODEL: each node is a sphere of radius R (nodeR, port_geometry.go). An edge
// S->T means S outputs to T, so T should sit on S's sphere surface — the rest
// length of that edge is R_S. relaxPositions iterates the edges, nudging each
// endpoint toward that rest length, with pinned nodes held fixed.

package Wiring

import "sort"

// sphereEdge is a DIRECTED connection used for relaxation: Source outputs to
// Target, so the edge's rest length is Source's sphere radius (Target sits on
// Source's sphere surface).
type sphereEdge struct {
	Source string
	Target string
}

const relaxIterations = 16

// relaxPositions runs a constraint-relaxation pass over node centers: for each
// directed edge S->T the rest length is radius[S] (T sits on S's sphere). Each
// iteration nudges both endpoints toward that rest length, splitting the
// correction by weight (pinned nodes get weight 0 and do not move). Edges are
// sorted for deterministic results regardless of caller map order.
func relaxPositions(centers map[string]vec3, edges []sphereEdge, radius map[string]float64, pinned map[string]bool, iterations int) map[string]vec3 {
	pos := make(map[string]vec3, len(centers))
	for k, v := range centers {
		pos[k] = v
	}
	es := append([]sphereEdge(nil), edges...)
	sort.Slice(es, func(i, j int) bool {
		if es[i].Source != es[j].Source {
			return es[i].Source < es[j].Source
		}
		return es[i].Target < es[j].Target
	})
	for it := 0; it < iterations; it++ {
		for _, e := range es {
			if e.Source == "" || e.Target == "" {
				continue
			}
			pa, oka := pos[e.Source]
			pb, okb := pos[e.Target]
			if !oka || !okb {
				continue
			}
			L := radius[e.Source]
			if L <= 0 {
				continue
			}
			d := pb.sub(pa)
			dist := d.length()
			if dist < 1e-9 {
				d = vec3{X: 1}
				dist = 1
			}
			diff := (dist - L) / dist
			wa, wb := 1.0, 1.0
			if pinned[e.Source] {
				wa = 0
			}
			if pinned[e.Target] {
				wb = 0
			}
			tot := wa + wb
			if tot == 0 {
				continue
			}
			corr := d.scale(diff)
			pos[e.Source] = pa.add(corr.scale(wa / tot))
			pos[e.Target] = pb.sub(corr.scale(wb / tot))
		}
	}
	return pos
}
