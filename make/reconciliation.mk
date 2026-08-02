# Canonical PipelineGen reconciliation orchestration.
# Existing Drive and Qdrant commands remain the sole repair owners.

RECONCILE_PIPELINE_RUNNER ?= python3 scripts/ci/reconcile-pipeline.py

reconcile-pipeline:
	@$(RECONCILE_PIPELINE_RUNNER) $(if $(RECONCILE_APPLY),--apply,) --report artifacts/reconcile/pipeline.json

verify-reconciliation-contracts:
	@$(GO) test ./internal/application/qdrant/reconciler ./internal/application/scripts/adapters ./internal/application/assets/deletion/reconciler ./internal/application/jobs/finalizer ./internal/infrastructure/drive ./internal/infrastructure/database/sqlite/outboxevents ./internal/application/assets/providers/stock/enrichment ./internal/application/assets/providers/stock/stockpipeline ./internal/domain/clips ./internal/application/jobs/completion ./internal/application/jobs/outbox

verify-orphan-cleanup:
	@$(GO) test ./internal/application/assets/deletion ./internal/application/assets/deletion/reconciler ./internal/application/jobs/finalizer ./internal/application/jobs/outbox ./internal/infrastructure/drive

verify-retention:
	@$(GO) test ./internal/domain/completion ./internal/application/qdrant/dr ./internal/application/assets/maintenance

verify-cancel-recovery:
	@$(GO) test ./internal/application/jobs/finalizer ./internal/application/jobs/outbox ./internal/application/scripts/generation ./internal/application/scripts/usecase

verify-migrations:
	@$(GO) test ./internal/infrastructure/database/...

verify-migration-upgrade: verify-migrations

verify-db-integrity:
	@$(GO) test ./internal/infrastructure/database ./internal/infrastructure/database/sqlite/...

verify-qdrant-rebuild:
	@$(GO) test ./internal/application/qdrant/reconciler ./internal/infrastructure/qdrant/indexing ./internal/infrastructure/qdrant/collections
