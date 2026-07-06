# IMAGES-LEGACY-CLEANUP-2026-07-06 — Action Plan

> **Authority**: this file is the canonical narrative companion to the wave-tracker entry
> `architecture/current.yaml#IMAGES-LEGACY-CLEANUP-2026-07-06`. Updates to scope / IDs / bands
> must keep the wave-tracker anchor + this file + `CHANGELOG.md ## Unreleased → ### Documentation`
> + `AGENTS.md ## Recent cross-cutting closures` in lockstep per **godlike/06 SSOT** (one-canonical-
> owner-per-fact).

---

## §0 — Context

Italian audit (2026-07-06 user-pasted) enumerated **10 legacy / ambiguous surfaces in the
images subsystem** with 2 priority bands:

- **Band A (immediate)** — `/api/images/upload` silent-fallback to `SearchAndDownload` (semantic
  disaster: a `POST /upload` returns a `SearchAndDownload` response when ingest is unwired).
- **Band A (P0 absolute)** — `/api/images/animate` is 501-only (fake-availability surface),
  `/api/images/generate` collides with `/api/images/generated/generate`, generated-search
  accepts arbitrary `?origin` (breaking territory separation), `google-vids` engine silently
  translates to ken-burns (silent-success).

Per **godlike/07 NO-FAKE-AVAILABILITY** (the load-bearing invariant of this wave):
> Every endpoint / sentinel / fallback that does not do what its name suggests is **fail-closed**
> or **explicitly 4xx/5xx** — never 200 OK with empty payload, never silent-context-switch.

Per **godlike/06 SSOT one-canonical-owner-per-fact**: each of the 10 audit points has exactly
one canonical surface to retire AND exactly one canonical replacement (or justification for
retention).

---

## §1 — State at Wave Registration (2026-07-06, post-ff)

The user's 10-item audit was pasted after **two recent PRs** (already on `origin/main`)
landed file-decomposition refactors that address SOME items:

| Item | Already Addressed? | Evidence on disk (post-ff / commit `c236d6e0` → `4d70d024`) |
|---|---|---|
| 1 — `/api/images/generate` legacy-compat collision | **YES (partial)** | `internal/api/images/legacy_generate_handler.go` (legacy seam) coexists with `generated_generate_handler.go` (canonical). Both wired; deprecation TODOs remain. |
| 2 — `/api/images/animate` 501-only | **YES** | `internal/api/images/animate_handler.go` returns 501 explicitly with typed message. |
| 3 — Upload silent-fallback to `SearchAndDownload` | **NO — REAL P0** | `internal/api/images/upload_handler.go:51-65` falls back via `h.service.SearchAndDownload(…)` if `h.ingestSvc == nil`. **Fake-availability.** |
| 4 — generated-search accepts arbitrary `?origin` | **NO — REAL P0** | `internal/api/images/generated_search_handler.go:104`: `origin := c.DefaultQuery("origin", string(domain.ImageOriginGenerated))`. Territory violation. |
| 5 — Stale "forward-pointer returns empty" docstring | **PARTIAL** | `generated_search_handler.go` doc has been cleaned up; repo-wide grep needed. |
| 6 — `ErrUnsupportedModel` orphaned sentinel | **NO — REAL** | `internal/application/images/generated/provider_registry.go:88` still declares the sentinel "as audit-pin"; zero live callers but the var exists. |
| 7 — `google-vids` silent-fallback to ken-burns | **NO — REAL** | `internal/api/images/handler_full.go:81` says "Sections requesting engine='google-vids' will fall through to the ken-burns path" — silent-success semantic. |
| 8 — `/webhook/remote` audit-pin comments in runtime files | **NO — REAL** | `internal/api/images/handler.go:64` + `handler_full.go:9` retain multi-line retirement comments that belong in `docs/archive/`. |
| 9 — `GenerateImageRequest` + `GeneratedGenerateRequest` DTO duplication | **NO — REAL** | Two near-identical structs: `internal/api/images/request_types.go::GenerateImageRequest` + `internal/api/images/generated_generate_handler.go::GeneratedGenerateRequest`. Field-level identical. |
| 10 — `FullImagesHandler` separate from `/images/*` | **PARTIAL** | `internal/api/images/handler_full.go` lives INSIDE the images package, mixing the full-images (`/images/video/generate`) bounded context with the canonical image surfaces. Documented as sibling but should move back to `internal/api/fullimages/`. |

