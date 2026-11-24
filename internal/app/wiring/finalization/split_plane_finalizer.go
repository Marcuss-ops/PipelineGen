package splitplane

import (
	"context"
	"database/sql"
	"fmt"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	jobsfinalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/finalize"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// MediaOutbox is the media-plane enqueue surface required by the two-phase
// finalizer.
type MediaOutbox interface {
	Enqueue(context.Context, assetspersistence.Transaction, string, string, string, string, string) (*pgmedia.EnqueueResult, error)
}

// SplitPlaneFinalizer is the explicit two-plane commit boundary used when
// jobs and media are separate SQLite files. Asset rows/events are committed
// on media first; the job terminal state and execution outbox are committed
// on jobs second. Both phases are idempotent, so a retry after phase two
// failure converges without a second publication.
type SplitPlaneFinalizer struct {
	mediaDB       *sql.DB
	mediaOutbox   MediaOutbox
	assetTx       finalization.AssetFinalizerTx
	jobsFinalizer *jobsfinalizer.Finalizer
}

// NewSplitPlaneFinalizer constructs the two-phase finalizer for the composition
// root. It never opens databases or resolves dependencies itself.
func NewSplitPlaneFinalizer(mediaDB *sql.DB, mediaOutbox MediaOutbox, assetTx finalization.AssetFinalizerTx, jobsFinalizer *jobsfinalizer.Finalizer) *SplitPlaneFinalizer {
	return &SplitPlaneFinalizer{
		mediaDB:       mediaDB,
		mediaOutbox:   mediaOutbox,
		assetTx:       assetTx,
		jobsFinalizer: jobsFinalizer,
	}
}

func (f *SplitPlaneFinalizer) CompleteWithArtifacts(ctx context.Context, req finalization.FinalizationRequest) (*finalization.FinalizationResult, error) {
	if f == nil || f.mediaDB == nil || f.mediaOutbox == nil || f.assetTx == nil || f.jobsFinalizer == nil {
		return nil, fmt.Errorf("split-plane finalizer: incomplete wiring")
	}

	mediaTx, err := f.mediaDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("split-plane finalizer: begin media tx: %w", err)
	}
	refs := make([]finalization.ArtifactRef, 0, len(req.Artifacts))
	for i, artifact := range req.Artifacts {
		ref, events, finalizeErr := f.assetTx.FinalizeAsset(ctx, assetfinalizer.WrapTx(mediaTx), artifact)
		if finalizeErr != nil {
			_ = mediaTx.Rollback()
			return nil, fmt.Errorf("split-plane finalizer: asset[%d] %s: %w", i, artifact.ArtifactID, finalizeErr)
		}
		refs = append(refs, ref)
		for j, event := range events {
			payload := string(event.Payload)
			if payload == "" || payload == "null" {
				payload = "{}"
			}
			if _, enqueueErr := f.mediaOutbox.Enqueue(ctx, mediaTx, event.EventType, event.AggregateID, "", payload, event.EventKey); enqueueErr != nil {
				_ = mediaTx.Rollback()
				return nil, fmt.Errorf("split-plane finalizer: media outbox[%d] %s: %w", j, event.EventType, enqueueErr)
			}
		}
	}
	if err := mediaTx.Commit(); err != nil {
		return nil, fmt.Errorf("split-plane finalizer: commit media tx: %w", err)
	}

	// The jobs finalizer owns the jobs transaction. Artifact rows were already
	// committed on media, so pass an empty artifact list to prevent it from
	// attempting cross-database writes; requested execution-plane events stay
	// in the same jobs transaction as the terminal state.
	jobReq := req
	jobReq.Artifacts = nil
	result, err := f.jobsFinalizer.CompleteWithArtifacts(ctx, jobReq)
	if err != nil {
		return nil, fmt.Errorf("split-plane finalizer: complete jobs tx: %w", err)
	}
	result.ArtifactRefs = refs
	return result, nil
}

var _ finalization.JobFinalizer = (*SplitPlaneFinalizer)(nil)
