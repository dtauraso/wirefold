---
branch: task/mutex-scene-persist
---

# One file per goroutine

## The change

Every lock in `scene_persist` exists because **two writers share one file**. Split the
files so each writer owns its own, and there is nothing left to serialize.

    TODAY                                          THIS

    camera   ─┐                                    camera   ──▶ camera.json
    overlays ─┼─ sceneFileMu ──▶ view/scene.json    overlays ──▶ overlays.json
    sphere   ─┘                                    sphere   ──▶ sphere.json

    quantOffset ─┐                                 quantOffset ──▶ position.json
                 ├─ entityFileMus[path] ──▶ meta.json
    localPolars ─┘                                 localPolars ──▶ local-polars.json

This is the same move that removed the other four locks — give the state one owner — with
the state being a file rather than a struct field.

## What each lock becomes

| Lock | Why it exists | After the split |
|---|---|---|
| `sceneFileMu` | three writers read-modify-write ONE json document; disjoint keys do not help because the unit of write is the whole map | **deleted** — one writer per file, no read-modify-write of a shared document |
| `entityFileMus[path]` + `entityFileMuMu` | `writeQuantOffset` (timer goroutine) and `WriteLocalPolars` (node's own mover) both write `nodes/<id>/meta.json` | **deleted** — different files, no shared path |
| `debouncedPersister.mu` ×5 | guards `pending`/`has`/`timer` between the goroutine that arms and the timer that fires | **NOT addressed by the split.** This is in-memory state, not a file. See below. |

So the split kills three of the four. Do not claim it kills the fourth.

## The fourth lock is a separate question

`debouncedPersister.mu` guards in-memory debounce bookkeeping shared by the arming
goroutine and the `time.AfterFunc` goroutine. Splitting files does not touch it. It has its
own answer — route schedules to one owner goroutine instead of `AfterFunc` — but that is a
different change and should not be smuggled into this one. Land the file split, then decide.

## Also true, and better after the split

`writeJSONAtomic` gives every writer the same literal `path + ".tmp"`. Two writers on one
path can therefore collide on the temp name and the second `os.Rename` fails outright —
`no such file or directory`, an error that has been seen in this repo's own test output.
With one writer per file that collision cannot occur. Note the lock was *hiding* this, not
fixing it.

## The hard part: this is an on-disk format change

Existing `topology/` in this repo has real `nodes/<id>/meta.json` files and a
`view/scene.json`. Splitting the writers splits the format. That means:

- **Readers must be updated in the same commit as writers.** A writer that splits without a
  reader that reassembles loses the user's layout silently — the worst possible failure for
  a persistence change.
- **Existing on-disk files must still load.** Decide deliberately between a migration
  (read old, write new, delete old) and a reader that accepts both. Do not leave it
  implicit. `topology/` is checked in, so whatever is chosen must handle the committed
  files, not just fresh ones.
- **A round-trip test is the real check**: load an existing pre-split topology, drag, save,
  reload, and assert the geometry is identical. Green unit tests have hidden live
  persistence failures in this repo before; drive the real binary.

## What must be proven

1. `sceneFileMu`, `entityFileMus`, `entityFileMuMu` are deleted — not narrowed.
2. Each new file has exactly ONE writer. Assert it, do not assume it — a second writer
   appearing later is exactly how these locks were born.
3. The pre-split on-disk format still loads, and a load→drag→save→reload round trip
   preserves geometry exactly.
4. `-race -count=3 ./...` clean.
5. Drive it in the LIVE editor: drag a node, reload the window, confirm the position
   survived. Persistence bugs have escaped this repo's unit suite three times.

## Not doing

Not addressing `debouncedPersister.mu` here (see above). Not touching `Trace.mu` or
`LayoutHolder.mu` — different questions, already settled.