Net: **3 items addressed** (1, 2, 5-partial), **7 items REAL PENDING** (3, 4, 5-remaining, 6, 7,
8, 9, 10). The wave-tracker entry classifies each.

---

## §2 — Per-PR Execution Plan (one PR per Item, EXCEPT 6+8 bundled)

Each PR lands **directly on `main`** per AGENTS.md **Git-Lesson-2** (NO branches, NO `--force`,
NO PR). Race-protect via `git fetch && git log --oneline HEAD..@{u}` before every push.
Co-authored-by trailer per **Git-Lesson-3**.

### PR-IMG-LEGACY-1 (Items 6 + 8 — dead-code & stale-doc eviction)

**Goal**: physically remove orphaned audit-pin surfaces.

**Scope**:
- `internal/application/images/generated/provider_registry.go` — REMOVE `var ErrUnsupportedModel`
  declaration (currently zero live callers; sentinel only exists for godlike/07 "no silent
  resurrection" discipline but a typed grep would prove no future caller ever needs it).
- `internal/api/images/handler.go:62-69` — REPLACE the 7-line `/webhook/remote` retirement
  comment with a single-line pointer: `// /webhook/remote retired; see docs/archive/image-legacy.md`.
- `internal/api/images/handler_full.go:7-10` — SAME single-line replacement on the package header.
- New file `docs/archive/image-legacy.md` (canonical home for retirement narrative).

**Phase**: CONTRACT (physical git-rm of sentinel + comment-only reduction). Per godlike/07,
the `ErrUnsupportedModel` audit-pin strategy is satisfied by the typed-error generation surface
(`ErrProviderUnavailable`) that already exists.

**Verification gates** (`internal/api/images/` + `internal/application/images/generated/`):
```bash
gofmt -l internal/api/images/ internal/application/images/generated/   # exit 0
go vet ./internal/api/images/... ./internal/application/images/generated/...  # exit 0
go build ./internal/api/images/... ./internal/application/images/generated/...  # exit 0
git grep ErrUnsupportedModel internal/         # 0 live hits (archive/ docs exempt)
git grep "/webhook/remote" internal/api/images/  # only the 1-line pointer remains
```

**Deadline**: 2026-08-01.

---

### PR-IMG-LEGACY-2 (Item 3 — Upload fake-availability purge)

**Goal**: replace the silent fallback with explicit fail-closed.

**Scope**: `internal/api/images/upload_handler.go:51-72`.

**Behavior change**:
- `h.ingestSvc == nil && req.URL != ""`  →  `apiutil.Error(c, http.StatusServiceUnavailable,
  "image upload requires the ingest service; /upload cannot fallback to search")`.
- Remove the `SearchAndDownload` call entirely.
- Keep the existing valid path (`h.ingestSvc != nil && req.URL != ""` → `Ingest`).

**godlike/07 rationale**: silent fallback from `upload` (one domain) to `SearchAndDownload`
(a search-and-download domain) is a **cross-domain silent semantic switch**: callers posting
JSON `{image_url}` expect an upload to happen, but receive a search-then-download result with
different semantics. The fallback WAS a route-disguise that returned 200 with the wrong shape.

**Phase**: EXPAND (typed sentinel + 503 contract).

**Verification gates**:
```bash
# New TDD test: Upload_NoIngestSvc_Returns503
go test -short -count=1 -run '^TestUpload_' ./internal/api/images/...

# Existing upload happy-path tests still PASS (ingestSvc wired)
go test -short -count=1 -run '^TestUpload.*IngestSuccess' ./internal/api/images/...   # PASS
```

**Deadline**: 2026-08-08.

---

### PR-IMG-LEGACY-3 (Items 4 + 5 — strict territory enclosure)

**Goal**: enforce `=generated` invariant on `/api/images/generated/search` + sweep stale
"forward-pointer returns empty" documentation.

**Scope**:
- `internal/api/images/generated_search_handler.go:104` — REMOVE `c.DefaultQuery("origin", ...)`.
  Hardcode `domain.ImageOriginGenerated` in `ListImagesByOrigin` call.
- `internal/api/images/all_territories_handler.go` — same `origin` param removal if present.
- New TDD test: `TestGeneratedSearch_OriginParamIgnored_ReturnsGeneratedRows`
  (verifies `?origin=garbage` is ignored, rows are still `generated`-origin).
- Repo-wide grep cleanup:  `rg "Step[- ]?9 forward-pointer|forward-pointer.*empty|return.*200.*\[\]"
  internal/api/images/ documents/`  → 1-line replacement per file or move to docs/archive/.

**godlike/07 rationale**: each generated-territory read seam has **one canonical
identity** (the URL path `/generated/search`). Allowing callers to override via `?origin`
breaks territory separation — a caller can probe `?origin=retrieved` and corrupt the
generated enum invariant. Hardcoding makes the semantic explicit.

**Phase**: CONTRACT (param removal) + documentation-sweep EXPAND (repo-wide grep).

**Verification gates**:
```bash
# Origin param ignored (graceful acceptance of junk param)
go test -short -count=1 -run '^TestGeneratedSearch_OriginParam' ./internal/api/images/...

# Stale doc cleanup: zero hits of the canonical phrases
rg "Step[- ]?9 forward-pointer|forward-pointer.*returns empty" internal/api/images/ internal/application/images/   # 0 hits
```

**Deadline**: 2026-08-08.

---

### PR-IMG-LEGACY-4 (Item 7 — google-vids strict rejection)

**Goal**: refuse `engine="google-vids"` explicitly (400 or 410), document removal target.

**Scope**: `internal/api/images/handler_full.go:80-83`.

**Behavior change**:
- Replace the silent-fallthrough comment with a pre-generation check:
  `for _, sec := range req.Sections { if sec.Engine == "google-vids" { apiutil.BadRequest(c,
  "engine=google-vids retired; use ken-burns or ai-image-N explicitly"); return } }`
- Optionally: gate the same pre-condition at `internal/app/module_media_ingest.go:163` case
  branch (canonical ingest source switch) — flip the `case` to return a typed error
  `ErrEngineRetired` rather than falling through to the AI mark-up branch.

**godlike/07 rationale**: silent fallthrough means callers requesting Google Vids receive
ken-burns results labeled "google-vids" — the response shape is **wrong AND lies about the
provenance**. Strict 400 reveals the breakage; tunable callers downgrade explicitly.

**Phase**: EXPAND (strict refusal).

**Verification gates**:
```bash
# New TDD: TestGenerateFullImages_GoogleVidsEngine_ReturnsBadRequest
go test -short -count=1 -run '^TestGenerateFullImages' ./internal/api/images/...

# No silent fallthrough
rg "Sections requesting engine=\"google-vids\"" internal/api/images/   # 0 hits
```

**Deadline**: 2026-08-15.

---

### PR-IMG-LEGACY-5 (Item 9 — SSOT DTO unification)

**Goal**: collapse `GenerateImageRequest` + `GeneratedGenerateRequest` into one canonical DTO
that both routes (`/generate` legacy + `/generated/generate` canonical) decode.

**Scope**:
- Move `internal/api/images/generated_generate_handler.go::GeneratedGenerateRequest` to
  `internal/api/images/request_types.go` (canonical home).
- Rename to `type ImageGenerationRequest struct { ... }` (neutral name).
- `legacy_generate_handler.go::Generate` + `generated_generate_handler.go::GeneratedGenerate`
  both bind into `ImageGenerationRequest`. Per godlike/06, this is the SSOT.
- Keep the legacy route path stable (the route is at 410-Gone territory once
  PR-script-legacy-contract (CLEANUP-PRIORITY wave) flips it).

**Phase**: CUTOVER (final form: 1 DTO, 2 callers).

**Verification gates**:
```bash
# Both routes bind same DTO
go test -short -count=1 -run '^Test.*Generate.*BindJSON' ./internal/api/images/...   # PASS

# Single SSOT
git grep "type GeneratedGenerateRequest\|type GenerateImageRequest" internal/   # 0 hits (rewritten as ImageGenerationRequest)
rg "^type ImageGenerationRequest" internal/api/images/request_types.go   # 1 hit
```

**Deadline**: 2026-08-22.

---

### PR-IMG-LEGACY-6 (Item 10 — FullImagesHandler package evacuation)

**Goal**: move `/images/video/generate` to its own bounded context `internal/api/fullimages/`
(one canonical owner per fact: full images = different domain from image surfaces).

**Scope**:
- Move `internal/api/images/handler_full.go` content to `internal/api/fullimages/handler.go`
  (new package).
- Move `FullImagesHandler` mount out of `internal/app/bundle_types.go::BundleTypes.Handler`.
- Update `internal/app/assets_register_fullimages.go` (new file, estimated, OR canonical
  `module.go` equivalent) to wire the bundle.
- Optionally rename endpoint `/api/images/video/generate` → `/api/fullimages/generate`
  (audit-pin: backend API stability vs internal-package SSOT; default = NO rename, flag
  `deprecate_endpoint=true` opens forward-pointer to canonical rename).

**Phase**: CUTOVER (package relocation; route is identical).

**Verification gates**:
```bash
# Handler in canonical package
ls internal/api/fullimages/handler.go   # 1 file present
ls internal/api/images/handler_full.go  # 0 file present

# Wire chain
go test -short -count=1 -run '^TestFullImages' ./internal/api/fullimages/...
go vet ./internal/app/...  # exit 0 (build_bundles_domain.go updated)
```

**Deadline**: 2026-08-22.

---

## §3 — Wave-Tracker Entry (Slim-Shape)

```yaml
id: IMAGES-LEGACY-CLEANUP-2026-07-06
owner_capability: internal/api/images + internal/application/images + internal/api/fullimages
status: in_progress
deadline: 2026-08-22
linked_issues:
  - id: PR-IMG-LEGACY-1
    owner_capability: internal/api/images + internal/application/images/generated
    status: pending
    deadline: 2026-08-01
    phase: CONTRACT
  - id: PR-IMG-LEGACY-2
    owner_capability: internal/api/images
    status: pending
    deadline: 2026-08-08
    phase: EXPAND
  - id: PR-IMG-LEGACY-3
    owner_capability: internal/api/images
    status: pending
    deadline: 2026-08-08
    phase: CONTRACT
  - id: PR-IMG-LEGACY-4
    owner_capability: internal/api/images + internal/app
    status: pending
    deadline: 2026-08-15
    phase: EXPAND
  - id: PR-IMG-LEGACY-5
    owner_capability: internal/api/images
    status: pending
    deadline: 2026-08-22
    phase: CUTOVER
  - id: PR-IMG-LEGACY-6
    owner_capability: internal/api/fullimages
    status: pending
    deadline: 2026-08-22
    phase: CUTOVER
```

**Slim-shape rationale**: each `linked_issues` slot is `{id + owner_capability + status +
deadline + phase}` only. No `description` field (per godlike/06 SSOT one-canonical-owner-per-fact;
canonical narrative lives in this action plan file).

---

## §4 — Execution Order (with Dependencies)

1. **PR-IMG-LEGACY-1** (Items 6 + 8) — Pure dead-code eviction. **Independent of all others.**
   Land first.
2. **PR-IMG-LEGACY-2** (Item 3 — Upload 503) — Touches `upload_handler.go` exclusively.
   **Independent.**
3. **PR-IMG-LEGACY-3** (Items 4 + 5 — strict territory + doc sweep) — Touches
   `generated_search_handler.go` + repo-wide docs. **Independent.**
4. **PR-IMG-LEGACY-4** (Item 7 — google-vids strict) — Touches `handler_full.go` +
   `module_media_ingest.go`. **Independent** but a forward-pointer for PR-6 (fullimages
   package move reads `handler_full.go` source).
5. **PR-IMG-LEGACY-5** (Item 9 — DTO unification) — Touches `request_types.go` +
   `legacy_generate_handler.go` + `generated_generate_handler.go`. **Requires PR-1 (dead-
   code) to settle** so the unified DTO doesn't inherit audit-pin comments.
6. **PR-IMG-LEGACY-6** (Item 10 — fullimages move) — Touches `handler_full.go::FullImagesHandler`
   move to `internal/api/fullimages/`. **Requires PR-4** (google-vids strict resolution) so
   the moved file's behavior is locked before transport.

**Parallelisable**: PR-1 + PR-2 + PR-3 do not overlap files → safe to land in any order.
PR-4 + PR-5 + PR-6 form the dependency chain.

---

## §5 — Honest Scope-Lock (godlike/07)

1. **Items 1 + 2 are not formal PRs of this wave** — they were addressed by PR-IMG-SPLIT-1/2
   that landed on origin/main BEFORE the user's audit (commits `c236d6e0` → `4d70d024`).
   The wave-tracker entry DOES NOT need to retroactively reopen them; the audit notes them
   as addressed.
2. **Item 5 is partially addressed** by the post-ff cleanup in `generated_search_handler.go`.
   This wave's PR-3 performs the residual repo-wide `rg` sweep.
3. **Item 10 (FullImagesHandler) is a forwarding question**, not just a clean-up. The MVP
   outcome is "move the file"; the perfect outcome is "audit the bounded context + decide
   route stability". PR-6 lands the file move; a future PR (`PR-FULLIMAGES-DOMAIN-EVAL`)
   audits the route-name stability post-move.
4. **NO production code change in this commit** — `architecture/action-plans/2026-07-06-images-
   legacy-cleanup.md` is documentation-only. Per-file PRs land directly on `main` per
   AGENTS.md §Git-Lesson-2.
5. **Pre-existing 5-item voiceover + app build-issue carry-forward** per
   `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` is **unchanged** — NOT
   regressions of any PR-IMG-LEGACY-N commit.
6. **NO `t.Skip` markers** in any new TDD test (per PR-PERSIST-6-CANONICAL precedent + Active
   Concerns #10 fix — `t.Skip` is the godlike/07 fake-availability equivalent for tests).

---

## §6 — Golden Rule (Immutable, per audit)

```
generated = AI-created images
retrieved = found/downloaded/ingested images from normal sources
all       = aggregator only, NEVER owns business logic
legacy    = old endpoints kept for compat, NEVER used as new architecture
fullimages = section-wise video pipeline (distinct bounded context from images)
```

Future surface must conform. PR-3 enforces generated; PR-6 enforces fullimages separation;
PR-5 enforces the canonical DTO; PR-2 enforces upload integrity.

---

## §7 — Cross-References (3-Surface Lockstep per godlike/06 SSOT)

- `architecture/current.yaml#IMAGES-LEGACY-CLEANUP-2026-07-06` (wave-tracker anchor)
- `architecture/action-plans/2026-07-06-images-legacy-cleanup.md` (this file — canonical narrative)
- `CHANGELOG.md ## Unreleased → ### Added` (closure meta-entry after each per-PR lands)
- `AGENTS.md ## Recent cross-cutting closures` (mini-mirror entry per per-PR)
- Sister action plans (style precedent):
  - `architecture/action-plans/2026-07-06-images-territory-split.md` (precedent for file-state)
  - `architecture/action-plans/2026-07-25-cleanup-priority-1-5.md` (precedent for "burst wave" structure)
  - `architecture/action-plans/2026-07-05-voiceover-completion-action-plan.md` (precedent for godlike/06/07 discipline)
- AGENTS.md §Git-Lesson-2 (direct-to-main, no branches, no `--force`)
- AGENTS.md §Git-Lesson-3 (Co-authored-by trailer convention)
- AGENTS.md §Git-Lesson-4 + §Git-Lesson-5 (race-protect: fetch + byte-equivalent-replay acceptance)
- AGENTS.md §Pattern 8 (API package: thin transport only — godlike/07 rationale for PR-2 strict 503)

---

## §8 — Per-PR Commit Convention

```bash
git -c user.email='marcuss-ops@example.com' \
    -c user.name='Marcuss-ops' \
    add <touched_files>
git commit -m "refactor(images): PR-IMG-LEGACY-N — <scope>

- <bullet 1>
- <bullet 2>
- <bullet 3>

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>"

# Race-protect before push (per AGENTS.md §Git-Lesson-4)
git fetch origin
git log --oneline HEAD..@{u}    # must be empty for safe ff-push
                                   # if non-empty → rebase + ff-push
                                   # if non-empty with same subject → byte-equivalent-replay recover (Git-Lesson-5)

git push origin main
```

---

## §9 — Verification Surface (all per-PR gates)

```bash
# Per-PR, on its own subtree:
gofmt -l <touched_files>                                      # exit 0
go vet ./<touched_subtree>/...                                 # exit 0
go build ./<touched_subtree>/...                               # exit 0
go test -short -count=1 ./<touched_subtree>/...                # PASS
go test -short -count=1 -run '^Test<NewContract>' ./...        # PASS (per-PR typed-sentinel test)
```

The post-wave flip of `IMAGES-LEGACY-CLEANUP-2026-07-06.exit_signal = true` happens ONLY when
ALL 6 `linked_issues` are `status: shipped` AND the per-PR verification gates are green.

---

## §10 — Sign-Off

This action plan was derived from a user-pasted 10-item Italian audit on 2026-07-06 and
validated against the post-`PR-IMG-SPLIT-2` file state (commits `c236d6e0` → `4d70d024` on
`origin/main`). The per-PR scope was cross-validated by `thinker-with-files-gemini` against
godlike/06 SSOT + godlike/07 NO-FAKE-AVAILABILITY discipline.

Wave-flip author: PipelineGen Agent (`agent@pipelinegen.local`)
Canonical surface: this file
Audit-trail context: AGENTS.md `## Recent cross-cutting closures` (will mirror each per-PR).

Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>
