package clips

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ReprocessUseCase re-downloads, re-processes, and re-uploads a clip.
type ReprocessUseCase struct {
	assetRepo asset.Repository
	processor asset.Processor
}

// NewReprocessUseCase constructs the use case.
func NewReprocessUseCase(repo asset.Repository, proc asset.Processor) *ReprocessUseCase {
	return &ReprocessUseCase{assetRepo: repo, processor: proc}
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

	if err := uc.assetRepo.Upsert(ctx, clip); err != nil {
		return nil, fmt.Errorf("failed to update clip: %w", err)
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
