package performance

import (
	"context"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

type BatchConcurrency struct {
	BatchID         string
	RunID           string
	WorkerSlotCount int
	StartedAt       string
	CompletedAt     string
	CreatedAt       string
}

type BatchJobInterval struct {
	BatchID     string
	JobID       string
	RunID       string
	WorkerSlot  *int
	QueuedAt    string
	StartedAt   string
	CompletedAt string
	Status      string
	CreatedAt   string
}

type BatchConcurrencyStore interface {
	RecordBatch(context.Context, BatchConcurrency) error
	RecordBatchJob(context.Context, BatchJobInterval) error
}

// BatchConcurrencyReportReader derives the deterministic batch concurrency
// report for a persisted batch (migration 237 facts + the runs' canonical
// observability reports). The derivation is read-time only — lifecycle facts
// are stored, aggregates are never persisted — and goes through the canonical
// sweep (kernel/observability.DeriveBatchReport) with the end-before-start
// tie-breaker, so the report is deterministic for any batch.
type BatchConcurrencyReportReader interface {
	ConcurrencyReport(context.Context, string) (kernobs.BatchReport, error)
}
