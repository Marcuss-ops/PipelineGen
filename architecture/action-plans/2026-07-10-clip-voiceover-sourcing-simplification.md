# CLIP-VOICEOVER-SOURCING-SIMPLIFICATION — Action Plan

**Date**: 2026-07-10
**Status**: PLANNING — docs lockstep only; per-PR implementation lands incrementally
**Owner capability**: `internal/application/clips` + `internal/application/voiceover` + `internal/application/assets/sourcing`
**Wave-tracker anchor**: `architecture/waves/wave_p1_high.yaml#CLIPS-VOICEOVER-SOURCING-ALIASES-2026-07-10`
  (DEFERRED per `PRE-EXISTING-YAML-PARSE-2026-07-04` carry-forward + `PR-CURRENT-YAML-PARSE-FIX-PART-6` forward-pointer, deadline 2026-08-15)

---

## §0 — Honest status snapshot

**6 anti-patterns identified** (verbatim from user spec, items 13-18):

| # | Anti-pattern | Canonical location today | Production-cost (audit) |
|---|--------------|--------------------------|-------------------------|
| 13 | `SourceResolver.ResolveRepo(source)` ignora il parametro e restituisce sempre lo stesso repository | `internal/app/clips_adapters_index.go:131` (`sourceResolverAdapter`) + `internal/app/clips_adapters_ops.go:81` (`clipOpsSourceResolverAdapter`) | 2 production adapters both ignore `source`; 1 caller in `clip_ops_reconcile.go:60` + caller in `clip_ops_types.go:171` |
| 14 | `newClipsAdapterBundle` ha parametri placeholder (`log`, `any "vectorSvc removed"`, 3 repo uguali) | `internal/app/clips_adapters_index.go:237`; called from `internal/app/wire_assets_clips.go:187` | 11-arg constructor; ~40% dei parametri è dead code |
| 15 | `voiceover/groups_resolver.go` contiene solo alias deprecati (GroupsResolver, GroupEntry, ErrGroupNotFound, NewGroupsResolver) | `internal/application/voiceover/groups_resolver.go` (whole file) | 4 alias symbols; `destination.Resolver` already canonical |
| 16 | `voiceover/types.go` mantiene alias `VoiceoverRepository = persistence.Repository` + `VoiceoverRecord = persistence.VoiceoverRecord` + `PromoRequest/Result/Response/DefaultPromoLanguages` | `internal/application/voiceover/types.go` | 6 alias symbols; `workflow/promo` is canonical |
| 17 | `FileIsNotTrashed` + `FileExists` classificano l'errore Drive 404 con `strings.Contains(err.Error(), "404")` / `"notFound"` (anti-pattern già eliminato per SQLite) | `internal/infrastructure/drive/uploader_file.go` (FileIsNotTrashed + FileExists) | text-parsing classification error — same anti-pattern godlike/07 forbidden |
| 18 | `UpdateCumulativeJSON` + `sourcingEnrichmentAdapter.EnrichAndIndex` ritornano `nil` anche quando non fanno nulla | `internal/application/assets/sourcing/ports.go::UpdateCumulativeJSON` + adapter | silent-success class — caller cannot distinguish "ran" / "no-op" / "failed" |

**Cross-cutting observation**: tutti i 6 punti appartengono alla categoria **godlike/07 "silent-success + alias indirection"** — la rimozione diretta è meccanica; il valore aggiunto è la fail-typed + canonical owner per fact.

---

## §1 — Goal

Rimuovere le 6 fonti di silent-success e indirection indiretta, lasciando un'unica superficie canonica per ogni concern. Nessuna regressione funzionale; nessun cambio al wire-shape esterno. **6 PR atomici** (uno per anti-pattern), ciascuno con la sua per-PR migration sequence.

**Risultato finale** (post wave-flip):

