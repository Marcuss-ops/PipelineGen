# Canonical PipelineGen reconciliation orchestration.
# Existing Drive and Qdrant commands remain the sole repair owners.

RECONCILE_PIPELINE_RUNNER ?= python3 scripts/ci/reconcile-pipeline.py

reconcile-pipeline:
	@$(RECONCILE_PIPELINE_RUNNER) $(if $(RECONCILE_APPLY),--apply,) --report artifacts/reconcile/pipeline.json

verify-reconciliation-contracts:
	@$(GO) test ./internal/capabilities/reconciliation ./internal/capabilities/scripts/adapters ./internal/capabilities/assets/deletion/reconciler ./internal/capabilities/jobs ./internal/platform/drive ./internal/platform/sqlite/outboxevents ./internal/capabilities/assets/providers/stock/enrichment ./internal/capabilities/assets/providers/stock/stockpipeline ./internal/capabilities/jobs/completion ./internal/capabilities/outbox ./internal/platform/sqlite/outbox

verify-orphan-cleanup:
	@$(GO) test ./internal/capabilities/assets/deletion/reconciler ./internal/capabilities/jobs ./internal/capabilities/outbox ./internal/platform/drive ./internal/platform/sqlite/outbox

verify-retention:
	@$(GO) test ./internal/capabilities/maintenance ./internal/capabilities/assets/maintenance ./internal/platform/sqlite/assetindex

verify-cancel-recovery:
	@$(GO) test ./internal/capabilities/jobs ./internal/capabilities/outbox ./internal/capabilities/scripts/generation ./internal/capabilities/scripts/usecase

verify-migrations:
	@$(GO) test ./internal/platform/sqlite/...

verify-migration-upgrade: verify-migrations

verify-db-integrity:
	@$(GO) test ./internal/platform/sqlite ./internal/platform/sqlite/...

verify-qdrant-rebuild:
	@$(GO) test ./internal/capabilities/reconciliation ./internal/platform/qdrant/indexing ./internal/platform/qdrant/collections
