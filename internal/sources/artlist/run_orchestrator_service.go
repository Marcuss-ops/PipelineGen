package artlist

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/pkg/hashutil"
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

	return o.svc.jobAdapter.jobToResponse(job), nil
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

	// Determine concurrency: default 3, max 10
	concurrency := req.Concurrency
	if concurrency <= 0 {
		concurrency = 3
	} else if concurrency > 10 {
		concurrency = 10
	}

	// Build clip work items first (synchronous — fast)
	type clipWork struct {
		item        RunTagItem
		processInput *processor.ProcessInput
	}
	workItems := make([]clipWork, 0, len(discoveryResp.Clips))

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
			termSlug := sanitizeFolderName(resp.Term)
			if len(termSlug) > 20 {
				termSlug = termSlug[:20]
			}
			genID := fmt.Sprintf("%s_%s", termSlug, hashutil.MD5String(resp.Term)[:8])
			outputDir = filepath.Join(o.svc.cfg.Storage.DataDir, "media", "artlist", "general", genID)
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

		workItems = append(workItems, clipWork{item: item, processInput: processInput})
	}

	if len(workItems) == 0 {
		return resp, nil
	}

	// Process clips in PARALLEL using a semaphore pattern
	// Each goroutine gets its own item slice for safe concurrent writes
	processedCount := 0
	var mu sync.Mutex
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, work := range workItems {
		wg.Add(1)
		sem <- struct{}{} // acquire slot
		go func(w clipWork) {
			defer wg.Done()
			defer func() { <-sem }() // release slot

			result, procErr := o.svc.mediaProcessor.Process(ctx, w.processInput)

			mu.Lock()
			defer mu.Unlock()

			if procErr != nil {
				w.item.Status = "media_process_failed"
				w.item.Error = procErr.Error()
				resp.Failed++
				resp.Items = append(resp.Items, w.item)
				return
			}

			w.item.Status = result.Status
			if w.item.Status == "" {
				w.item.Status = "processed"
			}
			w.item.Filename = result.Filename
			w.item.LocalPath = result.LocalPath
			w.item.FileHash = result.FileHash
			w.item.DriveLink = result.DriveLink
			w.item.DriveFileID = result.DriveFileID
			w.item.DownloadLink = result.DownloadLink
			resp.Processed++
			resp.Items = append(resp.Items, w.item)

			// Arricchimento semantico in background
			if o.svc.semanticEnricher != nil {
				enrichClip := &models.MediaAsset{
					ID:           w.item.ClipID,
					Name:         w.item.Name,
					LocalPath:    w.item.LocalPath,
					DriveLink:    w.item.DriveLink,
					DriveFileID:  w.item.DriveFileID,
					DownloadLink: w.item.DownloadLink,
					Tags:         []string{resp.Term},
				}
				o.svc.semanticEnricher.EnrichAsync(enrichClip, resp.Term)
			}
		}(work)
	}

	wg.Wait()

	// Use the processed count for the error check
	// (resp.Processed is already incremented inside goroutines)
	mu.Lock()
	processedCount = resp.Processed
	mu.Unlock()

	o.svc.log.Info("artlist run completed",
		zap.String("term", resp.Term),
		zap.Int("concurrency", concurrency),
		zap.Int("found", resp.Found),
		zap.Int("processed", processedCount),
		zap.Int("failed", resp.Failed),
		zap.Int("skipped", resp.Skipped),
	)

	if processedCount == 0 && resp.Failed > 0 && resp.Skipped == 0 {
		resp.OK = false
		resp.Error = "all artlist items failed"
	}

	resp.CompletedAt = func() *string { t := time.Now().UTC().Format(time.RFC3339); return &t }()

	return resp, nil
}
