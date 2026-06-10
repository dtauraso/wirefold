---
name: per-goroutine-bridge
description: Go↔TS bridge is per-goroutine (each goroutine sends/picks up); geometry's central emitter is the deviation
metadata:
  type: feedback
---

David's Go invariant for the Go↔TS bridge: "each goroutine sends things to TS and TS sends things the goroutine picks up." Beads + firing events already follow this (per-goroutine emission). Geometry does NOT — edge curves are emitted centrally by `loader.go` (load) and `stdin_reader.go`/`NodeMoveRegistry` (node-move), and Go never emits node/port world positions at all. Fixing geometry means moving emission INTO the node goroutines and routing TS input (node-move) to the owning goroutine, not adding central emitters/handlers. Relates to the node-contract memory (local work + drive outputs).
