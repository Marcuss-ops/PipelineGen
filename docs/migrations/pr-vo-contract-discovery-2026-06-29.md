# PR-VO-CONTRACT Discovery (godlike/13 §1, June 2026)

> **Phase 1 of the 7-phase teardown.** Documents every consumer file +
> symbol of the legacy voiceover batch + promo surface so subsequent
> phases (Runtime cut, Data handling, Code removal, Configuration,
> Verification, Completion) target the right deletions without
> re-discovering the surface.

## Scope (the surface to remove)

| Symbol | Defined in | Status |
|--------|------------|--------|
| Job type `voiceover.batch` | `internal/domain/job/job.go::TypeVoiceoverBatch` | CONTRACT in Phase 3 |
| Job type `voiceover.promo` | `internal/domain/job/job.go::TypeVoiceoverPromo` | CONTRACT in Phase 3 |
| `Service.GenerateBatch` | `internal/application/voiceover/service.go` | CONTRACT in Phase 4 |
| `Service.GenerateWithDestination` | `internal/application/voiceover/service.go` | CONTRACT in Phase 4 |
| `Service.GeneratePromo` | `internal/application/voiceover/promo.go` | CONTRACT in Phase 4 |
| `Service.translator` field | `internal/application/voiceover/service.go` | CONTRACT in Phase 4 |
| `voiceover/batch` handler | `internal/api/assets/voiceover/handler.go` (legacy route) | Runtime cut in Phase 2 |
| `voiceover/promo` handler | same | Runtime cut in Phase 2 |
| `voiceover/sync/pkg` | `internal/application/voiceover/sync/` | Completion in Phase 7 |
| Sunset machinery | `internal/api/assets/voiceover/handler.go` (Sunset const + counter + helper) | Completion in Phase 7 |

## Lifetime consumer map (verbatim from `rg` 2026-06-29)

### Service method consumers (Phase-4 surface)

| Caller | Surface | Phase |
|--------|---------|-------|
| `internal/application/voiceover/job_handler.go` | `Service.HandleJob` dispatches `voiceover.batch` + `voiceover.promo` to legacy methods | Phase-4 target |
| `internal/application/scripts/jobs/job_helpers.go:159` | `voService.GenerateWithDestination(...)` sync call | Phase-4 migration target |
| `internal/application/scripts/adapters/processor_voiceover_test.go` | stub `GenerateWithDestination` | Phase-4 test stub target |
| `internal/application/scripts/adapters/processor_images_voiceover_test.go` | stub `GenerateWithDestination` | Phase-4 test stub target |
| `internal/application/voiceover/service_test.go` (TestGenerateBatch_RejectsPathTraversalPayload) | calls `Service.{Generate,GenerateBatch}` | Phase-4 test rename target |

### Job-type consumer (Phase-3 surface)

| Caller | Surface | Phase |
|--------|---------|-------|
| `internal/application/voiceover/service.go:RegisterHandler` | `jobsSvc.RegisterHandler(TypeVoiceoverBatch, …)` + `(TypeVoiceoverPromo, …)` (pre-cutover) | Phase-3 target |
| `internal/application/voiceover/job_handler.go:HandleJob` | switch on j.Type for legacy types | DEL with job_handler.go in Phase-4 |
| `internal/application/jobs/registry.go::Compose` | `JobPolicy{Type: TypeVoiceoverBatch, …}` + `JobPolicy{Type: TypeVoiceoverPromo, …}` | Phase-3 target |
| `internal/application/jobs/payloads.go::VoiceoverBatchPayload` | payload struct | Phase-3 target |
| `internal/application/jobs/registry_completeness_test.go` | `canonicalJobTypes` slice entries | Phase-3 target |
| `internal/application/jobs/registry_compose_ssot_test.go` | SSOT entries | Phase-3 target |

### Sync package consumer (Phase-7 surface)

