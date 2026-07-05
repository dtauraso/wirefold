package Wiring

// scene_persist.go — shared machinery for the six domain persisters in this package
// (fade, overlays, camera viewpoint, polar-lock equations, node position, port anchor).
// Each of those files repeated the same three things: a debounced-coalesce timer, a
// read-modify-write of a JSON object, and an atomic (tmp+rename) write. This file factors
// that machinery out once so the six files hold only their domain-specific shape (which
// key(s) they own, how to marshal/unmarshal them) and wire it in.
//
// Two read-modify-write flavors exist because the two persisted-file kinds have different
// failure semantics:
//   - scene.json (fade/overlays/camera/locks) is read BEST-EFFORT: an absent or malformed
//     file yields an empty object and the writer proceeds, because the writer only ever
//     owns a subset of scene.json's keys and scene.json is allowed to not exist yet (fresh
//     topology). This intentionally REPLACES an unparsable file rather than blocking the
//     write — matches every scene.json writer's pre-existing behavior.
//   - per-entity files (node meta.json, port anchor files) are read REQUIRED: the file must
//     already exist (a node/port is always written before it can be moved), so an error is
//     propagated and logged rather than silently proceeding on a fabricated empty object.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// sceneFileMu serializes read-modify-write cycles on view/scene.json across all of its
// writers (camera, overlays, fade, polar locks) so their field updates never race/clobber.
var sceneFileMu sync.Mutex

// debouncedPersister is the generic debounce/coalesce/write machinery every domain
// persister below embeds ANONYMOUSLY (not as a named field): that lets the `writes` test
// counter promote through to e.g. `md.vpPersist.writes`. Each domain type keeps its own
// `path`/`root` and `debounce` fields at its OWN top level (not inside this generic type),
// because call sites construct persisters with keyed struct literals like
// `&fadePersister{path: ..., debounce: ...}`, and Go's keyed-literal syntax cannot address a
// field nested inside an embedded struct.
type debouncedPersister[T any] struct {
	mu      sync.Mutex
	pending T
	has     bool
	timer   *time.Timer
	writes  int // count of completed writes (test observability)
}

// arm records the latest pending value and (re)arms the debounce timer, invoking flush once
// the value has been stable for `debounce`. Each call resets the window, so a continuous
// stream of updates (e.g. a drag) coalesces into a single write after activity settles.
func (c *debouncedPersister[T]) arm(debounce time.Duration, v T, flush func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = v
	c.has = true
	if c.timer == nil {
		c.timer = time.AfterFunc(debounce, flush)
	} else {
		c.timer.Reset(debounce)
	}
}

// take returns the pending value and clears it; ok is false when nothing is pending (e.g.
// a flush that raced an empty timer fire).
func (c *debouncedPersister[T]) take() (v T, ok bool) {
	c.mu.Lock()
	v, ok = c.pending, c.has
	c.has = false
	c.mu.Unlock()
	return v, ok
}

// recordWrite increments the completed-write counter (test observability).
func (c *debouncedPersister[T]) recordWrite() {
	c.mu.Lock()
	c.writes++
	c.mu.Unlock()
}

// logPersistErr logs a persister write failure in the uniform shape used by every
// debounced persister in this package. Fire-and-forget: persistence never blocks or panics
// on a failed write — the caller's `flush` just returns, and the NEXT schedule naturally
// carries the current (still-correct, in-memory) state into a future write attempt.
func logPersistErr(label, path string, err error) {
	fmt.Fprintf(os.Stderr, "%s: write %s: %v\n", label, path, err)
}

// readSceneObjBestEffort best-effort-reads path (typically scene.json) as a raw-message
// map for a read-modify-write. An absent or malformed file yields an empty map — see the
// package doc comment above for why this is intentional rather than an oversight.
func readSceneObjBestEffort(path string) map[string]json.RawMessage {
	obj := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &obj) // best-effort: see package doc comment above
	}
	return obj
}

// sceneReadModifyWrite locks sceneFileMu, best-effort-reads path's existing object, lets
// mutate edit it in place (setting only the key(s) the caller owns), then atomically writes
// it back. This is THE single read-modify-write path for scene.json, shared by the camera,
// overlays, fade, and polar-lock writers so they never race each other.
func sceneReadModifyWrite(path string, mutate func(obj map[string]json.RawMessage)) error {
	sceneFileMu.Lock()
	defer sceneFileMu.Unlock()
	obj := readSceneObjBestEffort(path)
	mutate(obj)
	return writeJSONAtomic(path, obj)
}

// readEntityObjRequired reads an existing per-entity JSON file (node meta.json, port file)
// as a raw-message map for a read-modify-write. Unlike readSceneObjBestEffort, the file MUST
// already exist, so an absence or parse failure is a real error the caller should propagate.
func readEntityObjRequired(path string) (map[string]json.RawMessage, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// entityReadModifyWrite reads a required per-entity JSON file, lets mutate edit it, then
// atomically writes it back. Shared by the node-position and port-anchor writers.
func entityReadModifyWrite(path string, mutate func(obj map[string]json.RawMessage)) error {
	obj, err := readEntityObjRequired(path)
	if err != nil {
		return err
	}
	mutate(obj)
	return writeJSONAtomic(path, obj)
}

// writeJSONAtomic marshals v and writes it to path via a temp-file-then-rename, creating
// parent directories first, so a reader never observes a partially-written file.
func writeJSONAtomic(path string, v any) error {
	out, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