1. **#13**: `ClipRepositoryPort` consumato direttamente; `SourceResolverPort` cancellato; nessuna duplicazione di repository.
2. **#14**: `buildClipOpsPorts(...)` strict constructor (solo i campi consumati da `ClipOpsService`); niente `_ = log` placeholder.
3. **#15**: Sostituzione integrale di `voiceover.GroupsResolver` con `destination.Resolver`; file `groups_resolver.go` git-rm'd.
4. **#16**: Sostituzione di `voiceover.VoiceoverRepository` con `persistence.Repository`; sostituzione dei 4 promo types con `workflow/promo.*`; `voiceover/types.go` git-rm'd.
5. **#17**: `errors.As(err, *googleapi.Error)` + `Code == http.StatusNotFound`; classification centralizzata in helper `internal/infrastructure/drive/errors.go`.
6. **#18**: Tutti gli adapter silent-success ritornano typed-sentinel (o fail-closed composition-time); impossible per il caller di scambiare "ran" con "no-op".

---

## §2 — Per-PR migration sequence (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT)

### §2.1 — `PR-CLIPS-DAPTER-RESOLVER-RETIRE` (#13, P0, deadline 2026-07-15)

**Target**: `SourceResolverPort` rimosso dal application-layer; `ClipRepositoryPort` usato direttamente dal consumer.

**Surface (3 files)**:

1. `internal/application/clips/ports.go` — REMOVE:
   ```go
   type SourceResolverPort interface {
       ResolveRepo(source string) ClipRepositoryPort
   }
   ```
   Conserva solo `ClipRepositoryPort` (canonical SOLE owner per godlike/06 SSOT one-canonical-owner-per-fact).

2. `internal/application/clips/clip_ops_types.go` — REPLACE:
   ```go
   // PRE:
   type ClipOpsServiceDeps struct {
       sourceResolver SourceResolverPort  // ← elimina
       ...
   }
   func (s *ClipOpsService) repoFor(src string) ClipRepositoryPort {
       if s.sourceResolver == nil { return nil }
       return s.sourceResolver.ResolveRepo(src)
   }
   // POST:
   type ClipOpsServiceDeps struct {
       clipRepo ClipRepositoryPort  // canonical SOLE repository per source filter
       ...
   }
   ```
   Il filtro per `source` resta — ma è una query parameter, non un repository swap:
   ```go
   s.clipRepo.Search(ctx, SearchQuery{Source: src, ...})
   ```

3. `internal/app/clips_adapters_index.go` + `internal/app/clips_adapters_ops.go`:
   - REMOVE `sourceResolverAdapter` + `clipOpsSourceResolverAdapter` (2 struct) + 2 `var _` pins
   - KEEP single `clipRepoAdapter` wiring il `*assets.ClipsRepository` al `ClipRepositoryPort` interface (canonical, already exists in adapter.go)

**Tests** (in NEW `internal/application/clips/clip_ops_resolver_retire_test.go`):
- `TestClipOps_DirectRepo_FilterBySourceQuery`
- `TestClipOps_NoSourceResolverField`
- Compile-time assertions: `var _ ports.ClipRepositoryPort = (*RepoAdapter)(nil)` (locks canonical owner)

**Verification gates**:
- `gofmt -l` clean
- `go vet ./internal/application/clips/... ./internal/app/...` exit 0
- `rg "SourceResolverPort" internal/` returns 0 production-code hits
- `go test -short -count=1 ./internal/application/clips/...` PASS

**godlike/07 minimum-blast-radius**: zero signature change on the public API; the `ResolveRepo` switch becomes a per-call `Source: src` filter on the same physical repo.

---

### §2.2 — `PR-CLIPS-DAPTER-BUNDLE-SLIM` (#14, P0, deadline 2026-07-15)

**Target**: `newClipsAdapterBundle` rimosso; costruttore stretto `buildClipOpsPorts(...)` con SOLO i campi consumati da `ClipOpsService`.

**Surface (3 files)**:

1. `internal/app/clips_adapters_index.go` — REMOVE 11-arg `newClipsAdapterBundle(...)`; REPLACE with strict:
   ```go
   func buildClipOpsPorts(clipRepo clips.ClipRepositoryPort, jobs jobsAdapter) *clips.ClipOpsServiceDeps {
       return &clips.ClipOpsServiceDeps{
           ClipRepo: clipRepo,  // canonical name (post-§2.1)
           Jobs:     jobs,
       }
   }
   ```
   Compile-time pin `var _ clips.PortDepsShape = (*clips.ClipOpsServiceDeps)(nil)`.

