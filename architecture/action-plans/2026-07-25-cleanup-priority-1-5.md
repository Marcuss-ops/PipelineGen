# Hard-Tech Cleanup Wave — 8 Priority Action Plan (2026-07-25)

> **Authority**: this file is the canonical narrative companion to the wave-tracker entry
> `architecture/current.yaml#CLEANUP-PRIORITY-1-5-2026-07-25`. Updates to scope / IDs / bands
> must keep the wave-tracker anchor + this file + `CHANGELOG.md ## Unreleased → ### Documentation`
> + `AGENTS.md ## Recent cross-cutting closures` in lockstep per godlike/06 SSOT (one-canonical-
> owner-per-fact).

## Context

Italian audit (2026-07-25 user-pasted) surfaced 8 concrete complexity / dead-code / false-
success hotspots across the codebase. Scope: **shrink surface area** (less ports, less
payload, less dependency bag, less noop fallbacks) without breaking production behaviour.

Per godlike/07 NO-FAKE-AVAILABILITY (the load-bearing invariant of the wave):

> No silent-success surface remains after the wave: noop adapters, noop stubs, hard-coded
> fallbacks, registered-but-deprecated routes all either **fail-closed** or return **HTTP 410
> Gone** with explicit migration target — never 200 OK with empty payload.

Per godlike/06 SSOT one-canonical-owner-per-fact: each of the 8 priorities has exactly one
canonical surface to retire and one canonical replacement. Per godlike/07 minimum-blast-
radius: each per-PR lands in isolation on its own subtree with targeted
`gofmt + go vet + go build + go test -short` gates (per AGENTS.md Git-Lesson-2 direct-to-main).

## Per-Band Prioritization (slim-schema ratchet)

| Band | Items | Deadline | Pattern |
|------|-------|----------|---------|
| **P0_absolute** | PR-noop-adapters-purge + PR-script-legacy-contract | 2026-08-01 | Hard-kill the silent-success surface + drive-the-route-gone |
| **P1** | PR-script-deps-slim + PR-qdrant-readiness-slim + PR-jobs-retry-contract | 2026-08-08 | Slim-source-of-truth + mandatory-DI at boot |
| **P2_media** | PR-stock-production-deps + PR-parent-state-cutover | 2026-08-15 | Fail-fast at composition root + typed-column cutover EXPAND phase |
| **P3_bassa** | PR-docs-archive + PR-CLEANUP-HOTSPOT-CROSSREF | 2026-08-22 | Doc-only archival + post-wave `git log` frequency cross-validation |

## Tactical Guidance per Priority

### 1. PR-script-legacy-contract (P0_absolute, deadline 2026-08-01)

**Goal**: retire 2 still-live legacy routes while keeping graceful client transition.

**Surface to retire**:
- `internal/api/script/handler_legacy_from_clips.go` (~185 LoC, `LegacyGenerateFromClips`)
- `internal/api/script/handler_legacy_with_images.go` (~similar, `LegacyGenerateWithImages`)
- `internal/api/script/handler_legacy_warnings.go` if used only by legacy
- 2 `r.POST(...)` calls in `internal/api/script/handler_flow.go::RegisterRoutes`
  (lines ~113 `/generate-from-clips`, ~?? `/generate-with-images`)
- Prometheus counters in `internal/api/script/handler_legacy_deprecation.go`
- `DeprecationCount` shim helper

