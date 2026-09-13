# Canonical PipelineGen reconciliation orchestration.
# Existing Drive and Qdrant commands remain the sole repair owners.

# reconcile-pipeline — RETIRED 2026-09-13: its driver
# scripts/ci/reconcile-pipeline.py does not exist (purged by commit 7e6965aab),
# so the target could only ever fail with "No such file or directory". The
# Go-native reconciliation gates below are the surviving surface.

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
