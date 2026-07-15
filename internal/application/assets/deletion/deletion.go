// Package deletion owns deletion request, completion and maintenance use cases.
package deletion

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"go.uber.org/zap"
)

// DispatcherPort is the narrow state-machine entrypoint used by deletion.
type DispatcherPort interface {
	EnqueueDriveDelete(ctx context.Context, assetID string, permanently bool) error
}

// DriveGoneChecker confirms the external file was removed before completion.
type DriveGoneChecker interface {
	CheckDriveGone(ctx context.Context, fileID string) (bool, error)
}

// CompletionTxRunner performs the atomic media row + outbox cleanup.
type CompletionTxRunner interface {
	RunCompletionTx(ctx context.Context, assetID string) error
}

// AssetTreeCleaner is the exact projection cleanup surface used by DeleteClip.
type AssetTreeCleaner interface {
	DeleteByAssetID(ctx context.Context, source, assetID string) error
	DeleteNode(ctx context.Context, nodeID string) error
}

// AssetIndexReader is the exact read surface used by orphan cleanup.
type AssetIndexReader interface {
	ListAll(ctx context.Context) ([]*assetindex.AssetRecord, error)
}

// RepositoryPorts owns source lookup and source-specific deletion.
type RepositoryPorts struct {
	Artlist   *assets.ClipsRepository
	Clips     *assets.ClipsRepository
	Stock     *assets.ClipsRepository
	Voiceover *assets.VoiceoversRepository
	Images    *assets.ImagesRepository
}

// MutationPorts owns deletion request dispatch and tree projection cleanup.
type MutationPorts struct {
	Dispatcher DispatcherPort
	AssetTree  AssetTreeCleaner
}

// CompletionPorts owns the post-state-machine closeout.
type CompletionPorts struct {
	DriveGone DriveGoneChecker
	Tx        CompletionTxRunner
}

// MaintenancePorts owns the orphan-file reference projection.
type MaintenancePorts struct {
	AssetIndex AssetIndexReader
}

// Dependencies contains four operational capabilities plus observability.
type Dependencies struct {
	Repositories RepositoryPorts
	Mutation     MutationPorts
	Completion   CompletionPorts
	Maintenance  MaintenancePorts
	Log          *zap.Logger
}

// DeletionService stores capability boundaries directly. Request routing,
// completion and maintenance no longer share a flat dependency field list.
type DeletionService struct {
	repositories RepositoryPorts
	mutation     MutationPorts
	completion   CompletionPorts
	maintenance  MaintenancePorts
	log          *zap.Logger
}

func NewDeletionService(deps Dependencies) *DeletionService {
	log := deps.Log
	if log == nil {
		log = zap.NewNop()
	}
	return &DeletionService{
		repositories: deps.Repositories,
		mutation:     deps.Mutation,
		completion:   deps.Completion,
		maintenance:  deps.Maintenance,
		log:          log,
	}
}
