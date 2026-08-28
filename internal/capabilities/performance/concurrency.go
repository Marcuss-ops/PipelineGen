package performance

import "context"

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
