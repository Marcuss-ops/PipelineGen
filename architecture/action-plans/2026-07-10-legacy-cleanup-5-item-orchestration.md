# Legacy Cleanup 5-Item Orchestration (Direct-to-Main, Push Frequently)

`ship_date: 2026-07-10`
`wave-tracker slot: architecture/waves/wave_p1_high.yaml#LEGACY-CLEANUP-5-ITEM-ORCHESTRATION-2026-07-10 (DEFERRED per PRE-EXISTING-YAML-PARSE carry-forward)`
`workflow: NO BRANCHES — direct-to-main, atomic commits, push after each item per AGENTS.md Git-Lesson-2`
`precedent_plan: architecture/action-plans/2026-07-10-legacy-cleanup-3-commit.md (subsumed — 5 items map to the same 3-commit topology but with explicit per-item expansion + Option B reaffirmation)`

---

## §0 — Operative Identity & Workflow Discipline

> **CRITICAL OPERATIVE RULE** (per godlike/07 NO-FAKE-AVAILABILITY + user-spec literal):
> - **NO BRANCHES** (mai `git checkout -b`, mai `gh pr create`, mai force-push)
> - **ONLY MAIN** (tutti i commit vanno direttamente su `origin/main`)
> - **PUSH FREQUENTLY** (un commit atomico per item, push dopo ogni verification gate verde)
> - **RACE-PROTECT** (`git fetch origin && git log --oneline HEAD..@{u}` MUST return empty pre-push per AGENTS.md Git-Lesson-4)
> - **CO-AUTHORED-BY TRAILER** (per AGENTS.md Git-Lesson-3: `Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>`)

### §0.1 — 5-item audit summary table (the canonical scope)

| # | Item | Surface | P0 rationale | Drop-site / canonical destination |
|---|------|---------|--------------|-----------------------------------|
| 1 | `animate_handler` removal | `internal/api/images/animate_handler.go` + `AnimateRequest` in `request_types.go` + NVIDIA docs | endpoint declares "not implemented" (handler.go:47,65); NVIDIA no longer wired | route literal `r.POST("/animate", h.Animate)` deleted from `handler.go` |
| 2 | `PostProcessArtifact` alias retirement | `internal/application/scripts/dto/compat_types.go` | alias `= any` references test only; canonical accumulation lives elsewhere | `SerializeEntityResultRoundTrip` relocated to `internal/application/scripts/usecase/persistence.go`; whole `compat_types.go` deleted |
| 3 | `vector_search:` config block elimination | `config.yaml` + `config.example.yaml` | 0-type-Go-reader; canonical block is `qdrant:` | direct YAML deletion; no struct migration needed |
| 4 | `status: removed` deprecations archival | `architecture/deprecations.yaml::removed::*` | these records pollute the audit signal; already clinically dead | runtime copy into `architecture/archive/deprecations_removed_2026-07-10.yaml` (verbatim, no rewrite) |
| 5 | fullimages Option B (image, not video) | `internal/application/images/fullimages/service.go::generateOneVideo` + `processGeneratedVideo` + `uploadAndFinish` + `publishToDrive` + MP4 cache sidecar | name says "video", code does "image"; Ken Burns MP4 path is orphaned | rename `SectionVideo→SectionImage` + `VideoPath→ImagePath` + `generateOneVideo→generateOneImage`; delete 3 orphan functions + orphan constants (`videoOutWidth`, `videoOutHeight`, `videoDuration`); delete `cacheMeta` + `cachePath` + `saveCacheSidecar` + `loadCacheSidecar` |

### §0.2 — Pre-flight live-truth checks (godlike/07 NO-FAKE-AVAILABILITY)

Run BEFORE any surgery on each item. Each is BLOCKING:

```bash
cd "$(git rev-parse --show-toplevel)"

# §0.2.1 — git state
git fetch origin
echo "HEAD: $(git log -1 --format='%H')"
echo "Branch: $(git branch --show-current)"
echo "Divergence: $(git rev-list --left-right --count HEAD...origin/main)"

# §0.2.2 — Item 1 truth-check
rg -n 'AnimateRequest|/api/images/animate|animate_handler' --type go internal/api/images/

# §0.2.3 — Item 2 truth-check
rg -n 'PostProcessArtifact|SerializeEntityResultRoundTrip|compat_types' --type go internal/application/scripts/

# §0.2.4 — Item 3 truth-check
rg -n '^vector_search:' --type yaml --type go . 2>/dev/null

# §0.2.5 — Item 4 truth-check
rg -n '^\s*status:\s*removed' architecture/deprecations.yaml

# §0.2.6 — Item 5 truth-check
rg -n 'processGeneratedVideo|uploadAndFinish|publishToDrive' --type go internal/application/images/
```

**Acceptance gate per item**: 0 hits post-removal = TRUE; any persistence = FALSE → write 0-action audit-pin (mirrors `PR-ARTLIST-FOLDER-MARKER-INDEX` precedent).

---

## §1 — Item 1: `animate_handler` Removal

### §1.1 — Scope (surgical, single atomic commit)