2. `internal/app/wire_assets_clips.go:187` — REPLACE call:
   ```go
   // PRE:
   clipsOpsPorts := newClipsAdapterBundle(
       clipsAdapters.Count,     // unused: clip ops never counts
       nil,                     // vectorSvc removed PG-034
       clipsRepo, clipsRepo, clipsRepo,  // 3 times same repo
       cfg, _ = log, idx, hash, tree,   // multiple dead-code args
   )
   // POST:
   clipsSvc := appclips.NewClipOpsService(clipRepo, jobsAdapter, zap.NewNop())
   // passthrough to internal/app/wire_assets_clips.go::(b) directly
   ```

3. (Optional, post-§2.1 merge) `internal/app/build_bundles_clips.go` — REMOVE 30-line placeholder block.

**Tests** (in NEW `internal/app/wire_assets_clips_test.go`):
- `TestBuildClipOpsPorts_StrictSignature` — assert ctor rejects any non-typed params (Go compile-time check via signature change)
- `TestBuildClipOpsPorts_DelegationContract` — assert wiring preserves the canonical `ClipOpsServiceDeps` shape

**Verification gates**:
- `gofmt -l` clean
- `go vet ./internal/app/... ./internal/application/clips/...` exit 0
- `rg "vectorSvc removed" internal/` returns 0 hits
- `rg "_ = log" internal/app/clips_adapters_index.go` returns 0 hits
- `go build ./cmd/server/` clean (full composer still compiles)

**godlike/07 minimum-blast-radius**: net -22 LoC in `clips_adapters_index.go`; 5 fewer dead-code args at the call site in `wire_assets_clips.go`.

---

### §2.3 — `PR-VOICEOVER-GROUPSRESOLVER-RETIRE` (#15, P0, deadline 2026-07-22)

**Target**: `voiceover.GroupsResolver` sostituito integralmente da `destination.Resolver`; file `voiceover/groups_resolver.go` git-rm'd.

**Surface (2 files) + git-rm**:

1. `internal/application/voiceover/groups_resolver.go` — git-rm intero file (4 alias symbols).
2. Tutti i callers (migrazione a `destination.Resolver`):
   ```bash
   rg "voiceover\.GroupsResolver|voiceover\.GroupEntry|voiceover\.ErrGroupNotFound|voiceover\.NewGroupsResolver" internal/
   ```
   La migrazione per-call site:
   ```go
   // PRE:
   gr := voiceover.NewGroupsResolver(repo, clamp, &errGroupNotFound)
   entry, err := gr.Get(ctx, key)
   // POST:
   res := destination.NewResolver(repo)
   entry, err := res.Lookup(ctx, key)  // returns destination.ErrNotFound on miss
   ```

**Canonical SOLE owners** (per godlike/06 SSOT):
- `destination.Resolver` lives ONLY at `internal/application/assets/destination/resolver.go`
- `destination.ErrNotFound` lives ONLY at the same file
- `voiceover/groups_resolver.go` is REMOVED (no replacement needed)

**Pre-write caller audit** (rg-verified): the canonical callers are in `internal/application/scripts/usecase/flow_helpers_artlist.go` (1 import) + `internal/api/script/handler_facade.go` (1 typed port usage). Migration is mechanical: replace `voiceover.GroupsResolver` with `destination.Resolver` AT ALL call-sites, delete the file.

**Tests** (extend existing test files):
- `internal/application/assets/destination/resolver_test.go` already covers the substitution semantics; no NEW tests needed (godlike/07 minimum-blast-radius — surface contract preserves).
- Add 1 integration test in NEW `internal/application/voiceover/groups_resolver_retire_test.go` asserting `rg "voiceover\.GroupsResolver" internal/` returns 0 production-code hits post-merge (forward-prevention).

**Verification gates**:
- `gofmt -l` clean
- `git rm internal/application/voiceover/groups_resolver.go` succeeds
- `go vet ./internal/application/voiceover/... ./internal/application/assets/destination/...` exit 0
- `rg "voiceover\.GroupsResolver\|voiceover\.GroupEntry\|voiceover\.ErrGroupNotFound\|voiceover\.NewGroupsResolver" internal/` returns 0 hits
- Full `go test -short ./internal/application/voiceover/...` PASS

