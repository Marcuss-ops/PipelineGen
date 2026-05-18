package artlist

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"velox/go-master/internal/core/processor"
	driveutil "velox/go-master/internal/upload/drive"
)

// RunOrchestratorService coordina l'esecuzione dei run Artlist
type RunOrchestratorService struct {
	svc *Service
}

// NewRunOrchestratorService crea un nuovo orchestratore di run
func NewRunOrchestratorService(svc *Service) *RunOrchestratorService {
	return &RunOrchestratorService{svc: svc}
}

// GetRunTag ottiene lo stato di un run esistente
func (o *RunOrchestratorService) GetRunTag(ctx context.Context, runID string) (*RunTagResponse, error) {
	if runID == "" {
		return nil, fmt.Errorf("runID is required")
	}

	job, err := o.svc.jobAdapter.GetJobByRunID(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("failed to get job for run %s: %w", runID, err)
	}

	if job == nil {
		return nil, fmt.Errorf("job not found for run %s", runID)
	}

	resp := &RunTagResponse{
		OK:     true,
		RunID:  runID,
		Term:   job.ActiveKey,
		Status: string(job.Status),
	}

	return resp, nil
}

// RunTag esegue la pipeline Artlist per un termine di ricerca
func (o *RunOrchestratorService) RunTag(ctx context.Context, req *RunTagRequest) (*RunTagResponse, error) {
	resp := &RunTagResponse{
		OK:        true,
		Term:      strings.TrimSpace(req.Term),
		Strategy:  strings.TrimSpace(req.Strategy),
		DryRun:    req.DryRun,
		Requested: req.Limit,
		StartedAt: func() *string { t := time.Now().UTC().Format(time.RFC3339); return &t }(),
	}

	if resp.Term == "" {
		resp.OK = false
		resp.Error = "term is required"
		return resp, fmt.Errorf("term is required")
	}

	rootFolderID := req.RootFolderID
	if rootFolderID == "" {
		rootFolderID = driveutil.ResolveArtlistRootFolderID(o.svc.cfg)
	}

	dest, err := o.svc.destinationService.ResolveDestination(ctx, resp.Term, rootFolderID)
	if err != nil {
		resp.OK = false
		resp.Error = fmt.Sprintf("failed to resolve destination: %v", err)
		return resp, err
	}
	resp.TagFolderID = dest.FolderID

	discoveryResp, err := o.svc.searchService.SearchLiveAndSave(ctx, resp.Term, req.Limit)
	if err != nil {
		resp.OK = false
		resp.Error = fmt.Sprintf("discovery failed: %v", err)
		return resp, err
	}
	resp.Found = len(discoveryResp.Clips)

	if len(discoveryResp.Clips) == 0 {
		resp.OK = false
		resp.Error = "no candidates found"
		return resp, nil
	}

	if o.svc.mediaProcessor == nil {
		resp.OK = false
		resp.Error = "media processor is not configured"
		return resp, fmt.Errorf("media processor is not configured")
	}

	processedCount := 0
	for _, clip := range discoveryResp.Clips {
		item := RunTagItem{
			ClipID:       clip.ID,
			Name:         clip.Name,
			DownloadLink: clip.DownloadLink,
			DriveLink:    clip.DriveLink,
			DriveFileID:  clip.DriveFileID,
			LocalPath:    clip.LocalPath,
			FileHash:     clip.FileHash,
		}

		if item.ClipID == "" {
			item.ClipID = clip.ID
		}
		if item.Name == "" {
			item.Name = clip.Name
		}
		if item.Name == "" {
			item.Name = item.ClipID
		}

		sourceURL := clip.DownloadLink
		if sourceURL == "" {
			sourceURL = clip.ExternalURL
		}

		outputDir := ""
		if o.svc.cfg != nil {
			outputDir = filepath.Join(o.svc.cfg.Storage.DataDir, "artlist", sanitizeFolderName(resp.Term))
		}

		processInput := &processor.ProcessInput{
			ID:          item.ClipID,
			Name:        item.Name,
			SourceURL:   sourceURL,
			Term:        resp.Term,
			OutputDir:   outputDir,
			Filename:    item.Name + ".mp4",
			FolderID:    dest.FolderID,
			Duration:    req.ClipDuration,
			Width:       req.Width,
			Height:      req.Height,
			DriveFileID: item.DriveFileID,
			Metadata: map[string]any{
				"source":         "artlist",
				"strategy":       req.Strategy,
				"root_folder_id": rootFolderID,
			},
		}
		if processInput.Duration <= 0 && o.svc.cfg != nil {
			processInput.Duration = o.svc.cfg.Video.Duration
		}

		if req.DryRun {
			item.Status = "dry_run"
			resp.Skipped++
			resp.Items = append(resp.Items, item)
			continue
		}

		result, procErr := o.svc.mediaProcessor.Process(ctx, processInput)
		if procErr != nil {
			item.Status = "media_process_failed"
			item.Error = procErr.Error()
			resp.Failed++
			resp.Items = append(resp.Items, item)
			continue
		}

		item.Status = result.Status
		if item.Status == "" {
			item.Status = "processed"
		}
		item.Filename = result.Filename
		item.LocalPath = result.LocalPath
		item.FileHash = result.FileHash
		item.DriveLink = result.DriveLink
		item.DriveFileID = result.DriveFileID
		item.DownloadLink = result.DownloadLink
		resp.Processed++
		processedCount++
		resp.Items = append(resp.Items, item)
	}

	if processedCount == 0 && resp.Failed > 0 && resp.Skipped == 0 {
		resp.OK = false
		resp.Error = "all artlist items failed"
	}

	resp.CompletedAt = func() *string { t := time.Now().UTC().Format(time.RFC3339); return &t }()

	return resp, nil
}
