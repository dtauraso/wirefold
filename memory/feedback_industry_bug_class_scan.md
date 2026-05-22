---
name: Name and check the bug class before declaring a change ready
description: When touching code in a known bug-prone category (animation/timing/state, IPC, persistence, concurrency), name the well-known bug class first and check against it
type: feedback
---

When code I'm writing falls into a category with a known catalog of "everyone hits this" bugs, name the bug class out loud and check the change against it *before* declaring it ready.

> **Top-level rule.** If I find myself building infrastructure (clock / queue / diff / layout / parser / cache / scheduler / state-machine / backpressure / event bus / serializer / undo stack), pause and survey 3+ niches' solved versions before rolling my own. This is the single highest-leverage check in this file — most "didn't know the industry solved it" rounds come from skipping it.

## Six moves for surfacing solved-elsewhere knowledge

These compose. Run them at the start of meaningful work, not as a ritual on every line.

1. **Pre-design pause when building infrastructure.** If the task is shaped like "build a clock / queue / diff / layout engine / state-machine framework / backpressure mechanism / serializer / scheduler / parser / cache / event bus / undo stack," that's the smell of "probably solved many times in many niches." Stop and ask: which 3+ niches solved this? Is one of their solutions a fit before rolling my own? Cheap to run, expensive to skip — this is exactly the "didn't know the industry solved it" filter.

2. **Shape-indexed search, not just name-indexed.** The class+aliases catalog (below) works when I already suspect a class. For "I have a phenomenon, what is it?" use the shape index: behavior-shape sentences ("two things must tick in lockstep at different rates"; "X happens logically but Y takes time to catch up"; "many producers, one consumer, can't lose, can't block"; "state must reconcile after disconnection"). The user's phenomenon → matching shape → niche aliases → literature.

3. **Translate user-described phenomena into niche vocabularies before implementing.** When the user describes behavior in their own words ("halo turns on when first member receives input"), deliberately render it in 3+ niche dialects (game-dev: "view-state binding to tween completion"; DES: "presentation event ordering vs simulation event ordering"; UI framework: "animation-driven derived state"). If any translation rings a niche bell, follow the thread before coding.

4. **Recognize field-shaped problems by their texture.** Concurrency → kernels/databases. Layout → typography/CAD. Simulation → physics/EE. Scheduling → OS/real-time. Merging → version control/CRDT/phylogenetics. Caching → CPU/web/databases. State-machine framework → telecoms/embedded. When the texture matches, search the field's textbook (or canonical post) before designing.

5. **Retroactive niche tagging.** When a session resolves a bug class, ask: which niche would have caught this in 5 minutes? Record the niche name on the catalog entry. Over time the catalog acquires a "things X niche knows we don't" map, which itself tells me which niches are worth pre-consulting for future similar problems.

6. **Niche-vocabulary import.** For the work in front of me, ask: what standard solution would field X have for this shape? What structure from their literature fits? Rotate through fields depending on the work: game dev (animation/timing), DBA (caching/persistence), kernel (concurrency), DSP (filters/sampling), networking (clock skew/lag), compiler (transformation correctness), OS (scheduling/resource accounting).

7. **Coordinates over labels.** Niches publish *named points* (`Store(capacity=1)`, "Petri-net firing rule", "CSP rendezvous") and the surrounding ecosystem reinforces each name as a closed category — citations, tutorials, and libraries stay within-niche. That makes labels lossy: claim one and inherit assumptions you didn't want; refuse one and lose communicable shorthand for the 90% that does match. Resist the label collapse. Decompose the named pattern into the *coordinates* it actually fixes (e.g. for slot-1 channels: capacity, who-frees-the-slot, sync-vs-async, who-observes-completion, what-counts-as-arrival), and describe the wirefold position as a point in that space. Cite the named patterns as nearby coordinates, not as the thing being implemented. Apply this whenever I'm about to write "this is just X with a tweak" — the tweak is the whole point and X's frame will smuggle in expectations the topology doesn't share.

**Why:** the catalog spans more niches than any single practitioner's lived path crosses. The deeper structural problem the user named: "the industry is so niched that their solution organizing system doesn't scale" — every niche files the same underlying bug class under its own vocabulary (game dev: "fix your timestep"; DES: "logical vs physical clock"; UI: "implicit animations"; distributed systems: "vector clocks"). None of them index against each other; each name presupposes its niche, so a query in one vocabulary won't surface the others. Surfacing the cross-niche aliases is what I bring to the collaboration.

**How to apply:** when starting or reviewing a change, classify it. If it falls in any of these buckets, do the scan *before* declaring the change ready. State the class + the specific check in the working text so the user sees the reasoning, not just the patch. If no bucket matches, skip — this is a filter, not a ritual.

**Entry format (deliberate convention).** Each bucket includes:
- the *underlying* class name (vocabulary-neutral when possible),
- a one-line *check* phrased as a question,
- typical *symptoms* the user might describe,
- the *fix shape*,
- **niche aliases** — what each industry calls this class. The aliases are the Rosetta stone: a future query in any niche's vocabulary should land here. Add aliases as new ones surface. Aliases scale the catalog beyond me-as-its-only-reader.