**godlike/07 minimum-blast-radius**: pure code-motion; one file deleted; ~5 call-sites updated. ZERO signature change on the canonical `destination.Resolver`.

---

### §2.4 — `PR-VOICEOVER-ALIASES-RETIRE` (#16, P1, deadline 2026-07-29)

**Target**: 2 alias repo (`VoiceoverRepository`, `VoiceoverRecord`) + 4 alias promo (`PromoRequest`, `PromoResult`, `PromoResponse`, `DefaultPromoLanguages`) rimossi; canonical direct references.

**Surface (2 files)**:

1. `internal/application/voiceover/types.go` — REMOVE alias block:
   ```go
   // PRE (deprecated):
   type VoiceoverRepository = persistence.Repository
   type VoiceoverRecord = persistence.VoiceoverRecord
   type PromoRequest = workflowPromo.Request
   type PromoResult = workflowPromo.Result
   type PromoResponse = workflowPromo.Response
   var DefaultPromoLanguages = workflowPromo.DefaultPromoLanguages

   // POST (canonical):
   // (block deleted; callers consume persistence.* + workflow/promo.* directly)
   ```
   `voiceover/types.go` is REMOVED entirely if zero remaining symbols (post-canonical content audit).

2. Tutti i callers via grep:
   ```bash
   rg "voiceover\.VoiceoverRepository|voiceover\.VoiceoverRecord|voiceover\.PromoRequest|voiceover\.PromoResult|voiceover\.PromoResponse|voiceover\.DefaultPromoLanguages" internal/ cmd/
   ```
   Meccanica per-call:
   ```go
   // PRE:
   func NewService(repo voiceover.VoiceoverRepository, ...) { ... }
   // POST:
   func NewService(repo persistence.Repository, ...) { ... }
   ```

**Canonical SOLE owners** (post migration):
- `persistence.Repository` lives ONLY at `internal/application/voiceover/persistence/repository.go`
- `persistence.VoiceoverRecord` lives ONLY at the same file
- `workflow/promo.Request + Result + Response + DefaultPromoLanguages` live ONLY at `internal/application/workflow/promo/types.go`

**Migration strategy** (2 sub-PRs to manage blast-radius):
- `PR-VOICEOVER-REPO-ALIASES` — only the 2 repo aliases (P1 sub-task A)
- `PR-VOICEOVER-PROMO-ALIASES` — only the 4 promo aliases (P1 sub-task B)

Each sub-PR lands independently with caller migration via `rg`-verified sweep.

**Tests**:
- The voiceover production test surface already exercises these aliases (canonical-substitution is mechanical).
- Forward-prevention archcheck gate: NEW `cmd/archcheck/scan/percheck_voiceover_alias_ban.go` mirroring `PR-CHECK-N-PLAYER-CLIENT-CENTRALIZATION` pattern (commit db35ed52, 2026-07-06).
- `TestVoiceover_NoDeprecatedAliases` — `rg "voiceover\.VoiceoverRepository" internal/` returns 0.

**Verification gates**:
- `gofmt -l` clean
- Migration per-sub-PR: each sub-PR's `rg "voiceover\.X"` returns 0 production-code hits post-sweep
- `go build ./...` exit 0
- `go test -short ./internal/application/voiceover/... ./internal/application/workflow/promo/...` PASS
- `go run ./cmd/archcheck --strict` reports ZERO violations on the voiceover alias pattern

**godlike/07 minimum-blast-radius**: 2 alias symbols → 0 (sub-PR A) + 4 alias symbols → 0 (sub-PR B); pure mechanical rename + caller migration.

---

### §2.5 — `PR-DRIVE-ERROR-TYPED` (#17, P1, deadline 2026-08-01)

**Target**: `FileIsNotTrashed` + `FileExists` migrated to `errors.As(*googleapi.Error)` + `Code == http.StatusNotFound`; centralized classification helper.

**Surface (2 files)**:

1. `internal/infrastructure/drive/uploader_file.go` — REPLACE:
   ```go
   // PRE:
   func FileIsNotTrashed(err error) bool {
       if err == nil { return false }
       s := err.Error()
       return strings.Contains(s, "404") || strings.Contains(s, "notFound")
   }
   func FileExists(err error) bool {
       return !FileIsNotTrashed(err)
   }
   // POST:
   // REMOVED; replaced by DriveAPIErrors (canonical helper)
   ```

