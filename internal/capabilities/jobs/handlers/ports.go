package handlers

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// VectorPointDeleter is the narrow pgvector port consumed by
// IndexDeleteHandler. Production concrete: *pgvector.IndexWriter.
type VectorPointDeleter interface {
	DeleteAssetPoints(ctx context.Context, assetIDs []string) error
}

// DriveDeleter is the narrow Drive side-effect port consumed by
// DriveDeleteHandler.
type DriveDeleter interface {
	Trash(ctx context.Context, fileID string) error
	Delete(ctx context.Context, fileID string) error
}

// StateAdvancer atomically advances a lifecycle state and emits the next
// outbox event.
type StateAdvancer interface {
	AdvanceAndEmit(
		ctx context.Context,
		assetID string,
		expectedState, newState asset.LifecycleState,
		eventType string,
		payloadJSON []byte,
		eventKey string,
	) error
}

// LifecycleStateReader is the pre-flight read port for DriveDeleteHandler.
type LifecycleStateReader interface {
	GetClip(ctx context.Context, id string) (*asset.Asset, error)
}

// ClipsLifecycleStateWriter is the lifecycle-state write port for
// DriveDeleteHandler.
type ClipsLifecycleStateWriter interface {
	SetLifecycleState(ctx context.Context, id string, state asset.LifecycleState) error
}
