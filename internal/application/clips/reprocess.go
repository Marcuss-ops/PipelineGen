package clips

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ReprocessUseCase re-downloads, re-processes, and re-uploads a clip.
//
// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): dispatcher
// field added so the use case's media_assets UPSERT (post-reprocess)
// routes through the canonical outbox+tx writer (QDRANT-002 atomicity
// invariant). Strict fail-closed: nil dispatcher surfaces explicitly
// via mutations.ErrDispatcherUnavailable wrap, NOT as a half-written
// asset row that would orphan Qdrant. See
// internal/app/registry_adapters.go::newMutationsDispatcherAdapter.
type ReprocessUseCase struct {
	assetRepo  asset.Repository
	processor  asset.Processor
	dispatcher mutations.AssetMutationDispatcher
}

// NewReprocessUseCase constructs the use case.
//
// PR 7 (June 2026): added `dispatcher` as the 3rd positional arg
// (after proc) so media_assets writes enforce the canonical outbox
// path. Composition-root pre-rejection lives in the wiring site
// (internal/api/assets/clips/handler.go NewHandler) which surfaces
// a configure-time error if dispatcher is nil.
func NewReprocessUseCase(repo asset.Repository, proc asset.Processor, dispatcher mutations.AssetMutationDispatcher) *ReprocessUseCase {
	return &ReprocessUseCase{assetRepo: repo, processor: proc, dispatcher: dispatcher}
}

// ReprocessRequest contains the input for reprocessing a clip.
type ReprocessRequest struct {
	ClipID      string `json:"clip_id"`
	Source      string `json:"source"`
	Force       bool   `json:"force"`
	UploadDrive bool   `json:"upload_drive"`
	Normalize   *bool  `json:"normalize"`
}

// ReprocessResult contains the output after reprocessing.
type ReprocessResult struct {
	ClipID       string `json:"clip_id"`
	Source       string `json:"source"`
	Status       string `json:"status"`
	LocalPath    string `json:"local_path"`
	FileHash     string `json:"file_hash"`
	DriveLink    string `json:"drive_link"`
	DownloadLink string `json:"download_link"`
	ProcessedAt  string `json:"processed_at"`
}

// Execute reprocesses the clip and returns the result.
func (uc *ReprocessUseCase) Execute(ctx context.Context, req ReprocessRequest) (*ReprocessResult, error) {
	if uc.assetRepo == nil {
		return nil, fmt.Errorf("asset repository not available")
	}
	if uc.processor == nil {
		return nil, fmt.Errorf("media processor not configured")
	}

	clip, err := uc.assetRepo.Get(ctx, req.ClipID)
	if err != nil {
		return nil, fmt.Errorf("clip not found: %w", err)
	}
	if clip == nil {
		return nil, fmt.Errorf("clip not found")
	}

	// Build ProcessInput from clip data
	processInput := &asset.ProcessInput{
		ID:        clip.ID,
		Name:      clip.Name,
		SourceURL: clip.SourceURL,
		FolderID:  clip.FolderID(),
		Duration:  int(clip.Duration.Milliseconds()),
		Metadata: map[string]any{
			"source": req.Source,
			"tags":   clip.Tags,
		},
	}

	result, err := uc.processor.Process(ctx, processInput)
	if err != nil {
		return nil, fmt.Errorf("reprocess failed: %w", err)
	}

	// Update clip with result
	clip.SetLocalPath(result.LocalPath)
	clip.SetFileHash(result.FileHash)
	if result.DriveLink != "" {
		clip.SetDriveLink(result.DriveLink)
	}
	if result.DownloadLink != "" {
		clip.SetDownloadLink(result.DownloadLink)
	}
	clip.UpdatedAt = time.Now()

	// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): route the
	// media_assets UPSERT through the canonical mutations.AssetMutationDispatcher
	// so the QDRANT-002 atomicity invariant (media_assets UPSERT + outbox_events
	// INSERT in one tx) applies uniformly to reprocess flows.
	//
	// Strict fail-closed: nil dispatcher surfaces an explicit error
	// (the POST /clips/{id}/reprocess call returns 503-equivalent),
	// NOT a half-written asset row. contentHash is the post-reprocess
	// fingerprint from result.FileHash; mirrors the v1 supersede-gate
	// semantics (QDRANT-002 item F: source_version on index.requested.v1).
	if uc.dispatcher == nil {
		return nil, fmt.Errorf("reprocess dispatcher not configured (QDRANT-asset-mutation isolation required): %w", mutations.ErrDispatcherUnavailable)
	}
	if err := uc.dispatcher.EnqueueAndIndex(ctx, clip, result.FileHash); err != nil {
		return nil, fmt.Errorf("dispatcher enqueue: %w", err)
	}

	return &ReprocessResult{
		ClipID:       req.ClipID,
		Source:       req.Source,
		Status:       result.Status,
		LocalPath:    result.LocalPath,
		FileHash:     result.FileHash,
		DriveLink:    result.DriveLink,
		DownloadLink: result.DownloadLink,
		ProcessedAt:  timeutil.FormatRFC3339(time.Now()),
	}, nil
}