---

### Animation / time-based UI

- **Check:** is wall-clock time (`Date.now`, `performance.now`, `setTimeout` for decay) used while a logical clock (sim clock, video clock, scrub position) exists?
- **Symptoms:** pause-decoupling, drift, "it kept going after I paused."
- **Fix:** unify on the logical clock; the view reads `getSimTime()` (or equivalent), not wall time.
- **Niche aliases:** "media clock" (video players), "transport clock" (DAWs), "playback clock" (animation engines), "scrub-aware time" (timeline editors).

### Model/view temporal decoupling

- **Check:** does the view subscribe to the *model's* event stream (which fires at logical-instant times) when the user perceives the visual *animation arrival* as the meaningful moment?
- **Symptoms:** "the indicator lit up before the pulse arrived," "the badge showed `complete` while the bar was still filling," "the toast appeared 300ms before the row finished sliding in."
- **Fix:** bind view state to animation lifecycle (anim-start/anim-end), not raw model events. Generalized: `view state = f(model event, animation params, current wall time)`.
- **Niche aliases:** "fix your timestep" / "interpolated rendering" (games — Glenn Fiedler), "logical vs physical clock" / "playback speed decoupling" (DES — SimPy, OMNeT++, ns-3, Verilog waveform viewers), "implicit animations" (Flutter, SwiftUI, CSS transitions), "client-side prediction with rollback" (networked games), "optimistic UI" / "tween-aware state" (web frontends).

### State subscriptions / event listeners

- **Check:** do all subscribers detach on unmount? Will one throwing subscriber take down the bus?
- **Symptoms:** memory leaks across remounts, one listener bug taking down all UI.
- **Fix:** isolate listener throws (try/catch per listener); route caught exceptions to a probe.
- **Niche aliases:** "subscription leak" (Rx/observables), "use-effect cleanup bug" (React), "event handler leak" (DOM), "signal/slot dangling pointer" (Qt).

### Persistence / dump-on-debounce

- **Check:** does an empty snapshot overwrite a meaningful one?
- **Symptoms:** the log file ends up empty after the bug just happened.
- **Fix:** guard with `if (snapshot.length === 0) return;` or accumulate without clearing on dump.
- **Niche aliases:** "lost-update on flush" (databases), "atomic-write-after-empty" (filesystems), "checkpoint-clobbering" (long-running services).

### `this`-binding on host APIs

- **Check:** are `setTimeout`/`clearTimeout`/`addEventListener` etc. assigned as object methods, losing global `this`?
- **Symptoms:** "Illegal invocation" in browsers; works in Node, fails in browser, or vice versa.
- **Fix:** wrap in arrow functions: `(fn, ms) => setTimeout(fn, ms)`.
- **Niche aliases:** "method binding" (JS classes), "unbound method" (Python), "method-vs-bound-method confusion" (any OO language with first-class methods).

### Concurrency / channels / goroutines

- **Check:** writer-without-reader deadlocks? channel overwrite without backpressure? unbounded fan-out?
- **Niche aliases:** "lost wakeup" (kernel sync), "fast producer / slow consumer" (queueing), "unbounded buffer" (streaming systems), "head-of-line blocking" (network).

### Caching / memoization

- **Check:** keyed correctly? invalidated on the right events? stale-while-revalidate semantics intentional?
- **Niche aliases:** "cache invalidation" (general — Phil Karlton's quip), "stale read" (databases), "memoization key bug" (functional programming), "ETag mismatch" (HTTP).

### Replay / determinism

- **Check:** any wall-clock or `Math.random` reads inside the replayable path?
- **Fix:** must be seeded or driven from the trace.
- **Niche aliases:** "non-deterministic replay" (games / record-replay debuggers), "side effects in pure code" (FP), "deterministic simulation" (FoundationDB-style testing), "rr divergence" (Mozilla rr).

### Pause/resume of any kind

- **Check:** every counter, timer, and animation in scope frozen consistently? If more than one site reinvents pause bookkeeping, that's the signal to unify.
- **Fix:** unify on a frozen-on-pause clock. The "more than one site reinvents pause" smell is itself a refactor trigger.
- **Niche aliases:** "transport pause" (DAWs / video editors), "world-pause vs UI-pause" (games), "scrubbing semantics" (timeline editors).

### React effect deps

- **Check:** do object/array deps trigger remounts they shouldn't? Is cleanup symmetric with setup?
- **Niche aliases:** "stale closure" (React), "missing dependency" (eslint-react), "useEffect identity bug."

### Diff / merge / undo

- **Check:** is the inverse operation actually inverse? Round-trip identity test exists?
- **Niche aliases:** "operational transform inverse" (collaborative editing), "CRDT merge associativity" (distributed state), "undo stack drift" (editors).

---

**Maintenance.** Add a bucket when a new class surfaces in this codebase. Add aliases when a new niche-vocabulary appears (don't wait for the bug; if I'm thinking about a class and remember the alias from a niche, log it). Removing buckets is fine if they prove not load-bearing here. Aliases shouldn't be removed — they're the Rosetta stone.
