# Postprocessor TDD Guard — Action Plan (2026-08-08)

> **Source:** User-pasted Italian spec (10 partitioned test plans)
> identifying 12 concrete test cases to add BEFORE / DURING the
> postprocessor pipeline refactor (SceneSynthesizer extraction,
> SceneAssetBinder extraction, SemanticAssetSearchAdapter unification,
> ClipSourceBuilder helper extraction, postprocess-pipeline E2E).
>
> **Status (2026-08-08):** Phase 0 baseline captured. Phase 1
> (5 critical TDD tests) and Phase 2 (10 extraction-guard tests)
> land incrementally on `main` per AGENTS.md Git-Lesson-2
> (no branches, no `--no-ff`, no `--force`, `Co-authored-by:`
> trailer per Git-Lesson-3, race-protect per Git-Lesson-4).

---

## §0 Honest status snapshot (godlike/07 NO-FAKE-AVAILABILITY)

### §0.1 Phase 0 baseline (golden file before any code change)

Captured 2026-08-08 via `go test -count=1 -v` against 3 packages:

| Package | Build | Total | PASS | FAIL | SKIP |
|---|:---:|:---:|:---:|:---:|:---:|
| `internal/application/scripts/adapters` | ✅ OK | 14 top-level + 7 subtests = 21 | **21** | 0 | 0 |
| `internal/application/scripts/usecase` | ✅ OK | 37 | **37** | 0 | 0 |
| `internal/infrastructure/qdrant/search` | ✅ OK | 36 | 30 | **6** | 0 |

The 6 FAIL in `qdrant/search` are **PRE-EXISTING carry-forward**
per `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`,
NOT regressions of any closure in this plan. Detail in §0.3.

### §0.2 Two absent prefix matches → adjutsed Phase 1

The user-spec Phase 0 command `-run 'TestClipBindings|TestStockAssociation|TestClipSearch|TestProcessorNames'`
returned 0 hits for two of the four prefixes:

| Prefix | Hits | Implication |
|---|:---:|---|
| `TestClipBindings` | 6 ✅ | Test surface exists (incl. `TestClipBindingsProcessor_ProseFallback`'s 7 subtests) |
| `TestStockAssociation` | 8 ✅ | Test surface exists — **includes** `TestStockAssociation_QdrantHit_ReturnsChanged` AND `TestStockAssociation_FallbackToClip_ReturnsChanged` (both PASS, already shipped) |
| `TestClipSearch` | 0 | The ClipSearchAdapter has no `package clip_search` — `internal/infrastructure/qdrant/search/clip_search_adapter_test.go` exists with different naming (`Test*Adapter*`) |
| `TestProcessorNames` | 0 | `processor_names.go::CanonicalProcessorNames()` has NO adjacent test file (`processor_names_test.go`) |

Net effect on Phase 1: **2 of the 5 critical tests in the user's spec are already green** — they don't need creation, they need verification of the canonical-contract invariant.
The remaining 5 critical tests fall into 3 actively-missing files:

1. `processor_names_test.go` (does not exist on disk) — needs canonical-set-locked + registry-contains-clip_search regression guards.
2. The SceneSynthesizer / SceneAssetBinder test file location depends on whether the new package `internal/application/scripts/scene/` is created in Phase 2 PR-2 or pre-exists.

### §0.3 Pre-existing carry-forward (NOT regressions)

The 6 FAIL in the Phase 0 baseline are forward-pointer surface, NOT closed by any PR in this plan:

| Test | Failure | Forward-pointer |
|---|---|---|
| 5× `TestSearcher_AliasCache_*` | `qdrant collection "media_assets_current" not found` | `PR-QDRANT-COLLECTION-AUTOSTART` (deadline 2026-08-15) |
| `TestSearcher_HybridSearch_SparseTextOnly_UsesDocumentKey` | wrong literal in filter compiler | `PR-QDRANT-FIX-HYBRID-DIM` (deadline 2026-08-15) |
| `TestSearcher_HybridSearch_DenseDimensionMismatch_Rejects` | panic + nil-deref | `PR-QDRANT-FIX-HYBRID-DIM` (same — panics + nil in the filter-compile call stack) |

The post-refactor Phase 0 re-run MUST maintain the same 30 PASS / 6 FAIL count — the 6 stay identical (carry-forward unchanged), the 5 NEW test additions land as PASS.

---

## §1 Phase 1 — 5 critical TDD tests (revised after baseline)

**Goal:** pin the canonical-contract invariants of the postprocessor pipeline
BEFORE any code-motion refactor lands. After Phase 1, a code-motion PR
that breaks an invariant fails the targeted gate.

### §1.1 `TestCanonicalProcessorNames_IncludesClipSearch`

**File:** `internal/application/scripts/adapters/processor_names_test.go` (NEW)

```go
func TestCanonicalProcessorNames_IncludesClipSearch(t *testing.T) {
    names := CanonicalProcessorNames()
    require.Contains(t, names, ProcessorClipSearch)
    assert.Equal(t, "clip_search", string(ProcessorClipSearch))
}
```

**godlike/06 SSOT rationale:** `CanonicalProcessorNames()` is the
single-source-of-truth for which postprocessor names the
registry accepts. If `clip_search` is in this closed set, then any
caller (including the condition-driven builder at
`buildPostprocessorList(out)` which appends `clip_search` only when
`ExtractEntities=true`) MUST run through this gate before runtime
registration. **Forward-prevention gate** for future logics that
add new processor names outside the registry.

### §1.2 `TestBuildPostprocessorList_OnlyUsesCanonicalProcessorNames`

**File:** `internal/application/scripts/usecase/generation_plan_builder_test.go` (NEW; `package usecase` INTERNAL for access to unexported `buildPostprocessorList`)

```go
func TestBuildPostprocessorList_OnlyUsesCanonicalProcessorNames(t *testing.T) {
    cases := []scriptpkg.OutputSpec{
        {}, // all-flags-off
        {ExtractEntities: true, GenerateMetadata: true, GenerateDocument: true, GenerateVoiceover: true, GenerateSceneImages: true, SaveToDB: true}, // all-flags-on
        {ExtractEntities: true},
        {GenerateMetadata: true},
        {GenerateVoiceover: true},
        {GenerateSceneImages: true},
        {GenerateDocument: true},
        {SaveToDB: true},
        {ExtractEntities: true, GenerateMetadata: true},
        {ExtractEntities: true, GenerateDocument: true},
    }
    canonical := map[adapters.ProcessorName]bool{}
    for _, name := range adapters.CanonicalProcessorNames() {
        canonical[name] = true
    }
    for _, tc := range cases {
        t.Run(fmt.Sprintf("flags=%+v", tc), func(t *testing.T) {
            got := buildPostprocessorList(tc)
            for _, name := range got {
                assert.True(t, canonical[name], "processor %q is not in canonical set", name)
            }
            if tc == (scriptpkg.OutputSpec{}) {
                assert.Empty(t, got, "all-flags-off → empty list")
            }
        })
    }
}
```

**godlike/07 NO-FAKE-AVAILABILITY:** this is the **forward-prevention
gate** that locks the post-PR refactor in a fail-closed state.
A future agent that adds a processor name without registering
in `CanonicalProcessorNames()` surfaces as test failure.

### §1.3 `TestSceneSynthesizer_FromProse_ThreeScenesIntroClipOutro`

**File:** `internal/application/scripts/scene/synthesizer_test.go` (in the NEW package from Phase 2 PR-2; OR if pre-extracted, in `processor_clip_bindings_test.go` at `package adapters`)

```go
func TestSceneSynthesizer_FromProse_ThreeScenesIntroClipOutro(t *testing.T) {
    synth := SceneSynthesizer{}
    scenes := synth.FromProse("First sentence. Second sentence. Third sentence. Fourth sentence.", 3)
    require.Len(t, scenes, 3)
    assert.Equal(t, scriptpkg.SceneIntro, scenes[0].Kind)
    assert.Equal(t, scriptpkg.SceneClip, scenes[1].Kind)
    assert.Equal(t, scriptpkg.SceneOutro, scenes[2].Kind)
    assert.Equal(t, "scene-0", scenes[0].ID)
    assert.Equal(t, 0, scenes[0].Index)
}

func TestSceneSynthesizer_FromProse_TwoScenesAreClipKind(t *testing.T) {
    synth := SceneSynthesizer{}
    scenes := synth.FromProse("One. Two. Three.", 2)
    require.Len(t, scenes, 2)
    assert.Equal(t, scriptpkg.SceneClip, scenes[0].Kind)
    assert.Equal(t, scriptpkg.SceneClip, scenes[1].Kind)
}

func TestSceneSynthesizer_CleansJSONEnvelopeNoise(t *testing.T) {
    cleaned := CleanProseFallbackText(`{"schema_version":1,"text":"bad"} Real prose starts here.`)
    assert.Equal(t, "Real prose starts here.", cleaned)
}
```

**PR-dependency:** requires a thin exported wrapper `CleanProseFallbackText(text string) string` in `synthesizer.go` that delegates to the package-internal `cleanProseFallbackText` (1-line delegation). The exported wrapper exists SOLELY so the `package scene_test` external test surface can lock the JSON-envelope stripping contract directly without going through the higher-level `FromProse` orchestration.

### §1.4 `TestSceneAssetBinder_BindClips_OneToOnePreservesOrder`

**File:** `internal/application/scripts/adapters/scene_asset_binder_test.go` (NEW; `package adapters` INTERNAL for the recently-extracted `scene.SceneAssetBinder` from Phase 2)

```go
func TestSceneAssetBinder_BindClips_OneToOnePreservesOrder(t *testing.T) {
    binder := &scene.SceneAssetBinder{}
    scenes := []scriptpkg.SpecScene{
        {ID: "scene-0", Index: 0},
        {ID: "scene-1", Index: 1},
        {ID: "scene-2", Index: 2},
    }
    evidence := &scriptpkg.ClipEvidence{
        AcceptedClipIDs: []string{"clip-a", "clip-b", "clip-c"}, // non-alphabetical order is the load-bearing invariant
        DriveLinks: map[string]string{
            "clip-a": "https://drive/a",
            "clip-b": "https://drive/b",
            "clip-c": "https://drive/c",
        },
    }
    result := binder.BindClips(scenes, evidence, scene.BindClipOptions{})
    require.True(t, result.Changed)
    assert.Equal(t, "clip-a", scenes[0].Bindings.Clip.ClipID)
    assert.Equal(t, "clip-b", scenes[1].Bindings.Clip.ClipID)
    assert.Equal(t, "clip-c", scenes[2].Bindings.Clip.ClipID)
}
```

**godlike/07 NO-FAKE-AVAILABILITY:** the non-alphabetical clip IDs are the load-bearing invariant — a future regression that silently `sort.Strings(clipIDs)` would surface as test failure.

### §1.5 `TestPostprocessPipeline_ProseFallbackClipsThenStockFallback`

**File:** `internal/application/scripts/pipeline_postprocess_e2e_test.go` (NEW; pipeline-level)

```go
func TestPostprocessPipeline_ProseFallbackClipsThenStockFallback(t *testing.T) {
    // input: prose-only topic "Cinematic mountain drone"
    // ClipEvidence: 3 clip IDs accepted with DriveLinks
    // Qdrant mock returns 0 stock hits (fallback path)
    // Pipeline runs: ClipBindings → StockAssociation → Document
    // FinalSpecScene must contain 3 scenes, each with:
    //   .Bindings.Clip  set to accepted clip (drive_link non-empty)
    //   .Bindings.Stock fallback=true with DriveLink from Clip.DriveLink
    // Final.size == 3 with Index 0,1,2 in order
}
```

**godlike/06 SSOT rationale:** this test is the canonical pipeline-contract
guard. The builder's WHERE order is
`clip_bindings → stock_association → voiceover → images → document`.
A regression that re-orders or skips `stock_association` after
`clip_bindings` would surface as test failure.

### §1.6 Phase 1 verification

```bash
cd /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored && \
gofmt -l internal/application/scripts/adapters/processor_names_test.go \
       internal/application/scripts/usecase/generation_plan_builder_test.go \
       internal/application/scripts/scene/synthesizer_test.go \
       internal/application/scripts/adapters/scene_asset_binder_test.go \
       internal/application/scripts/pipeline_postprocess_e2e_test.go && \
go vet ./internal/application/scripts/... && \
go build ./internal/application/scripts/... && \
go test -short -count=1 -v \
   -run 'TestCanonicalProcessorNames|TestBuildPostprocessorList|TestSceneSynthesizer|TestSceneAssetBinder_BindClips|TestPostprocessPipeline' \
   ./internal/application/scripts/...
```

All 5 Phase 1 tests must PASS. Pre-existing failures (in
`qdrant/search`) remain unaffected — verify via re-run of the
Phase 0 `go test ./internal/infrastructure/qdrant/search` command.

---

## §2 Phase 2 — 10 extraction-guard tests (DA AGGIUNGERE prima dell'estrazione)

The 10 tests in the user spec are split into 4 sub-groups by
where the code they guard lives. Each sub-group targets a
specific Phase 2 PR.

### §2.1 SceneAssetBinder.BindStock (4 tests)

**File:** `internal/application/scripts/scene/binder_test.go` (NEW; `package scene` INTERNAL)

```go
func TestSceneAssetBinder_BindStock_QdrantHit(t *testing.T) // Drives AssetID via the Binder, with a fake `AssetSearchPort` returning 1 hit
func TestSceneAssetBinder_BindStock_FallbackToClip(t *testing.T) // 0 Qdrant hits + ClipBinding present → fallback propagates Clip.DriveLink
func TestSceneAssetBinder_BindStock_SearchErrorFallsBackToClip(t *testing.T) // searcher returns error → fallback path (NOT error to caller)
func TestSceneAssetBinder_BindStock_NoHitNoClipNoChange(t *testing.T) // 0 hits + 0 ClipBinding → Changed=false, Stock nil
```

**godlike/07 NO-FAKE-AVAILABILITY:** the "search error → fallback" test is the canonical regression guard for the **silent-success** anti-pattern — a future regression that propagated errors instead of falling back would surface as test failure.

### §2.2 SemanticAssetSearchAdapter (8 tests)

**File:** `internal/infrastructure/qdrant/search/semantic_asset_search_test.go` (NEW)

```go
func TestSemanticAssetSearch_EmptyQueryReturnsEmptyWithoutEmbed(t *testing.T)
func TestSemanticAssetSearch_NilSearcherFails(t *testing.T)
func TestSemanticAssetSearch_NilEmbedderFails(t *testing.T)
func TestSemanticAssetSearch_DefaultsLimitAndMinScore(t *testing.T)
func TestSemanticAssetSearch_SourceStockBuildsStockFilter(t *testing.T)
func TestSemanticAssetSearch_WorkspaceRequiredForUserTraffic(t *testing.T)
func TestSemanticAssetSearch_IsSystemAllowsEmptyWorkspace(t *testing.T)
func TestSemanticAssetSearch_ConvertsDriveURLFallback(t *testing.T)
```

**godlike/06 SSOT:** the canonical surface is `AssetSearchPort`
(Phase 3 of `POSTPROCESSOR-UNIFICATION`, ship_sha
`c3dc10d` + `e4830c7d` + `24ccaf063`). The unified adapter
implements `AssetSearchPort` — no port-duplication.

**Backend-parity tests** (the user-spec excerpt that matters most for Canonical contract):

```text
user traffic senza workspace → fail (searcher.NeedsWorkspace gate)
system traffic senza workspace → ok (IsSystem override)
source=stock → filtro stock + lifecycle_state=ACTIVE
source=artlist → filtro artlist + lifecycle_state=ACTIVE
query vuota → no embed, no Qdrant round trip
```

### §2.3 AssetSearch wrapper retro-compat (2 tests)

**File:** `internal/infrastructure/qdrant/search/clip_search_adapter_test.go` (extend) + `internal/infrastructure/qdrant/search/stock_search_adapter_test.go` (extend)

```go
func TestStockSearchAdapter_DelegatesToAssetSearchPort(t *testing.T) {
    // asserts: StockSearchAdapter.SearchStock("car", 1) delegates to
    // AssetSearchPort.SearchAssets({Query: "car", Source: "stock", MediaType: "video", Limit: 1, ...})
    // per user-spec literal: "7-day type-alias migration soak"
}
func TestClipSearchAdapter_DelegatesToAssetSearchPort(t *testing.T)
```

**godlike/07 minimum-blast-radius:** both wrappers stay as
`type ClipSearchPort = AssetSearchPort` / `type StockSearchPort =
AssetSearchPort` Go type aliases (per the Phase 3 of
`POSTPROCESSOR-UNIFICATION`) so callers compile AND forward-prevent
port name drift.

### §2.4 ClipSourceBuilder helpers (5 tests)

**File:** `internal/application/scripts/clips/source_builder_test.go` (NEW; or `processor_clip_bindings_test.go` extension if pre-extraction)

```go
func TestClipIDNormalizer_TrimDedupPreservesOrder(t *testing.T)
func TestClipResolver_FallsBackFromMediaAssetIDToDriveFileID(t *testing.T)
func TestClipEvidenceBuilder_RequireDriveLinkMovesMissingDriveToMissingClipIDs(t *testing.T)
func TestClipEvidenceBuilder_NoRequireDriveLinkAcceptsClipWithoutDriveLink(t *testing.T)
func TestClipSourceTextBuilder_IncludesDescriptionTranscriptAndTags(t *testing.T)
```

**godlike/06 SSOT:** `clip_source_builder.go` is the canonical
home for these 4 helper extractions (current code is concentrated
inside `BuildClipContext`).

### §2.5 Phase 2 verification (per sub-group)

Each sub-group follows its own canonical gate per
`architecture/current.yaml#PR-POSTPROCESSOR-UNIFICATION-PHASE-2 + PHASE-3`:

```bash
gofmt -l <new-files>; go vet ./internal/application/scripts/...; \
go build ./internal/application/scripts/...; \
go test -short -count=1 -v -run 'Test.*Bundle|PostprocessPipeline_P.*|Scene.*Bind.*' ./internal/application/scripts/...
```

---

## §3 Phase 3 — Smoke test E2E manuale (post-refactor)

After the refactor lands (Phase 1+2 PRs merged), run the live
`/api/script/generate-from-clips` smoke:

```bash
JOB_ID=$(curl -sS -X POST "http://127.0.0.1:8080/api/script/generate-from-clips" \
  -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "Test 3 plainly relevant clip-to-stock moments",
    "title": "Test 3 Plainly Relevant Clip-to-Stock Moments",
    "language": "en",
    "tone": "cinematic and energetic",
    "target_words": 500,
    "generate_document": true,
    "generate_doc": true,
    "clip_ids": ["CLIP_ID_1", "CLIP_ID_2", "CLIP_ID_3"]
  }' | jq -r .job_id)

curl -sS "http://127.0.0.1:8080/api/jobs/$JOB_ID/full" \
  -H "Authorization: Bearer $VELOX_ADMIN_TOKEN" | jq -r '
    {
      status,
      doc_link,
      scene_count: (.final_specscene.scenes | length),
      first_clip_drive_link: .final_specscene.scenes[0].bindings.clip.drive_link,
      first_stock_is_fallback: .final_specscene.scenes[0].bindings.stock.fallback
    }'
```

Acceptance:

| Assertion | Verdict |
|---|---|
| `status` == `SUCCEEDED` | required |
| `doc_link` non-empty | required |
| `scene_count >= 3` | required |
| `first_clip_drive_link` non-empty | required |
| `first_stock_is_fallback` == `true` (when Qdrant empty) OR `false` (with `asset_id`) | conditional |

---

## §4 Phase 4 — Full pipeline test sweep (post-refactor)

```bash
cd /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored && \
go test ./internal/application/scripts/adapters ./internal/application/scripts/usecase ./internal/infrastructure/qdrant/search ./internal/app -count=1 -v && \
go test ./tests/e2e/... -count=1 -v
```

Phase 0 baseline expectations to preserve:
- `scripts/adapters`: 21 → 21 + 5 (Phase 1) + ~14 (Phase 2) = **40 PASS**
- `scripts/usecase`: 37 → 37 + 1 (Phase 1 with `generation_plan_builder_test.go`) = **38 PASS**
- `qdrant/search`: 30 PASS / 6 FAIL — unchanged carry-forward

---

## §5 What NOT to do (godlike/07 negative-pattern compliance)

- **DO NOT** skip Phase 0 baseline. Without the golden output, the post-refactor diff is unmeasurable; pre-existing failures and new regressions become indistinguishable.
- **DO NOT** put `processor_names.go::ProcessorClipSearch` const in any file other than `processor_names.go` (canonical SOLE home per godlike/06 SSOT). The const exists ONCE; an alias or duplicate would defeat the Phase 1 audit-pin.
- **DO NOT** propagate `AssetSearchPort` as 3 separate per-source ports (`ClipSearchPort` + `StockSearchPort` + `ArtlistSearchPort`). The port-duplication anti-pattern is exactly what Phase 3 `POSTPROCESSOR-UNIFICATION` already unified (ship_shas `c3dc10d + e4830c7d + 24ccaf063`).
- **DO NOT** re-order the `registerScriptPostProcessors` function — Persistence is FIRST (lock-safety invariant per `SCRIPTCONTRACT-2026-07-08` PR-1) and ClipSearch is LAST (registry-closed-set ordering).
- **DO NOT** use a `t.Skip` placeholder for the Phase 1 tests. The current "skip the test if it fails to compile" approach is the canonical godlike/07 silent-success anti-pattern; markers + skip-rationale comments are required for any deferral (per `ARCHITECTURE.md §Test policy` + AGENTS.md "Pre-existing build issues" convention).
- **DO NOT** add new exported symbols beyond what the canonical surface requires. The test files use `package scene_test` (external) for some assertions and `package scene` (internal) for others — this split is the canonical godlike/07 minimum-blast-radius boundary; future expansion must respect it.

---

## §6 Verification gates (per Phase / canonical per AGENTS.md §Pattern 5)

For each Phase PR, the canonical gate is the targeted
`gofmt + go vet + go build + go test -short`:
on the affected subtrees ONLY (the 4 PRs in §8 below):

```bash
gofmt -l <modified-files>
go vet ./internal/application/scripts/...
go build ./internal/application/scripts/...
go test -short -count=1 -v -run '<target-regex>' \
  ./internal/application/scripts/...
go test -short -count=1 ./internal/application/scripts/...
go test -short -count=1 ./internal/infrastructure/qdrant/search
```

`bash scripts/ci-architectural-checks.sh --strict` exits 0 iff
every per-check scanner (incl. forward-prevention gates for canonical
routes, root-override scans, archcheck Go scanners) is green.

The wave-flip to `status: shipped + exit_signal: true` in
`architecture/current.yaml` triggers ONLY after this gate exits 0
across all Phase 1+2 PRs (godlike/07 NO-FAKE-AVAILABILITY contract).

---

## §7 Cross-references

- **godlike/06 SSOT (one canonical owner per fact):**
  - `processor_names.go::CanonicalProcessorNames()` — SOLE owner of the closed processor set
  - `processor_names.go::ProcessorClipSearch` const — SOLE canonical home of the `"clip_search"` literal
  - `internal/application/scripts/ports/asset_search_port.go::AssetSearchPort` — SOLE canonical owner of the unified search surface
  - `internal/application/scripts/scene/synthesizer.go::SceneSynthesizer` (NEW Phase 2 PR-2) — SOLE owner of prose-synth logic
  - `internal/application/scripts/scene/binder.go::SceneAssetBinder` (NEW Phase 2 PR-2) — SOLE owner of clip + stock binding logic
  - `internal/application/scripts/scene/synthesizer.go::CleanProseFallbackText` (NEW exported wrapper) — SOLE owner of JSON-envelope stripping

- **godlike/07 NO-FAKE-AVAILABILITY:**
  - `PR-POSTPROCESSOR-UNIFICATION-PHASE-1` (ship_sha `1e5b17011`, 2026-07-08) commits the canonical home for `ProcessorClipSearch` AND the `Changed: true` flip on `StockAssociationProcessor` — already ON origin/main. This action plan does NOT re-derive it.
  - `PR-POSTPROCESSOR-UNIFICATION-PHASE-2` (ship_sha `4ea04f4b36bd7526fa36f1188589fee5f2b56267`, 2026-07-08) commits the SceneSynthesizer + SceneAssetBinder extraction — already ON origin/main. The Phase 1 §1.3 + §1.4 test additions lock the canonical-contract invariants after this extraction.
  - 6 pre-existing FAIL in `qdrant/search` are forward-pointer tasks in `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` (`PR-QDRANT-FIX-HYBRID-DIM`, `PR-QDRANT-COLLECTION-AUTOSTART`).

- **Sister action plans (canonical format precedent):**
  - [`2026-07-08-postprocessor-unification.md`](2026-07-08-postprocessor-unification.md) — this action plan's direct predecessor; Phase 2-4 are forward-pointers (now subsumed by THIS plan's Phase 1+2).
  - [`2026-07-08-youtube-clip-dod-action-plan.md`](2026-07-08-youtube-clip-dod-action-plan.md) — DoD action-plan format precedent for the 12-gate template.
  - [`2026-07-04-qdrant-verification-chain.md`](2026-07-04-qdrant-verification-chain.md) — Qdrant DoD — sibling forward-pointer surface for the 6 carry-forward FAIL.