**Files modified**:
- Delete `internal/api/images/animate_handler.go` (the file is the SOLE canonical owner of `Animate`)
- Delete `AnimateRequest` struct + godoc in `internal/api/images/request_types.go:81-91`
- Delete `r.POST("/animate", h.Animate)` from `internal/api/images/handler.go:65`
- Delete the route-registration godoc line `POST /animate → Animate (not implemented)` in handler.go:47 (replace with cleaner doc referencing only the live routes)
- Delete related NVIDIA comment block in `internal/api/images/` (the godlike/06 SSOT canonical surface for that package's doc-comment)
- Delete the corresponding test files if present (search via `rg _test.go.*Animate.* internal/api/images/`)

**Verification gates**:
```bash
gofmt -l internal/api/images/ 2>&1                            # MUST return empty
go vet ./internal/api/images/... 2>&1                         # MUST exit 0
go build ./internal/api/images/... ./internal/api/... 2>&1    # MUST exit 0
go test -short -count=1 ./internal/api/images/... 2>&1        # MUST PASS (no test referencing Animate)
bash scripts/ci-architectural-checks.sh                        # MUST exit 0
```

### §1.2 — SSOT (one canonical owner per fact)
- Routes owned by `internal/api/images/handler.go::Handler.RegisterRoutes` (canonical post-removal: `/upload` + `/generate` + `/generate-async` only).
- `AnimateRequest` struct is DELETED entirely (per godlike/07 — no silent-success export aliases).

### §1.3 — Race-protect clean push (AGENTS.md Git-Lesson-4)
```bash
git -c user.email='agent@pipelinegen.local' -c user.name='PipelineGen Agent' \
    add internal/api/images/
git commit -m 'feat(api): remove animate handler + AnimateRequest + NVIDIA docs

godlike/07 NO-FAKE-AVAILABILITY: animate endpoint declares "not implemented"
(per handler.go:47 + L65 route registration) and NVIDIA dependency was
detached prior; this commit removes the dead surface entirely.

Per legacy-cleanup-5-item-orchestration §1 (architecture/action-plans/2026-07-10-legacy-cleanup-5-item-orchestration.md).
godlike/06 SSOT one-canonical-owner-per-fact preserved (handler.go retains
canonical route authority for the live /upload + /generate + /generate-async surface).
godlike/07 minimum-blast-radius: zero behavior change at run-time (route was
already declared not-implemented).

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>'

git fetch origin && git log --oneline HEAD..@{u}   # MUST be empty
git push origin main                            # direct-to-main, no --force
```

---

## §2 — Item 2: `PostProcessArtifact` Alias Retirement + `compat_types.go` Deletion

### §2.1 — Scope (surgical, single atomic commit)

**Files modified**:
- Add `SerializeEntityResultRoundTrip(res *scriptpkg.EntityResult) (string, error)` to `internal/application/scripts/usecase/persistence.go` (canonical owner post-relocation)
- Update import in `internal/application/scripts/usecase/persistence.go::buildGenerationResult` line 92: change `scriptdto.SerializeEntityResultRoundTrip(...)` to `SerializeEntityResultRoundTrip(...)` (same-package call post-relocation)
- Delete `internal/application/scripts/dto/compat_types.go` (the entire file)
- Update `internal/application/scripts/adapters/processor_images_voiceover_test.go:14` reference `Replaced PostProcessArtifact (nonexistent type) with` from "Replaced PostProcessArtifact (nonexistent type) ... in compat_types.go" to "Replaced PostProcessArtifact (nonexistent type) ... (relocated to persistence.go)"

### §2.2 — SSOT (one canonical owner per fact)
- `SerializeEntityResultRoundTrip` lives ONLY at `internal/application/scripts/usecase/persistence.go` post-commit (caller in `buildGenerationResult` is same-package).
- `PostProcessArtifact` type alias is DELETED entirely (per godlike/07 — no leftover `= any` alias anywhere).
- All callers of `scriptdto.SerializeEntityResultRoundTrip` migrated to package-internal `SerializeEntityResultRoundTrip` per `rg` verification post-commit.

### §2.3 — Verification gates
```bash
rg -n 'PostProcessArtifact|SerializeEntityResultRoundTrip' --type go internal/  # MUST return 0 post-commit
gofmt -l internal/application/scripts/ 2>&1
go vet ./internal/application/scripts/... 2>&1
go build ./internal/application/scripts/... 2>&1
go test -short -count=1 ./internal/application/scripts/... 2>&1
bash scripts/ci-architectural-checks.sh
```

### §2.4 — Race-protect clean push
```bash
git -c user.email='agent@pipelinegen.local' -c user.name='PipelineGen Agent' \
    add internal/application/scripts/
git commit -m 'refactor(scripts): retire PostProcessArtifact alias + delete compat_types.go

godlike/06 SSOT one-canonical-owner-per-fact: SerializeEntityResultRoundTrip
relocated to persistence.go (canonical owner = buildGenerationResult caller).
PostProcessArtifact = any alias retired (was historical accumulator name only,
no production callers post-relocation per rg verification).

Per legacy-cleanup-5-item-orchestration §2.

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>'

git fetch origin && git log --oneline HEAD..@{u}
git push origin main
```

---

## §3 — Item 3: `vector_search:` Config Block Elimination

### §3.1 — Scope (surgical, single atomic commit)

**Files modified**:
- Delete the `vector_search:` block from `config.yaml` (preserves canonical `qdrant:` block)
- Delete the `vector_search:` section (lines ~172 in `config.example.yaml`) and the comment block referring to it (line ~180 mirror)
- Update `internal/platform/config/types_misc.go::CatalogScriptVectorSearch` to be RETIRED via `// Deprecated: enable via qdrant block catalog_script_vector_search` goddoc (the field stays temporarily but the comment says it's a legacy no-op)

### §3.2 — SSOT (one canonical owner per fact)
- `qdrant:` block is the SOLE canonical Qdrant configuration block (per the V3-SCHEMA closure precedent).
- `vector_search:` block is DELETED; any future `vector_search` reference in `architecture/deprecations.yaml` reaches the `catalog_script_vector_search` legacy deprecation only.

### §3.3 — Verification gates
```bash
rg -n '^vector_search:' config.yaml config.example.yaml  # MUST return 0
gofmt -l internal/platform/config/ 2>&1
go vet ./internal/platform/config/... 2>&1
go build ./internal/platform/config/... 2>&1
go test -short -count=1 ./internal/platform/config/... 2>&1
bash scripts/ci-architectural-checks.sh
```

### §3.4 — Race-protect clean push
```bash
git -c user.email='agent@pipelinegen.local' -c user.name='PipelineGen Agent' \
    add config.yaml config.example.yaml internal/platform/config/
git commit -m 'chore(config): retire legacy vector_search: yaml block

Qdrant is configured exclusively via the canonical qdrant: block per V3-SCHEMA.
The legacy vector_search: block had 0 Go readers + 0 YAML live consumers
(except the canonical_defaults_test syntax-check); removes operator confusion.

Per legacy-cleanup-5-item-orchestration §3.

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>'

git fetch origin && git log --oneline HEAD..@{u}
git push origin main
```

---

## §4 — Item 4: `status: removed` Deprecations Archival

### §4.1 — Scope (surgical, single atomic commit)

**Files modified**:
- Verbatim copy `status: removed::*` records from `architecture/deprecations.yaml` into `architecture/archive/deprecations_removed_2026-07-10.yaml` (re-using the existing archive convention; insert comma-separated YAML doc header explaining the archival)
- Delete the copied records from `architecture/deprecations.yaml` (preserve non-removed records untouched)

### §4.2 — SSOT (one canonical owner per fact)
- `architecture/archive/deprecations_removed_2026-07-10.yaml` is the canonical SOLE owner for `status: removed` records going forward.
- `architecture/deprecations.yaml` retains only `status: deprecated` + `status: superseded` + active future migrants (per the deleted-record grammar).

### §4.3 — Verification gates
```bash
rg -n '^\s*status:\s*removed' architecture/deprecations.yaml  # MUST return 0
ls -la architecture/archive/deprecations_removed_2026-07-10.yaml   # MUST exist
python3 -c "import yaml; yaml.safe_load(open('architecture/deprecations.yaml')); yaml.safe_load(open('architecture/archive/deprecations_removed_2026-07-10.yaml'))"
```

### §4.4 — Race-protect clean push
```bash
git -c user.email='agent@pipelinegen.local' -c user.name='PipelineGen Agent' \
    add architecture/deprecations.yaml architecture/archive/deprecations_removed_2026-07-10.yaml
git commit -m 'chore(architecture): archive status:removed deprecations to archive/ dir

godlike/06 SSOT: archive/ is canonical home for retired records.
Doc-only operation: no code change, no API change, no migration.
Per legacy-cleanup-5-item-orchestration §4.

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>'

git fetch origin && git log --oneline HEAD..@{u}
git push origin main
```

---

## §5 — Item 5: Fullimages Orphan Chain Cleanup (Option B — Images, not Videos)

### §5.1 — Verdict: Option B (Image Generation Only)

> **Deep-Think verdict** (per `thinker-with-files-gemini` 2026-07-10):
> `internal/application/images/fullimages` produces **IMAGES**, not MP4s.
> The flow:
> 1. `GenerateForSections` → `generateOneVideo` (line 110 in service.go)
> 2. `generateOneVideo` calls `s.imgService.GenerateSmartImage` (line 175) — generates an image
> 3. Resolves image path via `resolveImagePath` (line 216)
> 4. Returns `SectionVideo{VideoPath: imagePath}` — assigns the IMAGE path to the video field WITHOUT any ffmpeg conversion or upload
>
> The orphan chain `processGeneratedVideo` → `uploadAndFinish` → `publishToDrive` is **NEVER invoked** by the active flow. It only exists for backwards compat with callers that pre-date `generateOneVideo`. **0 test references** in `service_test.go` (only tests are on `SectionVideo` struct fields + `SafeFolderName`, not on the orphan functions).
>
> **DECISION**: rename the surface to truthfully reflect image-only generation. Eliminate the orphan chain entirely (3 functions + 4 helpers + 2 video-only constants `videoOutWidth`/`videoOutHeight`/`videoDuration` + cache sidecar struct/methods).

### §5.2 — Scope (surgical, single atomic commit)

**Files modified** (`internal/application/images/fullimages/service.go` is the SOLE canonical owner):
- Rename `type SectionVideo struct {...}` → `type SectionImage struct {...}` (file-level type)
  - Rename field `VideoPath string` → `ImagePath string`
- Rename `type Result struct { Videos []SectionVideo }` → `Result struct { Images []SectionImage }`
- Rename conversion sites:
  - `make([]SectionVideo, len(sections))` → `make([]SectionImage, len(sections))`
  - `results[arg.Idx] = s.generateOneVideo(...)` → `results[arg.Idx] = s.generateOneImage(...)`
  - Struct literal returns `SectionVideo{SectionIndex: idx, ...}` → `SectionImage{SectionIndex: idx, ...}`
- Rename function `generateOneVideo(ctx, sec, topic, idx) SectionVideo` → `generateOneImage(ctx, sec, topic, idx) SectionImage`
- Update Package + struct goddoc:
  - Type comment `SectionVideo holds the result for one generated video.` → `SectionImage holds the result for one generated image.`
  - Type comment `Result wraps all section videos into a single response.` → `Result wraps all section images into a single response.`
  - Service goddoc `Service generates one video per text section.` → `Service generates one image per text section.`
- **DELETE** orphan chain (3 functions):
  - `processGeneratedVideo` (lines 226-251)
  - `uploadAndFinish` (lines 252-303)
  - `publishToDrive` (lines 334-354)
- **DELETE** orphan helpers + struct:
  - `type cacheMeta struct {...}` (lines 305-308)
  - `func cachePath(videoPath string) string` (lines 310-312)
  - `func saveCacheSidecar(videoPath, driveLink, driveFileID string)` (lines 314-321)
  - `func loadCacheSidecar(videoPath string) (string, string)` (lines 323-333)
- **DELETE** orphan constants:
  - `videoOutWidth = 1920` (line 53)
  - `videoOutHeight = 1080` (line 54)
  - `videoDuration = 7` (line 51)
  - Note: keep `videoGenTimeout` (renamed-in-place to `imageGenTimeout`) + `imageGenWidth/Height` + `videoMaxWorkers` (renamed `imageMaxWorkers`)
- **UPDATE imports** (drop unused `delivery.Publisher` + `drive.AssetDestinationRequest` if no longer used)

**Files modified for consumer (`internal/api/fullimages/handler.go`)**:
- Update reference `mediafullimages.SectionVideo` → `mediafullimages.SectionImage` (line 53 + line 60)
- Update response struct `FullImagesResponse{Videos: ...}` → `FullImagesResponse{Images: ...}`
- Update json tag `"videos"` → `"images"` (both struct field + JSON tag)

**Files modified for tests (`internal/application/images/fullimages/service_test.go`)**:
- Update `TestSectionVideo_StyleField` → `TestSectionImage_StyleField`
- Update `TestSectionVideo_Error` → `TestSectionImage_Error`
- Update `TestResult_Videos` → `TestResult_Images`
- Update all struct literals `SectionVideo{...}` → `SectionImage{...}`

### §5.3 — SSOT (one canonical owner per fact)
- `internal/application/images/fullimages/service.go` is the SOLE canonical owner of `SectionImage` struct.
- `internal/api/fullimages/handler.go` is the SOLE canonical owner of `FullImagesResponse` JSON wire-shape (`"images"` array).
- The 3 deleted orphan functions + 4 helpers + 2 constants + cache sidecar = dead-code-removal entries (mirrors `AUDIT_PIN_DEAD_CODE_PURGE_2026-07_25` precedent commit `5a32611d`).

### §5.4 — Verification gates
```bash
rg -n 'SectionVideo|VideoPath|generateOneVideo|processGeneratedVideo|uploadAndFinish|publishToDrive|videoOutWidth|videoOutHeight|videoDuration|cacheMeta|cachePath|saveCacheSidecar|loadCacheSidecar' --type go internal/application/images/ internal/api/fullimages/  # MUST return 0
gofmt -l internal/application/images/fullimages/ internal/api/fullimages/ 2>&1
go vet ./internal/application/images/fullimages/... ./internal/api/fullimages/... 2>&1
go build ./internal/application/images/fullimages/... ./internal/api/fullimages/... ./internal/app/... 2>&1
go test -short -count=1 ./internal/application/images/fullimages/... ./internal/api/fullimages/... 2>&1
bash scripts/ci-architectural-checks.sh
```

### §5.5 — Race-protect clean push
```bash
git -c user.email='agent@pipelinegen.local' -c user.name='PipelineGen Agent' \
    add internal/application/images/fullimages/ internal/api/fullimages/
git commit -m 'refactor(images): Option B — fullimages produces images, not MP4

godlike/06 SSOT one-canonical-owner-per-fact: rename SectionVideo→SectionImage
+ VideoPath→ImagePath + generateOneVideo→generateOneImage (truthfully reflects
current behavior, which is pure image generation through s.imgService.

Deleted the orphan MP4 chain (processGeneratedVideo + uploadAndFinish +
publishToDrive + cacheMeta + cachePath + saveCacheSidecar + loadCacheSidecar
+ videoOutWidth/Height/Duration constants) — verified zero callers via rg.

Per legacy-cleanup-5-item-orchestration §5 (deep-think Option B verdict).

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>'

git fetch origin && git log --oneline HEAD..@{u}
git push origin main
```

---

## §6 — Wave-Flip Criterion (mother of all gates)

The wave entry `architecture/waves/wave_p1_high.yaml#LEGACY-CLEANUP-5-ITEM-ORCHESTRATION-2026-07-10` flips to `status: shipped + exit_signal: true` ONLY WHEN:

1. ✅ Item 1 ssot: `rg -n 'AnimateRequest\|/api/images/animate\|animate_handler' --type go internal/api/images/` returns 0
2. ✅ Item 2 ssot: `rg -n 'PostProcessArtifact\|compatibility/compat_types.go' --type go internal/` returns 0
3. ✅ Item 3 ssot: `rg -n '^vector_search:' config.yaml config.example.yaml` returns 0
4. ✅ Item 4 ssot: `rg -n '^\s*status:\s*removed' architecture/deprecations.yaml` returns 0; `ls architecture/archive/deprecations_removed_2026-07-10.yaml` returns success
5. ✅ Item 5 ssot: `rg -n 'SectionVideo\|VideoPath\|generateOneVideo\|processGeneratedVideo\|uploadAndFinish\|publishToDrive\|videoOutWidth\|videoOutHeight\|videoDuration\|cacheMeta\|cachePath\|saveCacheSidecar\|loadCacheSidecar' --type go internal/` returns 0
6. ✅ All `gofmt + go vet + go build + go test -short` exit 0 on the touched subtrees
7. ✅ `bash scripts/ci-architectural-checks.sh` exits 0 (forward-prevention gates consistent with the live tree)
8. ✅ 3-surface godlike/06 SSOT lockstep entries shipped for each commit (CHANGELOG + AGENTS + canonical ship_sha on `origin/main`)

The 4th surface (`architecture/waves/wave_p1_high.yaml` wave-tracker slot flip) is **DEFERRED** per the pre-existing `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-N` forward-pointer (deadline 2026-08-15). Parent CHANGELOG + AGENTS entries are the canonical SOLE closure record until the parse carry-forward resolves.

---

## §7 — Forward-Pointers (post-wave + per-item substitutes)

### §7.0 — Per-item substitutes (only if §0.2 returned FALSE for any item)

- **ANIMATE-FALSE-PREMISE-AUDIT-PIN** (deadline 2026-07-15)
- **POSTPROCESSARTIFACT-FALSE-PREMISE-AUDIT-PIN** (deadline 2026-07-15)
- **VECTOR-SEARCH-CONFIG-FALSE-PREMISE-AUDIT-PIN** (deadline 2026-07-15)
- **DEPRECATIONS-ALREADY-ARCHIVED-PIN** (deadline 2026-07-15) — if the deprecations.yaml archive rule was already shipped by another PR
- **FULLIMAGES-DEEPTHINK-REVALIDATE** (deadline 2026-07-22) — re-spawn thinker if any pre-PR fiber drifted

### §7.1 — Post-wave forward-pointers

- **PR-LEGACY-CLEANUP-HOTSPOT-CROSSREF** (deadline 2026-08-15) — post-wave git-log frequency cross-validation per slim-schema ratchet (mirrors `PR-CLEANUP-HOTSPOT-CROSSREF-2026-07-09` precedent commit `ab7042f0`)
- **PR-ANIMATE-CONSUMER-CLEANUP** (deadline 2026-08-22) — follow-up wave migrating any remaining caller of `AnimateRequest` after Item 1 deletion
- **PR-FULLIMAGES-DOWNSTREAM-MIGRATION** (deadline 2026-08-22) — operator-facing follow-up migrating operator callers from the conceptual `/generate-video` endpoint to `/generate-image` per Option B verdict
- **PR-IMAGE-MAGICK-SUBPATH-CASCADE-CHECK** (deadline 2026-09-01) — verify the canonical `delivery.Publisher` path-builder surfaces used by `generateOneImage` propagate the typed image URL/subpath set transparently across Image/Promo destinations

---

## §8 — Honest Scope-Lock (godlike/07 minimum-blast-radius)

- **In-scope**: 5 atomic surgical commits (one per item), each auto-sufficient per AGENTS.md Pattern 5; 3-surface godlike/06 SSOT lockstep per CANONICAL.md §1; verbatim pre-flight + race-protect + bonus audit-pin for any FALSE §0 item
- **Out-of-scope**: rewrite of unrelated canonical surfaces (e.g., the `generation_plan_builder.go` slated for canonical-processor-name unification lives on a different wave-tracker slot per `architecture/waves/wave_p1_high.yaml#POSTPROCESSOR-UNIFICATION-2026-07-08`)
- **Pre-existing 6-item voiceover + app build-issue carry-forward per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` UNCHANGED** — NOT a regression of any item's commit
- **`architecture/waves/wave_p1_high.yaml` wave-tracker slot flip DEFERRED** per `PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward (forward-pointer `PR-CURRENT-YAML-PARSE-FIX-PART-N`, deadline 2026-08-15)
- **Race-condition discipline (AGENTS.md Git-Lesson-4 + Git-Lesson-5)**: every push checks `git fetch && git log --oneline HEAD..@{u}` MUST be empty pre-push. If parallel agent lands byte-equivalent work during the commit-to-push window, accept the canonical SHAs on `origin/main` without force-push (the canonical-coordination signal, not a contest).

---

## §9 — Cross-References (umbrella)

- **Predecessors**:
  - `architecture/action-plans/2026-07-10-legacy-cleanup-3-commit.md` — same 5 items, 3-commit condensed topology (subsumed by this plan)
  - AGENTS.md `## Recent cross-cutting closures` entry for `AUDIT_PIN_DEAD_CODE_PURGE-2026-07-25` (commit `5a32611d`) — canonical orphan-chain-retirement precedent
  - AGENTS.md entry for `IMAGES-LEGACY-CLEANUP-2026-07-06` wave — canonical pattern for `/api/images/` route retirement + package moves
- **Siblings** (per §7 forward-pointers):
  - `architecture/action-plans/2026-07-08-script-pipeline-contract.md` (SCRIPT-PARENT-DECOUPLING)
  - `architecture/action-plans/2026-07-09-script-pipeline-decoupling.md` (DECOUPLING follow-up)
  - `architecture/action-plans/2026-07-09-logic-simplification-dead-code-action-plan.md` (dead-code 8-PR wave)

---

## §10 — Lifecycle Audit-Trail

```yaml
id: PR-LEGACY-CLEANUP-5-ITEM-ORCHESTRATION-2026-07-10
status: pending
ships: 5 atomic commits on origin/main (one per item)
yields_zero_new_hotspots: ENFORCED via §7.1 PR-LEGACY-CLEANUP-HOTSPOT-CROSSREF
wave_flip_criterion: §6 all 8 conditions met
godlike_07_NO_FAKE_AVAILABILITY: §0.2 is the load-bearing live-truth gate
godlike_06_SSOT: per-item §SSOT canonical declarations (3 surfaces per commit)
locked_3_surface: CHANGELOG.md + AGENTS.md + canonical ship_sha on origin/main per commit
carry_forward: architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04 (DEFERRED until parse carry-forward resolves)
race_protect: git fetch && git log --oneline HEAD..@{u} MUST return empty pre-push (no --force ever)
direct_to_main: 5 atomic commits on main per AGENTS.md Git-Lesson-2 (no branches, no --no-ff, no --force)
Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>  # per AGENTS.md Git-Lesson-3 trailer convention
```

---

**End of action plan. Execute each item per §1.3 → §5.5 with race-protect discipline (AGENTS.md Git-Lesson-4/5) + 3-surface SSOT lockstep per CANONICAL.md §1.**