**Replacement contract**: HTTP **410 Gone** (NOT 404 — 404 is ambiguous "routing bug or
missing resource"; 410 is explicit "intentionally retired, migrate to /generate V2").

Response body JSON shape:
```json
{
  "error": "endpoint_retired",
  "message": "This endpoint was retired 2026-08-01. Use POST /api/script/generate",
  "migration_target": "/api/script/generate",
  "deprecation_date": "2026-12-31",
  "docs": "https://github.com/Marcuss-ops/PipelineGen/blob/main/AGENTS.md#script-endpoints"
}
```

**godlike/07 minimum-blast-radius**: keep `handler_legacy_*.go` source files in repo
(FREEZE-phase, removal target 2026-12-31) — only flip the handler body to a `return 410`
shell. Per-channel telemetry (`metrics.GenerateFromClipsDeprecationTotal`) STAYS live so
dashboard surfaces the dead-route traffic.

**Verification gates**: `gofmt -l internal/api/script/` clean; `go vet ./internal/api/script/...` exit 0;
`go build ./internal/api/script/...` exit 0; per-legacy-route 410 contract test
(HTTP request → assert `StatusGone=410` + JSON body assertions).

### 2. PR-script-deps-slim (P1, deadline 2026-08-08)

**Goal**: shrink `ScriptFlowDeps` (~12 fields ignored) + `module.go::Dependencies` (~22 fields, 18 nil-tolerant).

**Surface to slim**:
- `internal/api/script/handler_deps.go::ScriptFlowDeps` — split into 3 typed structs
- `internal/api/script/module.go::Dependencies` — purge `ScriptDescriptor.Handler` future-proofing (comment admits zero non-HTTP caller)

**Replacement contract**:
```go
type GenerateDeps struct {  // /generate route only
    Engine         *scriptcore.Engine  // required
    EnabledFunc    func() bool         // required
}
type JobsDeps struct {     // /jobs/* routes only
    Service        *jobs.Service       // required
    Logger         *zap.Logger         // required
}
type LegacyDeps struct {   // /generate-from-clips, /generate-with-images only — gone after 2026-12-31
    // empty (routes are 410 stubs after PR-script-legacy-contract ships)
}
```

**Rule per godlike/07**: **if a dependency is optional, the route that uses it is NOT mounted;
if it is required, `Build` MUST fail-fast at composition root**. Zero "maybe will serve".

**Verification gates**: `gofmt -l internal/api/script/` clean; `go build ./internal/api/script/...` exit 0;
`go test -short ./internal/api/script/...` PASS (existing 17+ tests must still pass after split).

### 3. PR-noop-adapters-purge (P0_absolute, deadline 2026-08-01)

**Goal**: physically `git rm` 2 noop script adapters that return success with empty payload —
classic godlike/07 fake-availability violation.

**Surface to retire**:
- `internal/application/scripts/adapters/compat_adapters.go::noopEntityExtractionAdapter` (returns `EntityResult{}`)
- `internal/application/scripts/adapters/compat_adapters.go::noopMetadataGenerationAdapter` (returns `nil, nil`)

**Replacement contract**: typed sentinel `ErrEntityExtractorUnavailable` + `ErrMetadataGeneratorUnavailable`.
Composition root fails-fast at boot if a backend is unwired. Hand-written test must assert:
```go
processor.Run(ctx, cmd) // returns errors.Is(err, ErrEntityExtractorUnavailable), NOT EntityResult{}
```

**Verification gates**: `git grep -E 'noopEntityExtractionAdapter|noopMetadataGenerationAdapter' internal/` returns 0 live hits;
`gofmt + go vet + go build ./internal/application/scripts/...` clean;
new fail-closed contract test PASS.

### 4. PR-qdrant-readiness-slim (P1, deadline 2026-08-08)

**Goal**: collapse double probe + retire noop dry-run shims.

**Surface to slim**:
- `cmd/admin/qdrant_readiness.go::qdrantReadiness` — currently runs probe+schema check AND registers `qdrant_active_collection_real` separately (which then runs probe+schema+compare AGAIN)
- `cmd/admin/qdrant_readiness.go` reconciler — uses `readiNoopOutbox` + `readiNoopPayload` (always return nil)

**Replacement contract**:
- **One** `checkQdrantActiveCollectionReal` orchestrator that writes BOTH `report.QdrantReachable`
  AND `report.ActiveCollectionCompatible` in a single probe cycle.
- Drop `qdrantProbeAndSchema` (duplicate probe path) OR drop the duplicate `qdrant_active_collection_real` check.
- Reconciler dry-run uses explicit `DryRun: true` contract — not fake ports that look real.

**Verification gates**: `gofmt + go vet + go build ./cmd/admin/...` clean;
`bash tests/operational/qdrant_e2e_boxing_smoke.sh` PASS (replaces the in-process probes).

### 5. PR-jobs-retry-contract (P1, deadline 2026-08-08)

**Goal**: remove 3 legacy branches + hardcoded `return 3` fallback + brittle string-match error probe.

**Surface to slim**:
- `internal/application/jobs/enqueue_service.go::resolveMaxRetries` — currently has 3 branches:
  returns 3 (hardcoded fallback) when `Registry == nil`
- `internal/application/jobs/enqueue_service.go::NewService(...).WithRegistry(...)` — optional by design (to keep legacy)
- `internal/application/jobs/enqueue_service.go::enqueue` — uses `strings.Contains(err.Error(), "UNIQUE constraint")` to detect SQLite unique violations

**Replacement contract**:
- `NewService(deps ServiceDeps, reg RetryPolicyRegistry) *Service` — `reg` is **required**.
  Racket discipline: composition root fails-fast at boot if `reg == nil`.
- `resolveMaxRetries` is a single typed lookup: `return reg.GetMaxRetries(jobType)` (zero branches, zero fallback).
- SQLite unique-violation probe uses typed error: `var sqliteErr *sqlite3.Error; if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE { ... }`.

**Verification gates**: `gofmt + go vet + go build ./internal/application/jobs/...` clean;
constructor nil-reg test returns `ErrRegistryRequired` (typed sentinel);
existing enqueue tests PASS (no behavioral change for valid registry path).

### 6. PR-stock-production-deps (P2_media, deadline 2026-08-15)

**Goal**: composition-root fail-fast for `SourceStager` and `Renderer` deps. NO nil-tolerance in production code path.

**Surface to slim**:
- `internal/application/assets/providers/stock/stockpipeline/orchestrator_steps.go::StockStageSourcesStep` — guards `if stager == nil { return nil }` + `StockComposeChunksStep` — guards `if renderer == nil { return nil }`

**Replacement contract**:
- `internal/application/assets/providers/stock/stockpipeline/types.go` exports 2 typed sentinels:
  `ErrStagerRequired` + `ErrRendererRequired`.
- Constructor wires both. If nil in production: panic OR `return ErrStagerRequired` from `NewOrchestrator(deps)`.
- Tests pass explicit fakes — no `nil` leaking into production paths.

**Verification gates**: `gofmt + go vet + go build ./internal/application/assets/providers/stock/stockpipeline/...` clean;
new constructor-nil-deps test returns typed `ErrStagerRequired` / `ErrRendererRequired`.

### 7. PR-parent-state-cutover (P2_media, deadline 2026-08-15)

**Goal**: complete typed-column cutover for voiceover `parent_state_typed`. JSON key only as payload.

**Surface to slim**:
- `internal/application/voiceover/jobs/parent_aggregator.go::FinalizeAggregateParent` — currently does `WHERE parent_state_typed = ?` WRITE + `WHERE json_extract(result_json,'$.parent_state') = ?` READ (dual-state fragility)

**Replacement contract (EXPAND phase — LIVE, defer CUTOVER to post-2026-08-15 wave)**:
- Today: Go reads/writes/guards ONLY on `parent_state_typed`. JSON key ignored on read.
- Reads of legacy JSON key return zero rows in `ResultMap` query — silent gap is acceptable per EXPAND phase (no regression risk because typed column was empty for new rows anyway).
- CUTOVER (future wave, deadline TBD): drop `result_json.parent_state` field from wire shape; backfill
  existing rows from JSON to typed column via one-shot CLI.

**Verification gates**: `gofmt + go vet + go build ./internal/application/voiceover/...` clean;
`go test -short ./internal/application/voiceover/jobs/` PASS (4 TDD regression tests).

### 8. PR-docs-archive (P3_bassa, deadline 2026-08-22)

**Goal**: clean stale-doc surface. Doc-only commit.

**Surface to slim**:
- `architecture/deprecations.yaml` — contains `P0-3-GENERATION-RESPONSE` with `status: removed` (archived but still in active manifest)
- `REPOSITORY_CLEANUP.md` — still references `git checkout -b codex/<focused-cleanup>` (contradicts AGENTS.md Git-Lesson-2 direct-to-main)

**Replacement contract**:
- Move `architecture/deprecations.yaml` records with `status: removed` (and `removal_date < 2026-07-01`) to `architecture/archive/deprecations-removed-2026.yaml` (new file). Slim down active manifest to **live** deprecations only.
- Rewrite `REPOSITORY_CLEANUP.md` to drop the `git checkout -b` recipe + add an explicit pointer to AGENTS.md §Git-Lesson-2 + §Git-Lesson-4 + §Git-Lesson-5.

**Verification gates**: `python3 -c "import yaml; yaml.safe_load(open('architecture/deprecations.yaml'))"` exit 0;
`python3 -c "import yaml; yaml.safe_load(open('architecture/archive/deprecations-removed-2026.yaml'))"` exit 0;
`rg "git checkout -b codex" .` returns 0 hits (excluding archive/ history-only mentions).

### 9. PR-CLEANUP-HOTSPOT-CROSSREF (P3_bassa_post_wave, deadline 2026-08-22)

**Goal**: post-wave `git log --since=90.days` frequency cross-validation — verify no high-
frequency hotspots NOT captured by the 8 priorities above.

**Surface**: documentation-only commit. Surfacing holes mean new `PR-CLEANUP-NEWPRIORITY-N`
slim-schema append (NOT retroactive edit) per godlike/07 ratchet discipline.

## Migration Sequence (godlike/07 EXPAND → BACKFILL → CUTOVER → CONTRACT)

Per priority, in each PR:
- **EXPAND**: new typed sentinel / new HTTP 410 contract live alongside legacy surface.
  CI gate `check_70_legacy_410_contract` forward-prevention.
- **BACKFILL**: per-call-site migration to typed contract (only when needed).
- **CUTOVER**: legacy surface `git rm`'d ON OR AFTER its removal date.
- **CONTRACT**: physical git-rm + tightened ci-gate (zero allowlist rows).

## Verification Surface (per-PR gates)

Each per-PR MUST, in isolation on its own subtree:

```bash
# Pre-flight
gofmt -l <touched_files>                                      # exit 0
go vet ./<subtree>/...                                         # exit 0
go build ./<subtree>/...                                       # exit 0

# Test
go test -short -count=1 ./<subtree>/...                        # PASS

# For per-PR verification of the new contract:
go test -short -count=1 -run '^Test<NewContract>' ./...        # PASS
```

## Honest Scope-Lock (godlike/07 no-fake-availability)

1. **Static prioritization** by complexity + accumulated risk (NOT git-log frequency at submission).
   The forward-pointer `PR-CLEANUP-HOTSPOT-CROSSREF` is the post-wave validation surface —
   if surfaces a high-frequency hotspot not in plan, slim-schema append-only ratchet adds new
   linked_issues.
2. **6 pre-existing build issues** (`workerruntime + monitor + stockpipeline + module_media +
   images_routing + REMAINING carry-forward`) carry forward unchanged — **NOT regressions**
   of any PR-CLEANUP-PRIORITY-N commit.
3. **No production code change in this commit** (commit SHA post-rebase). Documentation +
   wave-tracker only.
4. **TDD-first**: each per-PR writes its own typed-sentinel test BEFORE the production code
   change. No `t.Skip` markers (per PR-PERSIST-6-CANONICAL precedent + Active Concerns #10 fix).

## Cross-References (3-surface godlike/06 SSOT lockstep)

- `architecture/current.yaml#CLEANUP-PRIORITY-1-5-2026-07-25` — canonical wave-tracker anchor
  (9 slim-shape `linked_issues` + parent `deadline: 2026-08-22`)
- `architecture/action-plans/2026-07-25-cleanup-priority-1-5.md` — this file (canonical narrative)
- `CHANGELOG.md ## Unreleased → ### Documentation` — addition entry
- `AGENTS.md ## Recent cross-cutting closures` — mini-mirror entry
- AGENTS.md §Git-Lesson-2 (direct-to-main, no branches, no `--force`)
- AGENTS.md §Git-Lesson-3 (Co-authored-by trailer convention)
- AGENTS.md §Git-Lesson-4 + §Git-Lesson-5 (recovery from non-fast-forward + byte-equivalent replay)

## Pre-Existing Build Issues Carry-Forward (NOT regressions)

The 6-item list per `architecture/current.yaml#PRE-EXISTING-BUILD-ISSUES-2026-07-04` is
unchanged. Each per-PR commit above MUST pass its targeted
`gofmt + go vet + go build + go test -short` gates on its own subtree;
the 5-item pre-existing list reproduces independently on the stashed pre-PR tree (NOT a
regression of this wave).

## Co-authored-by

Each per-PR commit lands with the canonical trailer per AGENTS.md §Git-Lesson-3:

```
Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>
```