- **AGENTS.md cross-references:**
  - **§Agility 8 — Clip-evidence canonical pattern** — used by §2.4 `TestClipEvidenceBuilder_RequireDriveLinkMovesMissingDriveToMissingClipIDs`.
  - **§Pattern 0 — Port abstraction** — `AssetSearchPort` (Phase 3 SOP-UNIFICATION) is the canonical Pattern 0 port; tests in §2.2 + §2.3 invoke via composition-root injection.
  - **§Pattern 7 — Reusing existing services** — `scriptcore.Engine.WriteScript` is THE canonical script-generation entry point; this plan's pipeline-level test (§1.5) asserts the surface compatibility.
  - **§Git-Lesson-2** — direct-to-main workflow, atomic per-PR landing.
  - **§Git-Lesson-3** — `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>.` byte-exact trailer (see §10).

---

## §8 Per-PR migration sequence (7 atomic PRs)

| PR | Surface | File diff | Gate | Deadline |
|---|---|---|---|---|
| **PR-1** | `processor_names_test.go` (NEW) + `canonical set` constant flip | +1 file | Phase 1 §1.1 + §1.2 verification | 2026-08-15 |
| **PR-2** | `generation_plan_builder_test.go` (NEW) | +1 file | Phase 1 §1.2 verification | 2026-08-15 |
| **PR-3** | `scene/synthesizer_test.go` (NEW) + `CleanProseFallbackText` exported wrapper on orchestrator | +1 file + 1 export | Phase 1 §1.3 verification | 2026-08-15 |
| **PR-4** | `scene/binder_test.go` (NEW `TestSceneSynthesizer_BindClips_OneToOnePreservesOrder`) | +1 file | Phase 1 §1.4 verification | 2026-08-22 |
| **PR-5** | `pipeline_postprocess_e2e_test.go` (NEW) | +1 file | Phase 1 §1.5 verification | 2026-08-22 |
| **PR-6** | `semantic_asset_search_test.go` (NEW 8 test) | +1 file | Phase 2 §2.2 verification | 2026-08-22 |
| **PR-7** | `clip_search_adapter_test.go` + `stock_search_adapter_test.go` extension (delegation tests) + `clips/source_builder_test.go` (5 test) + `scene/binder_test.go` extension (4 BindStock test) | +2 files + 2 extensions | Phase 2 §2.3 + §2.4 + §2.1 verification | 2026-09-01 |