2. NEW `internal/infrastructure/drive/errors.go` (canonical SSOT home for typed helpers):
   ```go
   package drive

   import (
       "errors"
       "google.golang.org/api/googleapi"
       "net/http"
   )

   // DriveIsNotFound classifies a Drive API error as 404.
   func DriveIsNotFound(err error) bool {
       var apiErr *googleapi.Error
       if !errors.As(err, &apiErr) { return false }
       return apiErr.Code == http.StatusNotFound
   }

   // DriveIsFileGone classifies as 404 + (trashed | deleted).
   func DriveIsFileGone(err error) bool {
       if !DriveIsNotFound(err) { return false }
       var apiErr *googleapi.Error
       _ = errors.As(err, &apiErr)  // already verified above
       // canonical message-match for trashed/deleted detail
       return strings.Contains(apiErr.Message, "trashed") ||
              strings.Contains(apiErr.Message, "deleted") ||
              strings.Contains(apiErr.Message, "notFound")
   }
   ```
   Compile-time `var _ typedCheckerShape = (*DriveErrorChecker)(nil)` (per codebase compile-time pin discipline).

**Caller sweep**:
```bash
rg "FileIsNotTrashed|FileExists" internal/
```
All callers: replace `FileIsNotTrashed(err)` → `drive.DriveIsFileGone(err)`; `FileExists(err)` → `!drive.DriveIsNotFound(err)`.

