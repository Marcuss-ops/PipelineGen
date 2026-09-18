# TICKET-I1 — Chunk producer contract + `(fingerprint, frame_range)` cache key

**Status: PARTIALLY LANDED — the producer and the VRAM gate are not.**

Written 2026-09-18. This is a forward design for the chunk PRODUCER, and the
`TODO.md` doctrine applies: a criterion that is not reachable from a checkout is
named, never implied.

What is ALREADY in the tree (verified, not assumed) — do not re-implement it:

- the **partition validator** and the chunker —
  `internal/capabilities/cliprender/chunk_plan.go` (`ChunkSet.Validate`,
  `BuildChunkSet`, `PlanFrameCount`), with its fail-closed cases pinned in
  `chunk_plan_test.go`;
- the **window identity** `(plan content address, frame_range)` — `Chunk.JobID`,
  derived by `chunkJobID` (the "fingerprint" half of the cache key, already
  range-inclusive);
- the **cache-key non-conflation contract** — `chunk_key_test.go`.

What is NOT implemented: the producer that submits windows, the per-window cache
ENTRY (deliberately deferred — see §4), and the VRAM reduction in §5.

Audience: whoever picks up the temporal-chunking (I1) wave. Read this before
touching `RenderJobClass`, the daemon admission path, or any render cache.

---

## 1. Why this ticket exists

The 2–5× throughput lever on the render lane is **temporal overlap of disjoint
frame windows**. Today that overlap is impossible, and the reason is structural,
not a tuning knob:

- Every job is `MonolithicSamePlan` (`kDefaultRenderJobClass`,
  `Chronon3d/apps/chronon3d_cli/daemon/daemon_render_concurrency.hpp`). The
  file states it plainly: *"No producer declares chunk membership today
  (RenderingGen submits whole clips on the clip lane and whole plans on the
  overlay lane)"*.
- A monolithic same-plan render is **one execution domain by construction**, so
  `decide_render_admission` returns `Serial` regardless of free VRAM.
- Independently, the measured peak of one 1080p FullGraph render is ~8.8 GB
  (`kMeasuredFullGraphPeakBytes`) on a 16 GB device
  (`kReferenceDeviceVramBytes`), so `peak_footprints_that_fit < 2` and even a
  disjoint class would be `Serial` today.
- Consequently `kRenderJobExecutionSerialized` is `true`, the mutex in
  `daemon_service_ipc.cpp` is held for the whole job, and the two are pinned
  together by `tests/cli/test_daemon_render_concurrency.cpp`.

So there are **two** independent blockers: (a) no producer declares chunks, and
(b) VRAM headroom. This ticket owns (a) and the artifact-cache key that (a)
requires. It does **not** pretend (b) is solved by declaring a class.

---

## 2. Existing authorities (do not create a second one)

The daemon-side machinery already exists. The design below composes it; it must
not duplicate any of it.

| Fact | Single owner |
|---|---|
| "Which kind of job is asking" | `enum class RenderJobClass` (`MonolithicSamePlan` / `ChunkedDisjointSamePlan` / `DifferentPlan`) |
| "May this job overlap" | `decide_render_admission(RenderPolicyInput)` — pure, `constexpr`, returns a `reason` |
| "Is the domain serialized" | `kRenderJobExecutionSerialized`, **derived** from `decide_render_admission(reference_device_policy_input())` |
| Serialization ↔ mutex lockstep | `tests/cli/test_daemon_render_concurrency.cpp` + the `static_assert`s beside `m_render_job_mutex` |
| A job's frame window | `runtime::job::FrameRange{first, last}` on `RenderJobRequest` (optional) |
| "Is the range present" | `range_enabled` on the IPC spec — an explicit `first=0/last=0` is a **one-frame chunk**, never "absent" (`chronon_ipc.hpp`) |
| Range validation | `normalize_render_job` → `RenderJobRequestErrorCode::InvalidFrameRange` |
| Job identity | `render_job_fingerprint(NormalizedRenderJob)` — already folds `range` |
| Converted-frame reuse identity | `media/frame_conversion/conversion_cache_key.hpp` (`content_digest`; `0` = unknown ⇒ always convert) |
| Chunk-boundary/GOP safety | `include/chronon3d/media/video/keyframe_cadence.hpp` (+ `tests/video/test_keyframe_cadence.cpp`) |
| Whole-clip artifact reuse | `internal/capabilities/cliprender/render_cache.go` (`clip_render_cache`) |

---

## 3. Normative contract for a chunk producer

A producer is any component that emits `RENDER_JOB` requests on the clip or
overlay lane (today: RenderingGen via `refactored`). To be eligible to declare
`ChunkedDisjointSamePlan`, it MUST satisfy all of the following.

**C1 — Inclusive, contiguous, exact partition.**
`FrameRange.first`/`last` are inclusive. For a plan with `N` frames, the chunks
`c₀…c_m` must satisfy:

```
c₀.first = 0
c_{i+1}.first = c_i.last + 1
c_m.last = N - 1
```

`first > last` is invalid (`InvalidFrameRange`). No gap, no overlap, full cover.

**C2 — Disjointness is a property of the DECLARATION, and it is checked.**
The producer MUST NOT declare `ChunkedDisjointSamePlan` unless C1 holds. The
class is a *claim* that overlap is safe; a wrong claim is a correctness bug, so
the validator is part of this ticket (see §6), not a convention.

