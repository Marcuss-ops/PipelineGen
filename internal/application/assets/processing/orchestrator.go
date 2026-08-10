// Package processing — Wave 3 orchestration layer for media processing.
//
// Orchestrator coordinates the four-step pipeline:
//
//  1. Stage   — acquisition.SourceStager downloads the source to local disk.
//  2. Transform — asset.MediaTransformer runs the FFmpeg pipeline.
//  3. Publish — delivery.Publisher uploads to Drive.
//  4. Commit  — AssetCommitter persists to DB/outbox.
//
// The orchestrator is the single owner of this coordination; the
// MediaTransformer is intentionally narrow and does not download,
// upload, or touch the database.
package processing

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Orchestrator coordinates staging, transformation, publishing, and commit.
type Orchestrator interface {
	Process(ctx context.Context, req ProcessRequest) (*ProcessResponse, error)
}

// ProcessRequest describes a media asset to be processed end-to-end.
type ProcessRequest struct {
	AssetID         string
	Name            string
	SourceURL       string
	OutputDir       string
	Filename        string
	FolderID        string
	Destination     delivery.DestinationKey
	Term            string
	Duration        int
	Width           int
	Height          int
	Normalize       *bool
	KeepAudio       bool
	RenditionLayout bool
	// Metadata is optional source-specific enrichment passed to the committer.
	Metadata map[string]any
}

// ProcessResponse is the result of an end-to-end processing run.
type ProcessResponse struct {
	AssetID      string
	Status       string
	LocalPath    string
	FileHash     string
	DriveLink    string
	DownloadLink string
	DriveFileID  string
	Renditions   []asset.RenditionOutput
}

// orchestrator is the concrete Orchestrator implementation.
type orchestrator struct {
	stager      acquisition.SourceStager
	transformer asset.MediaTransformer
	publisher   delivery.Publisher
	committer   AssetCommitter
	log         *zap.Logger
}

// NewOrchestrator creates an Orchestrator.
//
// All dependencies are required except stager, which may be nil to keep
// the legacy "caller already has a local file" path open. When stager
// is nil, the caller must set SourceURL to a local file path and the
// orchestrator will use it directly as LocalPath.
func NewOrchestrator(
	stager acquisition.SourceStager,
	transformer asset.MediaTransformer,
	publisher delivery.Publisher,
	committer AssetCommitter,
	log *zap.Logger,
) Orchestrator {
	if log == nil {
		log = zap.NewNop()
	}
	return &orchestrator{
		stager:      stager,
		transformer: transformer,
		publisher:   publisher,
		committer:   committer,
		log:         log,
	}
}