**Tests** (NEW `internal/infrastructure/drive/errors_test.go`):
- `TestDriveIsNotFound_GenuineNotFound` — fake error: `&googleapi.Error{Code: 404, Message: "File not found: abc"}` → true
- `TestDriveIsNotFound_OtherHTTPCode` — `Code: 500` → false
- `TestDriveIsNotFound_NonGoogleAPIError` — `errors.New("random text")` → false (regression guard for the anti-pattern we're fixing)
- `TestDriveIsNotFound_WrappedError` — `fmt.Errorf("op failed: %w", &googleapi.Error{Code: 404})` → true (errors.As walks wrapped chain)
- `TestDriveIsNotFound_NilError` → false (nil-tolerance)

**Verification gates**:
- `gofmt -l` clean
- `go vet ./internal/infrastructure/drive/...` exit 0
- `rg "strings.Contains.*err.Error" internal/infrastructure/drive/` returns 0 production-code hits post-sweep (forward-prevention at this directory level)
- `go test -short ./internal/infrastructure/drive/...` PASS 5/5

**godlike/07 minimum-blast-radius**: 2 functions → 1 (`DriveIsNotFound`); 0 signature drift on external callers (boolean return semantics preserved); classification surface migrated to typed googleapi.Error contract.

---

### §2.6 — `PR-SOURCING-ADAPTER-FAIL-CLOSED` (#18, P1, deadline 2026-08-08)

**Target**: `UpdateCumulativeJSON` + `sourcingEnrichmentAdapter.EnrichAndIndex` ritornano typed-sentinel; compose-time gate fail-closed at composition root.

**Surface (3 files)**:

1. `internal/application/assets/sourcing/ports.go` — ADD typed sentinels:
   ```go
   var (
       ErrSourcingUpdateCumulativeDisabled  = errors.New("sourcing: UpdateCumulativeJSON disabled at composition root")
       ErrSourcingEnrichAndIndexDisabled    = errors.New("sourcing: EnrichAndIndex disabled at composition root")
   )

   type SourcingAtomicPort interface {
       // PRE: UpdateCumulativeJSON(ctx, ...) error // returns nil always
       // POST: returns ErrSourcingUpdateCumulativeDisabled when handler NOT wired
       UpdateCumulativeJSON(ctx context.Context, ...) error

       // PRE: EnrichAndIndex(...) error // returns nil always
       // POST: returns ErrSourcingEnrichAndIndexDisabled when handler NOT wired
       EnrichAndIndex(ctx context.Context, ...) error
   }
   ```

2. `internal/application/assets/sourcing/enrichment_adapter.go` (or wherever the live impl lives) — REWRITE:
   ```go
   // PRE:
   func (a *sourcingEnrichmentAdapter) EnrichAndIndex(ctx context.Context, ...) error {
       if a.handler == nil { return nil }  // ← silent-success ANTIPATTERN
       return a.handler.EnrichAndIndex(ctx, ...)
   }
   // POST:
   func (a *sourcingEnrichmentAdapter) EnrichAndIndex(ctx context.Context, ...) error {
       if a.handler == nil {
           return ErrSourcingEnrichAndIndexDisabled
       }
       if err := a.handler.EnrichAndIndex(ctx, ...); err != nil {
           return fmt.Errorf("sourcing: enrichment handler: %w", err)
       }
       return nil
   }
   ```

3. `internal/app/build_bundles_sourcing.go` — FAIL-CLOSED at composition:
   ```go
   func wireSourcingAtomic(ctx context.Context, cfg SourcingConfig, h SourcingAtomicPort) (SourcingAtomicPort, error) {
       if cfg.Enabled && h == nil {
           return nil, fmt.Errorf("sourcing: atomic surface enabled but no handler wired: %w", ErrSourcingCapabilitiesRequired)
       }
       if !cfg.Enabled {
           return nil, ErrSourcingCapabilitiesDisabled  // typed-sentinel
       }
       return h, nil
   }
   ```

**Tests** (NEW `internal/application/assets/sourcing/atomic_fail_closed_test.go`):
- `TestUpdateCumulativeJSON_NilHandler_ReturnsSentinel` (no-logic silent-success regression guard)
- `TestEnrichAndIndex_NilHandler_ReturnsSentinel`
- `TestEnrichAndIndex_HandlerErrorPropagation_PreservesCause` (errors.As walks wrap chain)
- `TestWireSourcingAtomic_EnabledNoHandler_ReturnsErrSourcingCapabilitiesRequired`
- `TestWireSourcingAtomic_Disabled_ReturnsErrSourcingCapabilitiesDisabled`
- `TestWireSourcingAtomic_EnabledWithHandler_OK`

**Tests** (in NEW `internal/app/build_bundles_sourcing_test.go` — 3 hermetic TDD):
- `TestBuildBundlesSourcing_AtomicSurface_MissingHandlerReturnsError` (loud failure in `WireSourcingAtomic`)

**Verification gates**:
- `gofmt -l` clean
- `go vet ./internal/application/assets/sourcing/... ./internal/app/...` exit 0
- `rg "if .* == nil { return nil }" internal/application/assets/sourcing/` returns 0 hits post-sweep (anti-pattern audited out)
- `go test -short ./internal/application/assets/sourcing/...` PASS 6/6
- `go build ./cmd/server/ ./cmd/worker/` clean (composition still works)

**godlike/07 minimum-blast-radius**: 2 silent-success paths → typed-sentinel paths; FAIL-CLOSED gate at composition prevents `WireSourcingAtomic` from returning a nilled-out surface to handler-bootstrap.

---

## §3 — Cross-references (godlike/06 SSOT umbrella)

- **AGENTS.md Pattern 0** (typed-port abstraction layer): the canonical rule this action plan applies; issue #18 in particular mirrors the **DL-006 fail-closed composition-root gate** (per AGENTS.md AGENTS.md "Critical Artlist rules" section).
- **AGENTS.md Pattern 5** (mechanical splits): the per-PR shape; each PR follows the PR-XXX-RETIRE or PR-XXX-XXX format per codebase precedent.
- **AGENTS.md godlike/07 NO-FAKE-AVAILABILITY** (load-bearing for #13, #14, #17, #18): silent-success elimination discipline.
- **`architecture/waves/wave_p1_high.yaml#LOGIC-SIMPLIFICATION-DEAD-CODE-2026-07-09`** (existing wave): the 6 anti-patterns in this action plan are CANDIDATES for that wave's child slots — the action plan proposes a NEW dedicated wave with stricter per-PR scope.
- **`architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`**: 6-item voiceover + app build-issue carry-forward UNCHANGED — NOT regressions of this plan.
- **`architecture/waves/wave_p1_high.yaml#PRE-EXISTING-YAML-PARSE-2026-07-04`** + **`#PR-CURRENT-YAML-PARSE-FIX-PART-6`**: parse-error carry-forward (forward-pointer unblocks wave-tracker slot flip).
- **`architecture/waves/wave_p3_low_and_audit.yaml#PR-CLIPS-PORT-EXTRACT`** (per `2026-07-08-pr-clips-port-extract.md`): the canonical parallel plan — issues #13 + #14 here are the application-side counterpart of #15 (clips port migration).
- **`architecture/waves/wave_p1_high.yaml#FASE-3.7-CHECK-3`** + **Monitor port reclamation**: precedent for forward-prevention gates via `cmd/archcheck/scan/` + `Check N` numbering per existing pattern.

---

## §4 — Honest scope-lock (godlike/07)

**This action plan is documentation-only** — the current commit lands the wave-tracker slot DEFERRED + this action plan + the 3-surface lockstep (CHANGELOG.md + AGENTS.md mirrors). The implementation lands per §2.1..§2.6 between 2026-07-15 and 2026-08-08.

**Per-PR migration discipline** (godlike/07 EXPAND→BACKFILL→CUTOVER→CONTRACT):
- EXPAND: each PR's typed port or alias typed-sentinel is INTRODUCED; legacy surface is preserved with warning comments.
- BACKFILL: per-call site migration walks the codebase via rg-verified sweep.
- CUTOVER: legacy surface is git-rm'd; `rg` forward-prevention confirms ZERO production-code call-sites.
- CONTRACT: archcheck gate forward-prevents re-introduction (mirror the `PR-CHECK-N-PLAYER-CLIENT-CENTRALIZATION` commit db35ed52 precedent).

**Pre-existing carry-forward (NOT regressions of this plan)**:
- 6-item voiceover + app build-issue list per `architecture/waves/wave_p1_high.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04`.
- YAML parse error in `architecture/waves/wave_p1_high.yaml` per `PRE-EXISTING-YAML-PARSE-2026-07-04` + `PR-CURRENT-YAML-PARSE-FIX-PART-6` (deadline 2026-08-15).
- Pre-existing dirty working tree residue (39+ files from prior sessions — handler_flow.go modifications + 5 deleted handler_legacy_*.go files + untracked yt-clip-tests/ + 4 unrelated `cmd/admin/gen_api_docs.go` + `docs/api/ACTIVE_API_GENERATED.md` + `ARCHITECTURE.md`) is OUT OF SCOPE per godlike/07 minimum-blast-radius.

**Wave-flip criterion** (per action-plan lifecycle):
- All 6 per-PR closures reach `status: shipped + ship_sha`
- archcheck gate forward-prevention reports ZERO violations on the 4 surface canonical owners
- Full `go test -short ./...` PASS (excluding pre-existing carry-forward)
- Cross-validation frequency check via `PR-CLEANUP-HOTSPOT-CROSSREF-NEXT` shows no high-frequency hotspots NOT captured by this plan.

---

## §5 — Lifecycle audit-trail

- **2026-07-10**: Action plan landed (this commit). Wave-tracker slot DEFERRED per canonical codebase convention.
- **2026-07-15**: target checkpoint — `PR-CLIPS-DAPTER-RESOLVER-RETIRE` + `PR-CLIPS-DAPTER-BUNDLE-SLIM` ship.
- **2026-07-22**: `PR-VOICEOVER-GROUPSRESOLVER-RETIRE` ships (file git-rm + 5 call-site migration).
- **2026-07-29**: `PR-VOICEOVER-ALIASES-RETIRE` (sub-PR A: repo aliases) + (sub-PR B: promo aliases) ship.
- **2026-08-01**: `PR-DRIVE-ERROR-TYPED` ships.
- **2026-08-08**: `PR-SOURCING-ADAPTER-FAIL-CLOSED` ships.
- **2026-08-15**: parent wave-flip to `status: shipped + exit_signal: true` triggers ONLY when all 6 per-PR closures reach `status: shipped` AND archcheck gate reports ZERO violations AND full test suite green.

---

**Co-authored-by**: PipelineGen Agent <agent@pipelinegen.local>
**3-surface lockstep**: this action plan ≈ `CHANGELOG.md ## Unreleased > ### Documentation` mirror entry ≈ `AGENTS.md ## Recent cross-cutting closures` mirror entry. Wave-tracker slot DEFERRED per pre-existing YAML parse carry-forward.