**C3 — Chunk purity.**
The bytes of a chunk are a pure function of `(plan_fingerprint, frame_range,
output_spec)` — nothing else. No wall-clock, no chunk index, no ordering
dependence. This is what makes the cache safe and the assembly deterministic.

**C4 — Never publish a chunk as a clip.**
A chunk artifact is NOT a finished clip. It MUST NOT be written to
`clip_render_cache` under the whole-clip fingerprint, and it MUST NOT be handed
to the localization fan-out. A separate chunk-artifact store/key is required
(§4).

**C5 — Boundary frames are the muxer's problem, declared up front.**
Concatenating chunks reproduces the plan only if the boundary is GOP-safe. The
assembly layer MUST either (a) pick boundaries at keyframes per
`keyframe_cadence.hpp`, or (b) re-encode the boundary GOP, or (c) declare a
one-frame overlap. The chosen policy is part of the chunk plan, not an
afterthought at publish time.

---

## 4. Cache key

The identity is **two-level and must never be conflated**:

```
chunk key  = render_job_fingerprint(normalized job)     // already folds range + output_spec
clip  key  = cliprender.RenderRequest.Fingerprint()     // whole-request, unchanged
```

- Because `render_job_fingerprint` already includes `range` (see its contract
  comment), a different `frame_range` yields a different fingerprint **by
  construction** — there is no second hasher to write and no drift surface.
- The `(fingerprint, frame_range)` pair therefore reduces to the existing job
  fingerprint; the range is a *field inside* the key, not a separate axis. Any
  implementation that keys only on `plan_fingerprint` would serve chunk A's
  bytes for chunk B and is forbidden.
- The chunk-artifact store is **separate** from `clip_render_cache`. A cache hit
  on a chunk MUST NOT satisfy a whole-clip request and vice versa (C4).

**The identity half of this key already exists and is pinned.** In Go the window
identity is `Chunk.JobID`, derived by `chunkJobID(planSHA256, start, end)` —
plan content address + exact half-open boundaries + `ChunkContractVersion`. It is
namespaced (`chunk-…`, and `anchor-…` for the assembly anchor) and is
structurally incapable of occupying the clip lane's 64-char digest space, so the
two key families cannot be conflated even if the maps were merged. What is
deliberately **not** added is the per-window **cache entry/table**: as
`render_cache.go` records, a range key without a producer is a dead field that
silently never matches, so it must land in the same change that submits windows.

---

## 5. The VRAM precondition to actually flip overlap

Declaring chunks is necessary but **not sufficient**. `decide_render_admission`
will still return `Serial` while `peak_footprints_that_fit < 2`. The ordered
sequence is:

1. Reduce per-job peak transient residency below `device_total / 2` (≈8 GB on
   the reference 16 GB device). This is a VRAM-safety change with its own
   before/after evidence (a `vram_peak_mb` report), not a config tweak.
2. Only then flip `kRenderJobExecutionSerialized` to `false` **together with**
   removing the mutex in `daemon_service_ipc.cpp`, in the same commit — the
   `static_assert`s and `test_daemon_render_concurrency.cpp` enforce lockstep.
3. Re-run the disjoint-overlap measurement (§6).

Do **not** disable the serialization to "see if it helps"; the policy derives
its verdict from the measured peak on purpose.

---

## 6. Acceptance criteria (all to be implemented)

None of these are satisfied today. Each names its closing artifact.

- [x] **Partition validator** — LANDED. `ChunkSet.Validate` (`chunk_plan.go`)
      rejects a gap, an overlap, an empty window, and a range not ending at
      `N-1`, among eleven fail-closed cases pinned by
      `TestChunkSetValidateIsTheAssemblyGate`; exact cover and keyframe
      alignment are pinned by `TestBuildChunkSetIsAnExactPartition`.
- [x] **Cache-key non-conflation** — LANDED (`chunk_key_test.go`): the same
      window keeps its id across builds (`…IsTheRangeKey`); a window shared by
      two DIFFERENT splits carries one id (`…IsTheWindowNotTheSplit`); the same
      window under a different plan revision gets a different id
      (`…CarriesThePlanRevision`); and no chunk or anchor id is ever a
      whole-clip fingerprint (`…IsNotAWholeClipFingerprint`, using the same
      `isSHA256Hex` predicate the sealed plan digest uses). Each test was
      mutation-probed: dropping the range from `chunkJobID` makes
      `…IsTheRangeKey` fail with the offending window pair.
- [ ] **Admission** — two chunk footprints that fit ⇒ `ParallelDisjoint` with
      `admitted_slots ≥ 2`; one that does not fit ⇒ `Serial` with the arithmetic
      in `reason`. Extend the existing `decide_render_admission` cases.
- [ ] **Overlap (VRAM-gated, live)** — two disjoint chunks of one plan render
      concurrently with wall `w` strictly less than the sum of their solo walls.
      Requires §5 step 1. Names its host.
- [ ] **Assembly parity** — decoding the concatenated chunk artifact reproduces
      the monolithic artifact's frame sequence (GOP boundary respected per C5).
      This is the correctness gate for C5.

---

## 7. Non-goals and what is not reachable from a checkout

- **The producer implementation** (RenderingGen chunk scheduling) is not
  specified here beyond the contract in §3.
- **The VRAM reduction** (§5 step 1) is a Chronon residency change with a host
  measurement; it is out of scope for this ticket and is the real gate.
- **Live overlap numbers** cannot be produced from a checkout: they need the
  Vulkan/CUDA/NVENC host with a working daemon.
- Declaring chunks while leaving the serialization on is a **no-op** by
  construction and must not be reported as progress.
