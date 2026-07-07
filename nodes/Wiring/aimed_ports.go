// aimed_ports.go — port-marker placement at the emit boundary.
//
// AIMING IS BACK (task/polar-torus-port-edges, reversing the prior "option A" equatorial-ring
// placement): each edge is strictly one port per side, so a CONNECTED port has exactly one
// partner node, and a connected port's direction about its own node center is the chord toward
// that partner node's CENTER — `nodeWorldPos(self) + r_i * normalize(partnerCenter -
// nodeWorldPos(self))` (portWorldPosAimed, port_geometry.go). Because both ports of an edge aim
// at each other's centers, both ports plus both centers are colinear: the edge is RADIAL (same
// θ,φ at each end) by construction, not merely tangent to a ring.
//
// The one cartesian subtraction (`partnerCenter − selfCenter`) is a DISPLAY-BOUNDARY marker
// computation — the same boundary where nodeWorldPos already converts polar→cartesian. It is
// fresh per emit, never a stored offset, and never reconstructed mid-cascade during lock
// propagation (locks.go still moves node CENTERS only; a port's aim is re-derived, not carried).
//
// An EDGELESS port (no partner) keeps the prior LOCAL POLAR OFFSET placement — its own
// equatorial ring (theta=pi/2) at its own radius r_i, swept by ring-anchor phi (portWorldPos,
// port_geometry.go). A `port ∈ torus` lock (locks.go) is still MOVEMENT-ONLY and orthogonal to
// aiming: it only ever applied to an edgeless port sitting on its ring, so it changes nothing
// about a connected/aimed port's placement.

package Wiring
