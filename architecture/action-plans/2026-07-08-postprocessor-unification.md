# Postprocessor Unification Refactor — Action Plan (2026-07-08)

> **Source:** User-pasted Italian audit (8 points) on the
> `clips / stock / artlist clip search / postprocessor pipeline`
> zone identifying 4 duplicated logics + 1 registry / naming
> drift + 2 occult semantic divergences.
>
> **Phase 1 SHIPPED** at commit `1e5b17011` (2026-07-08). Phases
> 2-4 are forward-pointers, each lands incrementally on `main`
> per AGENTS.md Git-Lesson-2 (no branches, no `--no-ff`, no
> `--force`, `Co-authored-by:` trailer per Git-Lesson-3,
> race-protect per Git-Lesson-4).

---

## §0 Honest status snapshot (godlike/07 NO-FAKE-AVAILABILITY)

### Community snapshot
**[SHIPPED]** Phase 1 = canonical SSOT home for `ProcessorClipSearch`
+ `Changed: true` flip on `StockAssociationProcessor` — commit
`1e5b17011` on `origin/main` (2026-07-08).
**[PLANNING-ONLY]** Phase 2 = `SceneSynthesizer` + `SceneAssetBinder`
extraction (forward-pointer `PR-POSTPROCESSOR-UNIFICATION-PHASE-2`).
**[PLANNING-ONLY]** Phase 3 = `AssetSearchPort` +
`SemanticAssetSearcher` unification (forward-pointer
`PR-POSTPROCESSOR-UNIFICATION-PHASE-3`).
**[PLANNING-ONLY]** Phase 4 = `PhraseAssetSearchService` extraction
(forward-pointer
`PR-POSTPROCESSOR-UNIFICATION-CHEN-SCRIPT-RUNNER-HARNESS`).
**[PLANNING-ONLY]** INVALIDATOR Check 65 = forward-prevention gate
`{id: PR-CHECK-65-INVALIDATOR, owner_capability: cmd/archcheck/scan, status: PLANNING-ONLY, deadline: 2026-07-29, ship_sha: empty}`.

### Audit-pin detail

Today `internal/application/scripts/adapters/` has **9
canonical postprocessors** (closed set in
`CanonicalProcessorNames()`) but **`clip_search` was NOT in
that closed set** — it was declared in
`processor_clip_search.go:38` alongside its concrete
processor. The postprocessor policy map referenced it; the
plan builder appended it conditionally on
`ExtractEntities=true`. This dual-source-of-truth violation
was Phase 1's primary audit-pin target.

A second audit-pin target: `StockAssociationProcessor` returned
`&PostProcessResult{}` (i.e. `Changed=false`) after mutating
`scene.Bindings.Stock` in the per-loop body. Pre-fix that side
effect was invisible to the registry's `IsEmpty()` gate at
`postprocessor_composite_run.go:119` — the registry would have
emitted the false "postprocessor returned empty output"
warning. Post-Phase-1 the final return is
`&PostProcessResult{Changed: true}` which the gate's
`if r.Changed { return false }` short-circuit at
`postprocessor_document.go:104-106` honors.

---

## §1 Phase 1 — SHIPPED (commit `1e5b17011`)

### §1.1 Move `ProcessorClipSearch` to canonical SSOT

**File:** `internal/application/scripts/adapters/processor_names.go`

| Before | After |
|---|---|
| `ProcessorClipSearch ProcessorName = "clip_search"` declared in `processor_clip_search.go:38` | `ProcessorClipSearch` declared at index 1 of the const block in `processor_names.go` (canonical SOLE home per godlike/06 SSOT) |
| `processor_clip_search.go` retained the const declaration | `processor_clip_search.go` keeps only the canonical-so pointer comment + the `ClipSearchProcessor` struct |
| 8-element `CanonicalProcessorNames()` closed set | 9-element closed set with `ProcessorClipSearch` inserted at index 1 (between `entities` and `metadata`) |

