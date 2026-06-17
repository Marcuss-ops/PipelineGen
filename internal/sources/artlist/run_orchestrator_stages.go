package artlist

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/media/models"
	"velox/go-master/pkg/concurrent"
	"velox/go-master/pkg/defaults"
	"velox/go-master/pkg/hashutil"
	"velox/go-master/pkg/textutil"
)

// clipWork pairs a RunTagItem with its processor input.
type clipWork struct {
	item         RunTagItem
	processInput *processor.ProcessInput
}

// pipelineState holds mutable state accumulated across RunTag stages.
type pipelineState struct {
	resp        *RunTagResponse
	workItems   []clipWork
	concurrency int
}

// stageResolveDestination resolves the Drive destination folder for the term.
func (o *RunOrchestratorService) stageResolveDestination(ctx context.Context, req *RunTagRequest, resp *RunTagResponse) (string, error) {
	rootFolderID := defaults.String(req.RootFolderID, o.svc.cfg.Drive.ArtlistFolder())

	dest, err := o.svc.destinationService.ResolveDestination(ctx, resp.Term, rootFolderID)
	if err != nil {
		return "", fmt.Errorf("failed to resolve destination: %w", err)
	}
	resp.TagFolderID = dest.FolderID
	return rootFolderID, nil
}

// stageDiscoverClips discovers clips via live search, saves them to the DB,
// and returns the full SearchResponse for reuse in later stages.
func (o *RunOrchestratorService) stageDiscoverClips(ctx context.Context, req *RunTagRequest, resp *RunTagResponse) (*SearchResponse, error) {
	discoveryResp, err := o.svc.searchService.SearchLiveAndSave(ctx, resp.Term, req.Limit)
	if err != nil {
		return nil, fmt.Errorf("discovery failed: %w", err)
	}
	resp.Found = len(discoveryResp.Clips)

	if len(discoveryResp.Clips) == 0 {
		resp.Error = "no candidates found"
		resp.OK = false
		return discoveryResp, nil
	}
	return discoveryResp, nil
}

