// Package deletion — application-level wrapper around outbox.Dispatcher
// for asset deletion flows.
//
// QDRANT-002 close-out (June 2026): consumers for the canonical
// Dispatcher.EnqueueAndDelete + Dispatcher.EnqueueAndHardDelete methods.
// Previous CommitWave (PR7) implemented the dispatcher side; this file
// implements the application-side callers so any handler / admin tool
// wanting to soft-delete or hard-delete a clip has a single typed entry
// point instead of reaching into infrastructure/outbox directly.
//
// The two methods are intentionally distinct:
//
//   - SoftDelete: lifecycle_state → 'deleted' via SoftDeleteTx, emits
//     asset.index.delete_requested.v1 (IndexDeleteHandler completes
//     Qdrant delete + state flip in the handler tx).
//   - HardDelete: physically removes the media_assets row + related
//     rows in the producer tx and emits asset.index.delete_requested.v1
//     atomically with the physical removal. RECOVERABLE only from backup
//     — use for explicit operator-driven permanent removal.
//
// Both methods route through the dispatcher (the canonical write path
// per AGENTS.md Migration policy: zero-legacy means every write must
// emit an outbox_event so the Qdrant vector stays in sync).
package deletion

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// DispatcherPort is the minimum surface the Service needs from
// outbox.Dispatcher. Exposed as an interface so tests can inject a
// fake without pulling the full outbox package.
type DispatcherPort interface {
	EnqueueAndDelete(ctx context.Context, assetID string) error
	EnqueueAndHardDelete(ctx context.Context, assetID string) error
}

// LogPort is the minimum logging surface required by Service. Sized
// to zap.Logger's native signature (zap.Field variadic) so zap.NewNop()
// satisfies it directly without a custom adapter.
type LogPort interface {
	Warn(msg string, fields ...zap.Field)
	Error(msg string, fields ...zap.Field)
}

// Service wraps DispatcherPort with logging + validation for the
// two canonical deletion paths. Composition root wires this in
// BuildOutboxBundle alongside the dispatcher (see
// internal/app/build_bundles_process.go).
type Service struct {
	dispatcher DispatcherPort
	log        LogPort
}

// NewService constructs a deletion Service. dispatcher is REQUIRED —
// the canonical write path MUST emit an outbox_event, and there is no
// meaningful "deletion" without it. nil at construction is a wiring
// bug caught immediately.
func NewService(dispatcher DispatcherPort, log LogPort) (*Service, error) {
	if dispatcher == nil {
		return nil, errors.New("deletion.NewService: dispatcher is required (QDRANT-002 close-out — every delete must route through outbox.Dispatcher.EnqueueAnd*)")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{dispatcher: dispatcher, log: log}, nil
}

// DeleteRequest is the input for both SoftDelete and HardDelete.
type DeleteRequest struct {
	AssetID      string        `json:"asset_id"`
	Source       asset.Source  `json:"source,omitempty"`
	RequestedBy  string        `json:"requested_by,omitempty"`
	Reason       string        `json:"reason,omitempty"`
	RequestedAt  time.Time     `json:"requested_at,omitempty"`
}

// DeleteResult is the output of SoftDelete/HardDelete. The outbox
// event_id is the canonical audit token operators reference when
// replaying; the operation distinguishes soft vs hard in dashboards.
type DeleteResult struct {
	AssetID       string `json:"asset_id"`
	Operation     string `json:"operation"` // "soft-delete" | "hard-delete"
	EventEmitted  bool   `json:"event_emitted"`
	CommittedAt   string `json:"committed_at"`
	ProducerNote  string `json:"producer_note,omitempty"`
}

// SoftDelete routes through Dispatcher.EnqueueAndDelete. The
// dispatcher stamps index_state=DELETE_PENDING and emits the
// asset.index.delete_requested.v1 event in a single tx; the
// IndexDeleteHandler completes the picture (Qdrant DeletePoints +
// SoftDelete + state flip) in the consumer path.
//
// Use this for the canonical "user-facing delete" flow — the asset
// is recoverable from the SQLite row (lifecycle_state='deleted')
// and the Qdrant point is cleaned up by the worker.
func (s *Service) SoftDelete(ctx context.Context, req DeleteRequest) (*DeleteResult, error) {
	if s == nil || s.dispatcher == nil {
		return nil, errors.New("deletion.SoftDelete: dispatcher not configured (QDRANT-002 close-out invariant)")
	}
	if req.AssetID == "" {
		return nil, errors.New("deletion.SoftDelete: asset_id is required")
	}
	if err := s.dispatcher.EnqueueAndDelete(ctx, req.AssetID); err != nil {
		s.log.Error("deletion.SoftDelete: dispatcher.EnqueueAndDelete failed",
			zap.String("asset_id", req.AssetID),
			zap.String("reason", req.Reason),
			zap.Error(err))
		return nil, err
	}
	s.log.Warn("deletion.SoftDelete: index_state=DELETE_PENDING + outbox event emitted",
		zap.String("asset_id", req.AssetID),
		zap.String("source", string(req.Source)),
		zap.String("reason", req.Reason),
		zap.String("requested_by", req.RequestedBy),
	)
	return &DeleteResult{
		AssetID:      req.AssetID,
		Operation:    "soft-delete",
		EventEmitted: true,
		CommittedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		ProducerNote: "DELETE_PENDING stamped on media_assets.index_state; IndexDeleteHandler will complete Qdrant cleanup + lifecycle_state=\"deleted\"",
	}, nil
}

// HardDelete routes through Dispatcher.EnqueueAndHardDelete. The
// dispatcher emits the asset.index.delete_requested.v1 event AND
// physically removes the media_assets row + asset_locations +
// asset_processing + asset_versions in a single atomic tx. After
// commit, the row is GONE from SQLite — recovery requires a backup.
//
// Use this only for explicit operator-driven permanent removal (GDPR
// right-to-erasure, asset deletion with no undo path, etc.).
func (s *Service) HardDelete(ctx context.Context, req DeleteRequest) (*DeleteResult, error) {
	if s == nil || s.dispatcher == nil {
		return nil, errors.New("deletion.HardDelete: dispatcher not configured (QDRANT-002 close-out invariant)")
	}
	if req.AssetID == "" {
		return nil, errors.New("deletion.HardDelete: asset_id is required")
	}
	if err := s.dispatcher.EnqueueAndHardDelete(ctx, req.AssetID); err != nil {
		s.log.Error("deletion.HardDelete: dispatcher.EnqueueAndHardDelete failed",
			zap.String("asset_id", req.AssetID),
			zap.String("reason", req.Reason),
			zap.Error(err))
		return nil, err
	}
	s.log.Warn("deletion.HardDelete: physical media_assets row removed + outbox event emitted",
		zap.String("asset_id", req.AssetID),
		zap.String("source", string(req.Source)),
		zap.String("reason", req.Reason),
		zap.String("requested_by", req.RequestedBy),
	)
	return &DeleteResult{
		AssetID:      req.AssetID,
		Operation:    "hard-delete",
		EventEmitted: true,
		CommittedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		ProducerNote: "media_assets row + asset_locations + asset_processing + asset_versions physically removed; IndexDeleteHandler will handle Qdrant cleanup if the point still exists",
	}, nil
}