**godlike/06 SSOT rationale:** the goddoc on
`CanonicalProcessorNames()` is explicit about EXECUTION order
(this slice) vs REGISTRATION order
(`internal/app/wire_script_postprocess.go`). Persistence is at
the tail of EXECUTION (because each mutative processor must run
before the row is locked) but FIRST in REGISTRATION (because
post-PR-threaded writes must not run before the SQLite row is
locked — the SCRIPTCONTRACT-2026-07-08 PR-1 invariant). The two
orders differ on purpose.

### §1.2 `StockAssociationProcessor` reports `Changed: true`

**File:** `internal/application/scripts/adapters/processor_stock_association.go`

The per-loop body's mutation of `scene.Bindings.Stock` now
propagates to a non-empty `Changed: true` final return. The
registry's `IsEmpty()` short-circuit at
`postprocessor_document.go:104-106` honors the `Changed` flag
and skips the false "empty output" warning.

### §1.3 Test fixtures synced

**Files:**
- `internal/application/scripts/adapters/processor_names_test.go`
- `internal/application/scripts/adapters/normalizer_plan_tests_test.go`

| File | Test | Before | After |
|---|---|---|---|
| `processor_names_test.go::TestCanonicalProcessorNames_ClosedSet` | expected length | 8 | 9 |
| `processor_names_test.go::TestCanonicalProcessorNames_ClosedSet` | expected slice order | 8-element | 9-element with `ProcessorClipSearch` at index 1 |
| `normalizer_plan_tests_test.go::TestBuildPlanPostprocessorList` | expected length | 5 | 6 |
| `normalizer_plan_tests_test.go::TestBuildPlanPostprocessorList` | `plan.Postprocessors[1]` assertion | none | hard-coded `== "clip_search"` per the EXECUTION order |
| `normalizer_plan_tests_test.go::TestBuildPlanPostprocessorListFull` | expected length | 8 | 9 |
| `normalizer_plan_tests_test.go::TestBuildPlanPostprocessorListFull` | expected slice | 8-element | 9-element with `clip_search` at index 1 |

### §1.4 Verification (post-Phase-1)

- `gofmt -l` on all 5 modified files: **CLEAN**.
- `go vet ./internal/application/scripts/adapters/...`: **exit 0**.
- `go build ./internal/application/scripts/adapters/...`: **exit 0**.
- `go test -short -count=1 ./internal/application/scripts/adapters/...`: **PASS** (full package).

### §1.5 3-surface godlike/06 SSOT lockstep (per CANONICAL.md §1)

- **Surface 1/4** = this action plan (`architecture/action-plans/2026-07-08-postprocessor-unification.md`).
- **Surface 2/4** = `CHANGELOG.md ## Unreleased > ### Refactor` mirror entry.
- **Surface 3/4** = `AGENTS.md ## Recent cross-cutting closures` mirror entry.
- **Surface 4/4** = the canonical code commit at SHA `1e5b17011` on `origin/main`.

---

## §2 Phase 2 — Extract `SceneSynthesizer` + `SceneAssetBinder`
`{id: PR-POSTPROCESSOR-UNIFICATION-PHASE-2, owner_capability: internal/application/scripts/scene, status: PLANNING-ONLY, deadline: 2026-07-15, ship_sha: empty}`

Per audit point 3 + 4. Future agents move the prose-synth + clip
+ stock binding logics out of the two processors into a shared
package, e.g. `internal/application/scripts/scene/`:

```
internal/application/scripts/scene/
  synthesizer.go      // SceneSynthesizer.FromProse
  binder.go           // SceneAssetBinder.BindClips + BindStock + FallbackStockToClip
```

After Phase 2:

```go
func (p *ClipBindingsProcessor) Process(...) (*PostProcessResult, error) {
    return p.binder.BindClips(...).ToPostProcessResult(), nil
}
func (p *StockAssociationProcessor) Process(...) (*PostProcessResult, error) {
    return p.binder.BindStock(ctx, ..., p.search, opts).ToPostProcessResult(), nil
}
```

**godlike/07 minimum-blast-radius:** each per-PR lands
incrementally on `main`; the 2 helpers co-located in a single
new package avoid exposing `scriptpkg` at a new boundary.
**Code-reviewer verdict:** SHIP-pending forward-pointer.

