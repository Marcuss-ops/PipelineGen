package observability

import (
	"context"
	"encoding/json"
	"time"
)

// OperationMeta carries fan-out provenance that an owner-owned measurement
// (e.g. *ollama.Generator.GenerateScript) attaches to its canonical
// OperationReport. A parallel fan-out records queued_at, worker_id and task
// identity here, binds it to ctx with WithOperationMeta, and the owner merges
// it into the SAME operation that owns the wall clock. This lets a fan-out
// reconstruct real concurrency without the caller re-timing the boundary and
// without mistaking summed parallel work for wall time.
type OperationMeta struct {
	// WorkerID is the fan-out slot that ran the task (e.g. "seg-worker-0").
	// It is informational: the concurrency reconstruction is driven by the
	// timestamps, not the slot id.
	WorkerID string
	// QueuedAt is when the task was submitted to the fan-out pool. The owner
	// derives queue_wait_ms = started_at - queued_at.
	QueuedAt time.Time
	// Metadata carries identity labels (e.g. segment_id, segment_index) that
	// are persisted as run_operation_observations.metadata_json.
	Metadata map[string]string
}

type operationMetaContextKey struct{}

// WithOperationMeta binds meta to ctx so a nested owner-owned measurement can
// attach it to its canonical operation observation. It composes with WithRun:
// both values live on the same context chain.
func WithOperationMeta(ctx context.Context, meta OperationMeta) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, operationMetaContextKey{}, meta)
}

// OperationMetaFromContext returns the fan-out provenance bound to ctx, and
// whether any was bound.
func OperationMetaFromContext(ctx context.Context) (OperationMeta, bool) {
	if ctx == nil {
		return OperationMeta{}, false
	}
	meta, ok := ctx.Value(operationMetaContextKey{}).(OperationMeta)
	return meta, ok
}

// Apply enriches info with the bound provenance. Fields the caller already set
// explicitly win, so an owner that measured its own timestamp is never
// overwritten.
func (m OperationMeta) Apply(info *OperationInfo) {
	if info == nil {
		return
	}
	if info.WorkerID == "" {
		info.WorkerID = m.WorkerID
	}
	if info.QueuedAt.IsZero() {
		info.QueuedAt = m.QueuedAt
	}
	if info.MetadataJSON == "" {
		info.MetadataJSON = m.MetadataJSON()
	}
}

// MetadataJSON serializes Metadata as a stable JSON object (Go sorts map keys
// lexicographically), or "" when there is nothing to persist.
func (m OperationMeta) MetadataJSON() string {
	if len(m.Metadata) == 0 {
		return ""
	}
	b, err := json.Marshal(m.Metadata)
	if err != nil {
		return ""
	}
	return string(b)
}
