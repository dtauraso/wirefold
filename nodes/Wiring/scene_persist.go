package Wiring

// scene_persist.go — shared machinery for the domain persisters in this package
// (overlays, camera viewpoint, scene sphere, node position, local polars, port anchor).
// Each of those files repeated the same two things: a debounced-coalesce timer and an
// atomic (tmp+rename) write. This file factors that machinery out once so the per-domain
// files hold only their domain-specific shape (which fields they own, how to marshal them).
//
// One-file-per-writer (docs/planning/visual-editor/one-file-per-goroutine.md): every lock
// that used to live here (sceneFileMu, entityFileMus/entityFileMuMu) existed because TWO
// WRITERS shared ONE file — three writers read-modify-wrote view/scene.json, and two
// writers read-modify-wrote nodes/<id>/meta.json. Splitting each shared file into one file
// per writer (view/camera.json, view/overlays.json, view/sphere.json,
// nodes/<id>/position.json, nodes/<id>/local-polars.json) removes the sharing, so there is
// nothing left to serialize — those locks are DELETED, not narrowed. Each new file's writer
// marshals its own current state fresh and writes the whole file; there is no
// read-modify-write of a document another goroutine might also be writing.
//
// The one exception is nodes/<id>/{inputs,outputs}/<port>.json (port anchor files): those
// always had exactly one writer (writePortAnchor) — entityReadModifyWrite below is kept,
// delocked, purely to preserve the port file's OTHER field (`name`) across a write; it was
// never actually racing a second writer.
//
// Old-format compatibility: existing on-disk view/scene.json and nodes/<id>/meta.json (both
// pre-split, and view/scene.json is a real untracked file on disk today) are NEVER migrated
// or deleted by this package. Each domain's loader tries its new file first and falls back
// to reading the corresponding key out of the legacy file when the new file is absent — see
// loadSceneViewpoint (scene_camera.go), loadSceneOverlays/loadSceneSphere
// (scene_overlays_persist.go/scene_sphere_persist.go) and loadTree (loader_tree.go).

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

// atomicWriteTmpSuffix is the temp-file suffix writeJSONAtomic uses before renaming into
// place, so a reader never observes a partially-written file.
const atomicWriteTmpSuffix = ".tmp"

// debouncedPersister is the generic debounce/coalesce/write machinery every domain
// persister below embeds ANONYMOUSLY (not as a named field): that lets the `writes` test
// counter promote through to e.g. `md.persist.vp.writes`. Each domain type keeps its own
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

// stop cancels a pending debounce timer; callers follow with flush() to write any pending
// value synchronously on shutdown (there is no live TS "shutdown" message — this is called
// from the clean-exit path in RunStdinReader). Locks/unlocks mu independently of flush()'s
// own take()-based locking, so this never holds mu across a flush() call (no deadlock risk).
func (c *debouncedPersister[T]) stop() {
	c.mu.Lock()
	if c.timer != nil {
		c.timer.Stop()
	}
	c.mu.Unlock()
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

// readJSONBestEffort reads path and unmarshals it into v; an absent or malformed file
// leaves v untouched (its zero value) rather than erroring. Used for LEGACY-format fallback
// reads (the old shared scene.json / meta.json a pre-split topology still has on disk) and
// anywhere else an absent file is the normal, expected case (e.g. before the first save).
func readJSONBestEffort(path string, v any) {
	readJSONIfExists(path, v)
}

// readJSONIfExists reads path and unmarshals it into v, reporting whether a file was
// actually present and successfully parsed. Unlike readJSONBestEffort's caller-facing
// contract (which only cares about v), this distinguishes "file absent" from "file present
// but its fields happen to be the zero value" — needed wherever a caller must not confuse a
// genuinely-zero-valued persisted record with no record at all (loader_tree.go's
// position.json/local-polars.json overlay check).
func readJSONIfExists(path string, v any) bool {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return false
	}
	return json.Unmarshal(raw, v) == nil
}

// readEntityObjRequired reads an existing per-entity JSON file (a port anchor file) as a
// raw-message map for a read-modify-write. The file MUST already exist (a port is always
// written before it can be moved), so an error is propagated and logged rather than
// silently proceeding on a fabricated empty object.
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
// atomically writes it back. Used only by the port-anchor writer (writePortAnchor) to
// preserve a port file's other field (`name`) across an anchor-only write. UNLOCKED: the
// port-anchor file has exactly one writer (writePortAnchor itself, from the single
// anchorPersister timer) — it never shared this file with a second writer the way
// scene.json / meta.json used to, so there is nothing to serialize here. Do not add a
// second writer of a port file without re-examining this.
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
	tmp := path + atomicWriteTmpSuffix
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