---

## §3 Phase 3 — Unify `AssetSearchPort` + `SemanticAssetSearcher`
`{id: PR-POSTPROCESSOR-UNIFICATION-PHASE-3, owner_capability: internal/infrastructure/qdrant/search, status: PLANNING-ONLY, deadline: 2026-07-22, ship_sha: empty}`

Per audit point 1 + 2. Future agents collapse the 3 distinct
search ports into one canonical surface:

```go
type AssetSearchPort interface {
    SearchAssets(ctx context.Context, q AssetSearchQuery) ([]AssetSearchHit, error)
}

type AssetSearchQuery struct {
    Query       string
    Source      string  // artlist, youtube, stock
    Category    string
    MediaType   string
    WorkspaceID string
    IsSystem    bool
    Limit       int
    MinScore    float64
}
```

A single `SemanticAssetSearcher.SearchAssets(...)` replaces the
duplicated embed-filter-search-convert dance in both
`clip_search_adapter.go` and `stock_search_adapter.go`.

**godlike/07 minimum-blast-radius:** keep `ClipSearchPort` +
`StockSearchPort` as **type aliases** during the migration;

```go
type ClipSearchPort = AssetSearchPort
type StockSearchPort = AssetSearchPort
```

…then rm the duplicates only after a 7-day soak per the
FASE-2.1-VOICE-FREEZE discipline.

---

## §4 Phase 4 — Unify `PhraseAssetSearchService`
`{id: PR-POSTPROCESSOR-UNIFICATION-CHEN-SCRIPT-RUNNER-HARNESS, owner_capability: internal/application/scripts/artlist_phrase, status: PLANNING-ONLY, deadline: 2026-07-29, ship_sha: empty}`

Per audit point 6. Today the same preprocessing lives in two
spot: `ClipSearchProcessor.Process()` (dedupe, ArtlistPhrase
forwarding) and `SearchArtlistClips` (translation + folder
resolution + job enqueue). Future agents fold both into:

```go
type PhraseAssetSearchService struct {
    translator TranslationPort
    searcher   AssetSearchPort
    folderResolver ArtlistFolderResolver
    jobs JobEnqueueService
}

func (s *PhraseAssetSearchService) SearchPhrases(
    ctx context.Context,
    req PhraseAssetSearchRequest,
) []PhraseAssetMatch
```

After Phase 4:

```go
func (p *ClipSearchProcessor) Process(...) (*PostProcessResult, error) {
    matches := p.phraseSearch.SearchPhrases(ctx, ...)
    return &PostProcessResult{ArtlistClipSuggestions: matches, Changed: true}, nil
}
```

---

## §5 What NOT to do (godlike/07 negative-pattern compliance)

- **Do NOT** merge all 4 processors into a single
  `MediaProcessor` god-class. The phase plan keeps
  `ClipBindingsProcessor`, `StockAssociationProcessor`,
  `ClipSearchProcessor` as **thin orchestrators** while
  pushing logic into shared helpers + ports below them.
- **Do NOT** skip the registry+naming audit (audit point 7).
  Any drift between `CanonicalProcessorNames()` and
  `BuildPostprocessorList()` should land its own Phase 1-style
  closure before any Phase 2-4 refactor proceeds.
- **Do NOT** carry `processor_clip_search.go`'s old const
  declaration forward to a future commit. The const MUST
  remain in `processor_names.go` per godlike/06 SSOT (it
  references the same identifier; an alias would defeat the
  audit-pin).

---

## §6 Verification gates (per Phase)

For each Phase, the canonical targeted gate is:

```bash
cd /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored && \
gofmt -l <changed-files> && \
go vet ./internal/application/scripts/... && \
go build ./internal/application/scripts/... && \
go test -short -count=1 ./internal/application/scripts/... && \
bash scripts/ci-architectural-checks.sh --strict
```