| Caller | Surface | Phase |
|--------|---------|-------|
| `internal/app/composition.go` | `voiceoversync` import alias + `VoiceoverSync *voiceoversync.Service` field | Phase-7 migration target |
| `internal/app/build_bundles_domain.go` | `voiceoversync.NewService(...)` constructor call | Phase-7 migration target |
| `internal/app/module_media.go::WireAssets` | `*voiceoversync.Service` typed param | Phase-7 migration target |
| `cmd/admin/cleanup.go:507` (from prior turn audit) | reads `r.Domains.VoiceoverSync.Sync(ctx)` — works via re-export | Behind alias (rename to reconciliation/voiceover in Phase 7) |
| `internal/application/voiceover/sync/service.go` | legacy shim forwards to reconciliation/voiceover | Phase-7 deletion target |

### Sunset machinery consumer (Phase-7 surface)

| Caller | Surface | Phase |
|--------|---------|-------|
| `internal/api/assets/voiceover/handler.go::voiceoverGoneSunset` const | inline IMF-fixdate literal | Phase-2 introduce; Phase-7 delete |
| `internal/api/assets/voiceover/handler.go::addVoiceoverDeprecationHeader` helper | emits the Sunset + Link pair | Phase-2 introduce; Phase-7 delete |
| `internal/api/assets/voiceover/handler.go::legacyVoiceoverRouteInvocationsTotal` counter | Prometheus-style metric stub | Phase-2 introduce; Phase-7 delete |
| `internal/api/assets/voiceover/handler_pr_vo_c1_test.go` | Sunset regression pin | Phase-7 deletion target |

## Authority cross-reference

- `docs/voiceover/blocco-6-confirmation.md` — Phase 1 confirmation record
- `docs/voiceover/p0-bundle-A1-A6.md` — A1..A6 closure record
- `docs/voiceover/p1-bundle-B1-C1.md` — B1..C1 closure record (C1 ships Sunset lifecycle)
- `godlike/07` (CUTOVER + CONTRACT) — phase ordering rationale
- `godlike/13` (§1 = Discovery, current document)
- `AGENTS.md` Git-Lesson-2 — direct-to-main workflow
- `AGENTS.md` Rebase-Conflict Lesson — cheap-exit on broken phase

## Phase ordering rationale

The 7-phase ordering is dictated by compile-time dependency, not chronology:

1. **Phase 1** (Discovery, this doc) — no compile impact; documents scope.
2. **Phase 2** (Runtime cut) — backwards-compatible: legacy routes return 410 while canonical `voiceover.generate` job type stays. CI green.
3. **Phase 3** (Data handling) — drop `voiceover.batch` + `voiceover.promo` from canonicalJobTypes regex. Requires legacy handlers in `job_handler.go` to STOP handling those types in the same PR OR they go first.
4. **Phase 4** (Code removal) — delete `Generate*` methods + translator. Requires Phase-3 callers (job_handler.go's HandleJob, scripts/jobs/job_helpers.go) migrated in Phase 3/PR-3 PRs.
5. **Phase 5** (Configuration and ops) — doc-only churn.
6. **Phase 6** (Verification) — tests pin canonicalJobTypes cardinality + Sunset + 410 stubs. Requires Phases 2/3 complete.
7. **Phase 7** (Completion) — delete legacy sync pkg + Sunset machinery. Requires Phase 6 test fixtures in place.

## Skip semantic

Per user's spec: "Skip any phase whose go-vet/build/test breaks (use the
AGENTS.md Rebase-Conflict Lesson cheap-exit)." Phases may land out of
order OR be skipped entirely if dependency barriers cannot be cleared
in a single atomic commit. The cheap-exit pattern is to `git reset
--hard HEAD~1` and retry — never amend in a loop.

## Status update (filled at end of session)

| Phase | Land? | Commit hash | Notes |
|-------|-------|-------------|-------|
| 1 Discovery | (this commit) | — | scope doc only; no code |
| 2 Runtime cut | | | |
| 3 Data handling | | | |
| 4 Code removal | | | |
| 5 Configuration | | | |
| 6 Verification | | | |
| 7 Completion | | | |
