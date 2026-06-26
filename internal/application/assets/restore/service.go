// Package restore — application-level wrapper around outbox.Dispatcher
// for asset restore flows.
//
// QDRANT-002 close-out (June 2026): consumer for the canonical
// Dispatcher.EnqueueAndRestore method. Previous CommitWave (PR7) shipped
// the dispatcher side; this file adds the application-side caller so
// any handler / admin tool wanting to un-delete a clip has a single
// typed entry point instead of reaching into infrastructure/outbox.
//
// Restore is the inverse of SoftDelete:
//
//  1. Lifecycle_state flips from 'deleted' back to 'ready' restore-side.
//  2. An asset.index.requested.v1 outbox event is emitted in the same
//     dispatcher tx; the worker re-indexes the restord asset into
//     Qdrant (RESTORE operation tagging for downstream observability).
//
// The atomic tx guarantees we never publish a "ready" asset without
// its embedding job — surfacing the previous bug where repo.Restore
// flipped state without re-emitting an outbox event, leaving the
// Qdrant point stale until the next manual sync.
package restore

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// DispatcherPort is the minimum surface the Service needs from
// outbox.Dispatcher. The single EnqueueAndRestore method is the only
// path Restore takes (QDRANT-002: write+outbox happen in the same tx).
type DispatcherPort interface {
	EnqueueAndRestore(ctx context.Context, assetID string, contentHash string) error
}

// LogPort mirrors the LogPort from deletion/service.go. Sized to
// zap.Logger's native signature (zap.Field variadic) so zap.NewNop()
// satisfies it directly without a custom adapter.
type LogPort interface {
	Info(msg string, fields ...zap.Field)
	Warn(msg string, fields ...zap.Field)
	Error(msg string, fields ...zap.Field)
}

// Service wraps DispatcherPort with logging + validation for the
// canonical restore path. Composition root wires this in
// BuildOutboxBundle; the application.service package never imports
// infrastructure/outbox directly (canonical layering).
type Service struct {
	dispatcher DispatcherPort
	log        LogPort
}

// NewService constructs a restore Service. dispatcher is REQUIRED —
// restore without a re-index event leaves the Qdrant point stale (the
// lifecycle_state flip is meaningless without the embedding job that
// matches the v2 canonical content fingerprint).
// nil at construction is a wiring bug caught immediately.
func NewService(dispatcher DispatcherPort, log LogPort) (*Service, error) {
	if dispatcher == nil {
		return nil, errors.New("restore.NewService: dispatcher is required (QDRANT-002 close-out — restore must atomically flip lifecycle_state AND emit asset.index.requested.v1)")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{dispatcher: dispatcher, log: log}, nil
}

// RestoreRequest is the input for the restore flow. contentHash is
// REQUIRED for the event_key idempotency column (the worker's
// supersede gate short-circuits when the current metadata.content_hash
// matches — protects against stale-event replay storms).
type RestoreRequest struct {
	AssetID      string       `json:"asset_id"`
	Source       asset.Source `json:"source,omitempty"`
	ContentHash  string       `json:"content_hash"`
	RequestedBy  string       `json:"requested_by,omitempty"`
	Reason       string       `json:"reason,omitempty"`
	RequestedAt  time.Time    `json:"requested_at,omitempty"`
}

// RestoreResult is the output. Emitted is the canonical audit token
// operators reference when replaying; Operation="RESTORE" tags the
// IndexingHandler log so dashboards distinguish restore re-index
// events from fresh ingest (OPERATION="UPSERT") events.
type RestoreResult struct {
	AssetID       string `json:"asset_id"`
	Operation     string `json:"operation"` // always "RESTORE"
	EventEmitted  bool   `json:"event_emitted"`
	CommittedAt   string `json:"committed_at"`
	LifecycleFlip string `json:"lifecycle_flip"` // "deleted" -> "ready"
	ProducerNote  string `json:"producer_note,omitempty"`
}

// Restore routes through Dispatcher.EnqueueAndRestore. The dispatcher
// stamps lifecycle_state='ready' (clears deleted_at) AND emits the
// asset.index.requested.v1 event with operation="RESTORE" in a single
// atomic tx; the IndexingHandler then re-indexes the restored point
// into Qdrant with the v2 channel-per-channel verifier contract.
//
// Use this whenever a previously soft-deleted asset needs to come back
// (operator undo, false-positive cleanup, customer request). The
// canonical content fingerprint hash must match the original ingestion
// for the worker supersede gate to behave idempotently across multiple
// replay attempts of the same restore event.
func (s *Service) Restore(ctx context.Context, req RestoreRequest) (*RestoreResult, error) {
	if s == nil || s.dispatcher == nil {
		return nil, errors.New("restore.Restore: dispatcher not configured (QDRANT-002 close-out invariant)")
	}
	if req.AssetID == "" {
		return nil, errors.New("restore.Restore: asset_id is required")
	}
	if req.ContentHash == "" {
		// contentHash empty is allowed in principle (event_key falls
		// back to assetID-only dedup), but the worker supersede gate
		// will treat every replay as a fresh ingestion. We log a warning
		// so operators notice.
		s.log.Warn("restore.Restore: content_hash is empty — supersede gate disabled for this restore (event_key will only dedup by asset_id)",
			zap.String("asset_id", req.AssetID),
		)
	}
	if err := s.dispatcher.EnqueueAndRestore(ctx, req.AssetID, req.ContentHash); err != nil {
		s.log.Error("restore.Restore: dispatcher.EnqueueAndRestore failed",
			zap.String("asset_id", req.AssetID),
			zap.String("reason", req.Reason),
			zap.Error(err))
		return nil, err
	}
	s.log.Info("restore.Restore: lifecycle_state=ready + outbox event emitted (RESTORE op)",
		zap.String("asset_id", req.AssetID),
		zap.String("source", string(req.Source)),
		zap.String("reason", req.Reason),
		zap.String("requested_by", req.RequestedBy),
	)
	return &RestoreResult{
		AssetID:       req.AssetID,
		Operation:     "RESTORE",
		EventEmitted:  true,
		CommittedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		LifecycleFlip: "deleted -> ready",
		ProducerNote:  "IndexingHandler will re-index the restored point into Qdrant with the per-channel v2 contract",
	}, nil
}