// Process runs the full staging → transform → publish → commit pipeline.
func (o *orchestrator) Process(ctx context.Context, req ProcessRequest) (*ProcessResponse, error) {
	if req.AssetID == "" {
		return nil, fmt.Errorf("processing.Orchestrator: AssetID is required")
	}
	if req.Name == "" {
		return nil, fmt.Errorf("processing.Orchestrator: Name is required")
	}
	if req.SourceURL == "" {
		return nil, fmt.Errorf("processing.Orchestrator: SourceURL is required")
	}

	resp := &ProcessResponse{
		AssetID: req.AssetID,
		Status:  "failed",
	}

	// Step 1: Stage.
	localPath := req.SourceURL
	var cleanupToken string
	if o.stager != nil {
		stageCtx, err := o.stager.Prepare(ctx, acquisition.PrepareRequest{
			Source: acquisition.SourceRef{
				URL: req.SourceURL,
			},
			CallerRef:      req.AssetID,
			IdempotencyKey: req.AssetID,
		})
		if err != nil {
			return nil, fmt.Errorf("processing.Orchestrator: stage %s: %w", req.SourceURL, err)
		}
		localPath = stageCtx.LocalPath
		cleanupToken = stageCtx.CleanupToken
		defer func() {
			if releaseErr := o.stager.Release(ctx, cleanupToken); releaseErr != nil {
				o.log.Warn("processing.Orchestrator: stage release failed (best-effort)",
					zap.String("cleanup_token", cleanupToken),
					zap.Error(releaseErr))
			}
		}()
	}

	// Step 2: Transform.
	if o.transformer == nil {
		return nil, fmt.Errorf("processing.Orchestrator: MediaTransformer not wired")
	}
	transformInput := &asset.TransformInput{
		ID:              req.AssetID,
		Name:            req.Name,
		LocalPath:       localPath,
		OutputDir:       req.OutputDir,
		Filename:        req.Filename,
		Duration:        req.Duration,
		Width:           req.Width,
		Height:          req.Height,
		Normalize:       req.Normalize,
		KeepAudio:       req.KeepAudio,
		RenditionLayout: req.RenditionLayout,
	}
	transformResult, err := o.transformer.Transform(ctx, transformInput)
	if err != nil {
		return resp, fmt.Errorf("processing.Orchestrator: transform %s: %w", req.AssetID, err)
	}
	if transformResult == nil {
		return resp, fmt.Errorf("processing.Orchestrator: transform %s returned nil", req.AssetID)
	}

	resp.LocalPath = transformResult.LocalPath
	resp.FileHash = transformResult.FileHash
	resp.Renditions = transformResult.Renditions
	resp.Status = "transformed"

	// Step 3: Publish.
	if o.publisher != nil && req.FolderID != "" {
		publishReq := delivery.PublishRequest{
			Destination: req.Destination,
			LocalPath:   transformResult.LocalPath,
			Filename:    transformResult.Filename,
			Description: fmt.Sprintf("PipelineGen processed: %s (id=%s)", req.Name, req.AssetID),
			AssetID:     req.AssetID,
			Group:       req.Term,
			// godlike/08 forward-prevention: route the folder hint through
			// the canonical DestinationFolderID seam on delivery.PublishRequest
			// rather than the banned ParentFolderID. The publisher's
			// DestinationKey mapping owns folder resolution; per-task hints
			// flow through the typed seam.
			DestinationFolderID: req.FolderID,
		}
		publishResult, pubErr := o.publisher.Publish(ctx, publishReq)
		if pubErr != nil {
			// Drive upload failure is not fatal at this stage; the caller
			// can decide whether to persist a local-only record.
			o.log.Warn("processing.Orchestrator: Drive publish failed",
				zap.String("asset_id", req.AssetID),
				zap.Error(pubErr))
			resp.Status = "publish_failed"
		} else {
			resp.DriveLink = publishResult.WebViewLink
			resp.DownloadLink = publishResult.DownloadLink
			resp.DriveFileID = publishResult.FileID
			resp.Status = "published"
		}
	}

	// Step 4: Commit.
	if o.committer != nil {
		pa := finalization.PublishedArtifact{
			ArtifactID:  req.AssetID,
			Kind:        finalization.KindVideo,
			Filename:    transformResult.Filename,
			MIMEType:    "video/mp4",
			SizeBytes:   fileSizeFromPath(transformResult.LocalPath),
			SHA256:      transformResult.FileHash,
			Source:      sourceFromMetadata(req.Metadata),
			Description: req.Name,
			Location: finalization.AssetLocation{
				Provider:     "drive",
				FileID:       resp.DriveFileID,
				WebViewLink:  resp.DriveLink,
				DownloadLink: resp.DownloadLink,
				FolderID:     req.FolderID,
				Action:       finalization.PublishCreated,
			},
			ArtifactMetadata: req.Metadata,
		}
		if err := o.committer.Commit(ctx, pa); err != nil {
			return resp, fmt.Errorf("processing.Orchestrator: commit %s: %w", req.AssetID, err)
		}
		resp.Status = "committed"
	}

	return resp, nil
}

func fileSizeFromPath(path string) int64 {
	if path == "" {
		return 0
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func sourceFromMetadata(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	if v, ok := metadata["source"].(string); ok {
		return v
	}
	return ""
}