`bash scripts/ci-architectural-checks.sh --strict` returns
**exit 0** iff every per-check scanner (incl. forward-prevention
gates for canonical routes, root-override scans, and archcheck
Go scanners) is green. The wave-tracker slot flip to
`status: shipped + exit_signal: true` triggers ONLY after this
gate exits 0 (godlike/07 NO-FAKE-AVAILABILITY contract).

Phase 2-4 add downstream test runs for the helpers + ports
they touch (e.g. Phase 3's adapter file in
`internal/infrastructure/qdrant/search/`).

---

## §7 Cross-references

- `AGENTS.md §God-object decomposition wave` (July 2026): Paragraph
  on the SOLE owner of postprocessor registration in
  `internal/app/wire_script_postprocess.go` =
  `registerScriptPostProcessors`. Differs by design from Phase 1's
  EXECUTION-order closed set in
  `internal/application/scripts/adapters/processor_names.go::CanonicalProcessorNames()`.
- `AGENTS.md §Pattern 3 — Adding a phase to a pipeline`
  (forward+side-effect handler pattern) — used by Phase 1's
  godlike/07 NO-FAKE-AVAILABILITY invariant on the
  StockAssociationProcessor Changed: true flip.
- `AGENTS.md §SCRIPTCONTRACT-2026-07-08 wave tracker` —
  Persistence at REGISTRATION-tail lock-safety
  invariant, cited as the rationale for the dual-order
  goddoc on `CanonicalProcessorNames()`.
- `AGENTS.md Git-Lesson-2` — direct-to-main workflow,
  `Co-authored-by:` trailer per Git-Lesson-3, race-protect
  per Git-Lesson-4. Each per-Phase commit lands atomic.
- **Sister action plan** —
  [`architecture/action-plans/2026-07-04-archcheck-phase-2-action-plan.md`](2026-07-04-archcheck-phase-2-action-plan.md)
  (Phase 4 register retires — the canonical archcheck-script
  retirement companion to this action plan's Phase 4
  verified-gate step).
- **Sister action plan** —
  [`architecture/action-plans/2026-07-04-qdrant-verification-chain.md`](2026-07-04-qdrant-verification-chain.md)
  (Qdrant DoD — the canonical Qdrant index contract companion
  to this action plan's Phase 3 `AssetSearchPort` unification).

---

## §8 Per-Phase execution checklist

For Phase 2-4 (forward-pointers):

1. Bash: `git fetch origin && git log --oneline HEAD..@{u}`
   returns empty (race-protect clean).
2. Read the relevant grip points in the existing
   `processor_clip_bindings.go` + `processor_stock_association.go`
   + `processor_clip_search.go` files (Phase 2) or the
   `clip_search_adapter.go` + `stock_search_adapter.go` files
   (Phase 3) or the `ClipSearchProcessor` + `SearchArtlistClips`
   files (Phase 4).
3. Spawn `thinker-with-files-gemini` for the topology
   validation (per the established `PR-PERSIST-PR6-CANONICAL`,
   `PR-VO-TYPED-PRIMITIVES`, `PR-SPLIT-FILES` precedent).
4. Implement the minimal diff. Run the targeted gate.
5. Spawn `code-reviewer-minimax-m3` on the diff.
6. Append this action plan's Phase section (`status: shipped`)
   and commit the **3 doc surfaces** (action plan + CHANGELOG +
   AGENTS mirror). Push direct-to-main.
7. If the user-supplied suggested_followups surface mentions a
   specific Phase, port the click into the next session as the
   higher-priority entry.

---

## §9 Lifecycle audit-trail

- **2026-07-08**: Phase 1 SHIPPED at SHA `1e5b17011` on
  `origin/main`. The 5 modified files + the action plan + the
  3-surface mirror entries constitute the canonical closure
  record per godlike/06 SSOT.
- **Forward**: Phase 2-4 are future-agent forward-pointers;
  agents that pick up the work must read this file top-to-bottom
  before any code change.

---

## §10 Co-authored-by trailer (for closure commits)

Use this byte-exact trailer (per the codebase convention; the
period `.` and trailing `AGENTS.md Git-Lesson-3.` are required
for grep-pinning by future agents):

```
Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>. AGENTS.md Git-Lesson-3.
```