// stageBuildProcessInputs builds clip work items and process inputs from discovered clips.
// Returns the work items slice and the resolved root folder ID.
func (o *RunOrchestratorService) stageBuildProcessInputs(ctx context.Context, req *RunTagRequest, resp *RunTagResponse, clips []models.MediaAsset) []clipWork {
	workItems := make([]clipWork, 0, len(clips))

	for _, clip := range clips {
		item := RunTagItem{
			ClipID:       clip.ID,
			Name:         clip.Name,
			DownloadLink: clip.DownloadLink,
			DriveLink:    clip.DriveLink,
			DriveFileID:  clip.DriveFileID,
			LocalPath:    clip.LocalPath,
			FileHash:     clip.FileHash,
		}

		item.ClipID = defaults.String(item.ClipID, clip.ID)
		item.Name = defaults.String(item.Name, clip.Name)
		item.Name = defaults.String(item.Name, item.ClipID)

		sourceURL := defaults.String(clip.DownloadLink, clip.ExternalURL)

		outputDir := ""
		if o.svc.cfg != nil {
			termSlug := textutil.SafeName(resp.Term)
			if len(termSlug) > 20 {
				termSlug = termSlug[:20]
			}
			genID := fmt.Sprintf("%s_%s", termSlug, hashutil.MD5String(resp.Term)[:8])
			outputDir = filepath.Join(o.svc.cfg.Storage.DataDir, "media", "artlist", "general", genID)
		}

		rootFolderID := defaults.String(req.RootFolderID, o.svc.cfg.Drive.ArtlistFolder())

		processInput := &processor.ProcessInput{
			ID:          item.ClipID,
			Name:        item.Name,
			SourceURL:   sourceURL,
			ClipPageURL: clip.ClipPageURL,
			Term:        resp.Term,
			OutputDir:   outputDir,
			Filename:    item.Name + ".mp4",
			FolderID:    resp.TagFolderID,
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
		if o.svc.cfg != nil {
			processInput.Duration = defaults.Int(processInput.Duration, o.svc.cfg.Video.Duration)
		}

		if req.DryRun {
			item.Status = "dry_run"
			resp.Skipped++
			resp.Items = append(resp.Items, item)
			continue
		}

		workItems = append(workItems, clipWork{item: item, processInput: processInput})
	}

	return workItems
}

// stageProcessBatch processes clips in parallel with bounded concurrency.
func (o *RunOrchestratorService) stageProcessBatch(ctx context.Context, ps *pipelineState) error {
	workItems := ps.workItems
	if len(workItems) == 0 {
		return nil
	}

	if o.svc.mediaProcessor == nil {
		return fmt.Errorf("media processor is not configured")
	}

	var mu sync.Mutex
	sem := make(chan struct{}, ps.concurrency)
	var wg sync.WaitGroup

	for _, work := range workItems {
		work := work
		wg.Add(1)
		sem <- struct{}{}
		concurrent.SafeGoFunc("artlist-process-clip", struct {
			w   clipWork
			sem chan struct{}
		}{w: work, sem: sem}, func(arg struct {
			w   clipWork
			sem chan struct{}
		}) {
			defer wg.Done()
			defer func() { <-arg.sem }()

			result, procErr := o.svc.mediaProcessor.Process(ctx, arg.w.processInput)

			mu.Lock()
			defer mu.Unlock()

			if procErr != nil {
				arg.w.item.Status = "media_process_failed"
				arg.w.item.Error = procErr.Error()
				ps.resp.Failed++
				ps.resp.Items = append(ps.resp.Items, arg.w.item)
				return
			}

			arg.w.item.Status = defaults.String(result.Status, "processed")
			arg.w.item.Filename = result.Filename
			arg.w.item.LocalPath = result.LocalPath
			arg.w.item.FileHash = result.FileHash
			arg.w.item.DriveLink = result.DriveLink
			arg.w.item.DriveFileID = result.DriveFileID
			arg.w.item.DownloadLink = result.DownloadLink
			ps.resp.Processed++
			ps.resp.Items = append(ps.resp.Items, arg.w.item)
		})
	}

	wg.Wait()
	return nil
}

// stagePersistResults updates the DB records with pipeline results for each processed clip.
// When the dispatcher is wired, UpsertClip + IndexClip are routed through
// EnqueueAndIndex (atomic media_assets + outbox); otherwise the legacy
// UpsertClip + async clipIndexer.IndexClip path remains.
func (o *RunOrchestratorService) stagePersistResults(ctx context.Context, resp *RunTagResponse) {
	if o.svc.artlistRepo == nil {
		return
	}

	for _, item := range resp.Items {
		if item.Status == "media_process_failed" || item.Status == "dry_run" {
			continue
		}
		if existingClip, err := o.svc.artlistRepo.GetClip(ctx, item.ClipID); err == nil && existingClip != nil {
			existingClip.LocalPath = item.LocalPath
			existingClip.DriveLink = item.DriveLink
			existingClip.DriveFileID = item.DriveFileID
			existingClip.FileHash = item.FileHash
			existingClip.DownloadLink = item.DownloadLink
			existingClip.Status = "processed"
			existingClip.Source = "artlist"
			existingClip.MediaType = "video" // ensure media_type is always set for Artlist clips
			if o.svc.dispatcher != nil {
				if err := o.svc.dispatcher.EnqueueAndIndex(ctx, existingClip, existingClip.FileHash); err != nil {
					o.svc.log.Warn("failed to update clip record after pipeline (dispatcher)",
						zap.String("clip_id", item.ClipID),
						zap.Error(err))
				}
			} else {
				if err := o.svc.artlistRepo.UpsertClip(ctx, existingClip); err != nil {
					o.svc.log.Warn("failed to update clip record after pipeline (legacy)",
						zap.String("clip_id", item.ClipID),
						zap.Error(err))
				}
			}
		}
	}
}

// stageEnrichAsync launches semantic enrichment in the background for processed clips.
func (o *RunOrchestratorService) stageEnrichAsync(ctx context.Context, resp *RunTagResponse) {
	if o.svc.semanticEnricher == nil {
		return
	}
	for _, item := range resp.Items {
		if item.Status == "media_process_failed" || item.Status == "dry_run" {
			continue
		}
		enrichClip := &models.MediaAsset{
			ID:           item.ClipID,
			Name:         item.Name,
			LocalPath:    item.LocalPath,
			DriveLink:    item.DriveLink,
			DriveFileID:  item.DriveFileID,
			DownloadLink: item.DownloadLink,
			Tags:         []string{resp.Term},
		}
		o.svc.semanticEnricher.EnrichAsync(ctx, enrichClip, resp.Term)
	}
}

// stageIndexAsync launches clip indexing in the background for processed clips.
// When the dispatcher is wired, the atomic UpsertClip + IndexClip is already
// done by stagePersistResults (via EnqueueAndIndex); this stage is skipped
// to avoid double-indexing. On the legacy path (dispatcher nil), this is the
// async SafeGoFunc(clipIndexer.IndexClip) that callers used to rely on.
func (o *RunOrchestratorService) stageIndexAsync(ctx context.Context, resp *RunTagResponse) {
	if o.svc.dispatcher != nil {
		// stagePersistResults already handled UpsertClip + IndexClip atomically.
		return
	}
	if o.svc.clipIndexer == nil || !o.svc.clipIndexer.IsEnabled() {
		return
	}
	for _, item := range resp.Items {
		if item.Status == "media_process_failed" || item.Status == "dry_run" {
			continue
		}
		clipID := item.ClipID
		concurrent.SafeGoFunc("artlist-clip-indexer", struct {
			ID  string
			Ctx context.Context
		}{ID: clipID, Ctx: ctx}, func(idxArg struct {
			ID  string
			Ctx context.Context
		}) {
			idxCtx, cancel := context.WithTimeout(context.WithoutCancel(idxArg.Ctx), 30*time.Second)
			defer cancel()
			if err := o.svc.clipIndexer.IndexClip(idxCtx, idxArg.ID); err != nil {
				o.svc.log.Warn("clipindexer failed after pipeline",
					zap.String("clip_id", idxArg.ID),
					zap.Duration("timeout", 30*time.Second),
					zap.Error(err))
			} else {
				o.svc.log.Info("clip indexed for vector search",
					zap.String("clip_id", idxArg.ID))
			}
		})
	}
}

// concurrencyFromRequest determines the concurrency level: default 3, max 10.
func concurrencyFromRequest(req *RunTagRequest) int {
	c := defaults.Int(req.Concurrency, 3)
	if c > 10 {
		return 10
	}
	return c
}
