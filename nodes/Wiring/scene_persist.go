package Wiring

// scene_persist.go — shared machinery for the four domain persisters in this package
// (overlays, camera viewpoint, node position, port anchor).
// Each of those files repeated the same three things: a debounced-coalesce timer, a
// read-modify-write of a JSON object, and an atomic (tmp+rename) write. This file factors
// that machinery out once so the four files hold only their domain-specific shape (which
// key(s) they own, how to marshal/unmarshal them) and wire it in.
//
// Two read-modify-write flavors exist because the two persisted-file kinds have different
// failure semantics:
//   - scene.json (overlays/camera) is read BEST-EFFORT: an absent or malformed
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
	"strings"
	"sync"
	"time"
)

// safeTreePathComponent reports whether s is safe to use as a SINGLE path segment
// under the topology tree root — i.e. it is a plain name, not "", ".", "..", not
// absolute, and contains no path separator. Guards node ids / port names (spec- or
// input-controlled) before they are filepath.Join'd into a write path, so a value like
// "../../x" cannot escape the tree root (path traversal).
func safeTreePathComponent(s string) bool {
	return s != "" && s != "." && s != ".." && !filepath.IsAbs(s) &&
		!strings.ContainsRune(s, '/') && !strings.ContainsRune(s, '\\') &&
		s == filepath.Base(s)
}

// sceneFileMu serializes read-modify-write cycles on view/scene.json across all of its
// writers (camera, overlays, polar locks) so their field updates never race/clobber.
var sceneFileMu sync.Mutex

// atomicWriteTmpSuffix is the temp-file suffix writeJSONAtomic uses before renaming into
// place, so a reader never observes a partially-written file.
const atomicWriteTmpSuffix = ".tmp"

// debouncedPersister is the generic debounce/coalesce/write machinery every domain
// persister below embeds ANONYMOUSLY (not as a named field): that lets the `writes` test
// counter promote through to e.g. `md.vpPersist.writes`. Each domain type keeps its own
// `path`/`root` and `debounce` fields at its OWN top level (not inside this generic type),
// because call sites construct persisters with keyed struct literals like
// `&overlaysPersister{path: ..., debounce: ...}`, and Go's keyed-literal syntax cannot address a
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
// a flush that raced an empty timer fire). It resets pending to the zero value — critical
// when T is a reference type (the map-valued anchor/node-pos persisters mutate pending in
// place): clearing it forces the next schedule to allocate a fresh map, so flush can iterate
// the value it took here without a concurrent schedule writing the same map underneath it.
func (c *debouncedPersister[T]) take() (v T, ok bool) {
	c.mu.Lock()
	v, ok = c.pending, c.has
	var zero T
	c.pending = zero
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
// overlays and polar-lock writers so they never race each other.
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

// entityFileMus serializes read-modify-write cycles PER per-entity file path, so independent
// writers of the SAME nodes/<id>/meta.json (WriteLocalPolars called synchronously during a
// drag AND writeQuantOffset fired from the quant-offset debounce timer goroutine) never race:
// without this, two concurrent read-modify-writes both write meta.json.tmp and the second
// os.Rename fails with "no such file or directory" (the first already renamed the shared tmp),
// AND a lost-update can drop one writer's fields. Analogous to sceneFileMu for scene.json but
// keyed per path so different nodes still persist concurrently.
var (
	entityFileMuMu sync.Mutex
	entityFileMus  = map[string]*sync.Mutex{}
)

func entityFileMu(path string) *sync.Mutex {
	entityFileMuMu.Lock()
	defer entityFileMuMu.Unlock()
	mu, ok := entityFileMus[path]
	if !ok {
		mu = &sync.Mutex{}
		entityFileMus[path] = mu
	}
	return mu
}

// entityReadModifyWrite reads a required per-entity JSON file, lets mutate edit it, then
// atomically writes it back. Shared by the node-position and port-anchor writers. The whole
// read→mutate→write is held under a per-path lock (entityFileMu) so concurrent writers of the
// same file serialize instead of racing the shared meta.json.tmp rename.
func entityReadModifyWrite(path string, mutate func(obj map[string]json.RawMessage)) error {
	mu := entityFileMu(path)
	mu.Lock()
	defer mu.Unlock()
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
	tmp := path + atomicWriteTmpSuffix
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
