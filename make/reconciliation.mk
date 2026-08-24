# Canonical PipelineGen reconciliation orchestration.
# Existing Drive and Qdrant commands remain the sole repair owners.

RECONCILE_PIPELINE_RUNNER ?= python3 scripts/ci/reconcile-pipeline.py

reconcile-pipeline:
	@$(RECONCILE_PIPELINE_RUNNER) $(if $(RECONCILE_APPLY),--apply,) --report artifacts/reconcile/pipeline.json

verify-reconciliation-contracts:
	@$(GO) test ./internal/application/qdrant/reconciler ./internal/application/scripts/adapters ./internal/application/assets/deletion/reconciler ./internal/application/jobs/finalizer ./internal/platform/drive ./internal/platform/sqlite/outboxevents ./internal/capabilities/assets/providers/stock/enrichment ./internal/capabilities/assets/providers/stock/stockpipeline ./internal/application/jobs/completion ./internal/application/jobs/outbox

verify-orphan-cleanup:
	@$(GO) test ./internal/application/assets/deletion ./internal/application/assets/deletion/reconciler ./internal/application/jobs/finalizer ./internal/application/jobs/outbox ./internal/platform/drive

verify-retention:
	@$(GO) test ./internal/application/qdrant/maintenance ./internal/application/assets/maintenance

verify-cancel-recovery:
	@$(GO) test ./internal/application/jobs/finalizer ./internal/application/jobs/outbox ./internal/application/scripts/generation ./internal/application/scripts/usecase

verify-migrations:
	@$(GO) test ./internal/platform/sqlite/...

verify-migration-upgrade: verify-migrations

verify-db-integrity:
	@$(GO) test ./internal/platform/sqlite ./internal/platform/sqlite/...

verify-qdrant-rebuild:
	@$(GO) test ./internal/application/qdrant/reconciler ./internal/platform/qdrant/indexing ./internal/platform/qdrant/collections
