# Step 6 Hard Gate Promotion with Transitional Allowlist

Date: 2026-06-28
Owner: @pipeline-team
Hard deadline: 2026-07-15

## Audit Baseline (origin/main @ 471ac5eb)

### Files >500 LoC (productive, exclude *_test.go): 25

  1004 internal/application/assets/providers/aggregator.go\n   910 internal/app/wire_script.go\n   900 internal/application/clips/clip_ops.go\n   859 internal/infrastructure/database/sqlite/outbox/repository.go\n   736 internal/application/scripts/usecase/flow_helpers.go\n   729 internal/infrastructure/observability/metrics.go\n   660 internal/application/qdrant/legacyaudit/legacyaudit.go\n   650 internal/app/module_media.go\n   644 internal/application/clips/bulk_upload_worker.go\n   629 internal/application/workerdoctor/default_probes.go\n   626 internal/api/script/handler_legacy_adapters.go\n   598 internal/infrastructure/qdrant/collection_manager.go\n   597 internal/application/assets/sourcing/service.go\n   578 internal/application/qdrant/reconciler/service.go\n   572 internal/infrastructure/qdrant/verifier.go\n   565 internal/api/assets/voiceover/handler.go\n   562 internal/app/build_bundles_process.go\n   552 internal/application/mediasearch/service.go\n   550 internal/app/composition.go\n   544 internal/application/assets/providers/registry.go\n   531 internal/application/scripts/usecase/engine.go\n   526 internal/platform/config/types.go\n   523 internal/infrastructure/database/sqlite/jobs/repository.go\n   521 internal/app/module_sources.go\n   511 internal/application/scripts/adapters/postprocessor_registry.go\n
### Packages >=40 productive files: 1

  46 internal/infrastructure/qdrant

## Owner & Deadline

- Owner: @pipeline-team
- Hard deadline: 2026-07-15 (allowlist day-bombed)

## Migration Sequence (EXPAND -> BACKFILL -> CUTOVER -> CONTRACT)

Per docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md, the migration is:

- **Phase 0 (NOW, 2026-06-28)**: gate promoted + allowlist transitional [state: this commit]
- **Phase 1 (W1, by 2026-07-01)**: human-assisted split top-3 outlier:
  - internal/application/assets/providers/aggregator.go
  - internal/app/wire_script.go
  - internal/application/clips/clip_ops.go
- **Phase 2 (W2, by 2026-07-08)**: human-assisted split batch 2:
  - internal/infrastructure/database/sqlite/outbox/repository.go
  - internal/application/scripts/usecase/flow_helpers.go
  - internal/infrastructure/observability/metrics.go
- **Phase 3 (W3-W4, by 2026-07-15)**: human-assisted split remaining 17 outlier (one per developer-day).
- **Phase 4 (2026-07-15)**: allowlist expiration. Gate diventa HARD error su TUTTI i 25 outlier pendenti. Sospensione merge automatico fino a risoluzione.

## Evidence Accepted per Phase

For each phase, the canonical evidence is:
- `bash scripts/ci-architectural-checks.sh` returns GREEN (Check 15 + Check 16 0 violazioni HARD)
- `go build ./...` exits 0
- `go vet ./...` exits 0
- Test del package toccato: `go test ./<pkg-path>/...`

## Tracking

This promotion corresponds to `architecture/current.yaml` id-22 allowlist migration entry.