All 7 PRs land DIRECTLY on `main` per AGENTS.md Git-Lesson-2 (no
branches, no `--no-ff`, no `--force`). Before each push, run
`git fetch origin && git log --oneline HEAD..@{u}` — empty
required (race-protect clean per Git-Lesson-4).

---

## §9 Negative-pattern compliance check (godlike/07 typed-error contract)

Each Phase 1 / Phase 2 test file in §1 + §2 follows the
canonical convention:

- Test failures use `require.True/require.NoError/require.NotNil` (hard-fail).
- `assert.Equal/assert.Contains` for soft-equality (debug-friendly).
- NO `t.Skip` placeholder (NO silent-success — explicit `t.Run` sub-cases instead).
- NO string-compare `strings.Contains(s, "...")` heuristics (use typed enum + `require.Equal`.
- NO `panic("TODO")` placeholders in helpers (pure functions only, side-effect-free).
- Embedded interfaces (e.g. `ClipSearchPort.SearchAssets`) MUST compile before the test lands (no deferred compile gates).

---

## §10 3-surface godlike/06 SSOT lockstep (per CANONICAL.md §1)

Once the 7 PRs merge, the canonical closure record is:

- **Surface 1/4** = this action plan (`architecture/action-plans/2026-08-08-postprocessor-tdd-guard.md`).
- **Surface 2/4** = `CHANGELOG.md ## Unreleased > ### Added` mirror entry (one bullet per PR).
- **Surface 3/4** = `AGENTS.md ## Recent cross-cutting closures` mirror entry (consolidated per-PR audit-pin).
- **Surface 4/4** = the 7 commit SHAs on `origin/main` (verifiable via `git branch -r --contains <sha>` returning `origin/main` for each).

The `architecture/current.yaml` wave-tracker entry for this plan
is **DEFERRED** per the pre-existing
`architecture/current.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04`
carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer
(deadline 2026-08-15). When `PR-CURRENT-YAML-PARSE-FIX-PART-N`
(or successor) lands, the slot can be appended verbatim from
the §8 migration sequence above.

---

## §11 Lifecycle audit-trail

- **2026-08-08 — Phase 0 baseline captured** (this plan): 21+37+30 PASS / 6 FAIL carry-forward / 2 prefix-matches absent (`TestClipSearch` + `TestProcessorNames`).
- **Forward:** Phase 1 (5 critical TDD tests in 5 PRs) ship Q-by-Q from 2026-08-08 → 2026-08-22 deadline band. Phase 2 (14 extraction-guard tests across 2 PRs) ship by 2026-09-01. Phase 3 E2E manual smoke runs after each PR lands (per-Pipelinegen-server). Phase 4 final sweep restores `21 + 5 + 14 = 40` PASS in `scripts/adapters`, `37 + 1 = 38` PASS in `scripts/usecase`, `30 PASS / 6 FAIL` carry-forward in `qdrant/search`.
- **Audit-pin**: pre-existing 6 FAIL in `qdrant/search` are NOT closeable by this plan (forward-pointer per `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`).

---

## §12 Co-authored-by trailer (for closure commits, byte-exact per AGENTS.md Git-Lesson-3)

```
Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>. AGENTS.md Git-Lesson-3.
```

The trailing period `.` and `AGENTS.md Git-Lesson-3.` are required
for grep-pinning by future agents reading this action plan as the
canonical audit-record.
