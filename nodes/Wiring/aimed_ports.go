// aimed_ports.go — port-marker placement at the emit boundary.
//
// The dynamic aim model (a port pointing toward its connected node's moving center) is GONE
// (polar-frame-rewrite.md option A): an edge runs port-to-port, so a port no longer aims at a
// partner and there is no aimed-port registry. Aiming required `targetCenter − nodeWorldPos`,
// a mid-pipeline vector subtraction the polar frame forbids.
//
// A port's placement is a LOCAL POLAR OFFSET about its own node — its own equatorial ring
// (theta=pi/2) at its own radius r_i, swept by ring-anchor phi (portWorldPos, port_geometry.go).
// A torus lock is MOVEMENT-ONLY: since placement is always on the polar ring by construction, a
// lock changes NOTHING about where a port is drawn — there is no ring-projection override here.

package Wiring
