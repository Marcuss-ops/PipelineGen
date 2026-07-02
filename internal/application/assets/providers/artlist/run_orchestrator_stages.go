package artlist

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	defaults "github.com/Marcuss-ops/PipelineGen/pkg/defaults"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
)

// clipWork pairs a RunTagItem with its processor input.
type clipWork struct {
	item            RunTagItem
	processInput    *asset.ProcessInput
	stagedAsset     *assets.StagedAsset // set when shared SourceStager pre-staged the source
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
func (o *RunOrchestratorService) stageBuildProcessInputs(ctx context.Context, req *RunTagRequest, resp *RunTagResponse, clips []asset.Asset) []clipWork {
	workItems := make([]clipWork, 0, len(clips))

	for _, clip := range clips {
		item := RunTagItem{
			ClipID:       clip.ID,
			Name:         clip.Name,
			DownloadLink: clip.GetMetadataString("_download_link"),
			DriveLink:    clip.GetMetadataString("_drive_link"),
			DriveFileID:  clip.GetMetadataString("_drive_file_id"),
			LocalPath:    clip.GetMetadataString("_local_path"),
			FileHash:     clip.GetMetadataString("_file_hash"),
		}

		item.ClipID = defaults.String(item.ClipID, clip.ID)
		item.Name = defaults.String(item.Name, clip.Name)
		item.Name = defaults.String(item.Name, item.ClipID)

		sourceURL := defaults.String(item.DownloadLink, clip.ExternalURL())

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

		processInput := &asset.ProcessInput{
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

			// Track asset lifecycle: mark download step as running.
			if o.svc.assetProcessing != nil {
				if err := o.svc.assetProcessing.Start(ctx, arg.w.item.ClipID, "download"); err != nil {
					o.svc.log.Warn("asset_processing.Start failed",
						zap.String("clip_id", arg.w.item.ClipID),
						zap.Error(err))
				}
			}

			// Step 9/12 wire-up (July 2026): invoke the shared SourceStager
			// port before mediaProcessor.Process when wired. This locks
			// Artlist onto the SAME shared port YouTube and stock use,
			// and provides:
			//   - Early connectivity probe (stager.StageSource FailureCode
			//     surfaces a typed pre-flight diagnostic before the heavier
			//     transcode pipeline starts).
			//   - Observability for "URL was reachable at download time"
			//     (zap log carries staged LocalPath + bytes).
			//   - Single canonical path for the "download" surface (the
			//     stager wraps Artlist Downloader port → composes with the
			//     same Downloader that mediaProcessor internally uses).
			//
			// KNOWN LIMITATION (July 2026): asset.ProcessInput has no
			// LocalPath field, so mediaProcessor still performs its own
			// download after this pre-flight. The stager invocation IS
			// bandwidth-waste when both succeed; the staged file is
			// cleaned up immediately. A future refactor (FASE 7+) that
			// adds LocalPath to ProcessInput would let the stager download
			// REPLACE the mediaProcessor download. Until then the
			// pre-flight is an honest observability + port-usage probe.
			//
			// Thread safety: both arg.w.stagedAsset (per-goroutine clipWork
			// copy from the outer `work := work` loop capture) and
			// zap.Logger are thread-safe; no extra mutex is needed here.
			if o.svc.stager != nil && arg.w.processInput.SourceURL != "" {
				staged, stageErr := o.svc.stager.StageSource(ctx, assets.SourceRef{
					URL: arg.w.processInput.SourceURL,
				})
				if stageErr != nil {
					o.svc.log.Warn("shared SourceStager pre-stage failed (continuing with mediaProcessor)",
						zap.String("clip_id", arg.w.item.ClipID),
						zap.String("source_url", arg.w.processInput.SourceURL),
						zap.Error(stageErr))
				} else {
					arg.w.stagedAsset = staged
					o.svc.log.Info("shared SourceStager pre-staged source",
						zap.String("clip_id", arg.w.item.ClipID),
						zap.String("source_url", arg.w.processInput.SourceURL),
						zap.String("local_path", staged.LocalPath),
						zap.Int64("bytes", staged.Bytes))
				}
			}
			if arg.w.stagedAsset != nil {
				defer func(staged *assets.StagedAsset) {
					if cleanupErr := o.svc.stager.Cleanup(ctx, staged); cleanupErr != nil {
						o.svc.log.Warn("shared SourceStager cleanup failed (best-effort)",
							zap.String("local_path", staged.LocalPath),
							zap.Error(cleanupErr))
					}
				}(arg.w.stagedAsset)
			}

			result, procErr := o.svc.mediaProcessor.Process(ctx, arg.w.processInput)

			mu.Lock()
			defer mu.Unlock()

			if procErr != nil {
				// Track asset lifecycle: mark download step as failed.
				if o.svc.assetProcessing != nil {
					if err := o.svc.assetProcessing.Fail(ctx, arg.w.item.ClipID, "download", procErr.Error()); err != nil {
						o.svc.log.Warn("asset_processing.Fail failed",
							zap.String("clip_id", arg.w.item.ClipID),
							zap.Error(err))
					}
				}
				arg.w.item.Status = "media_process_failed"
				arg.w.item.Error = procErr.Error()
				ps.resp.Failed++
				ps.resp.Items = append(ps.resp.Items, arg.w.item)
				return
			}

			// Track asset lifecycle: mark download step as completed.
			if o.svc.assetProcessing != nil {
				if err := o.svc.assetProcessing.Complete(ctx, arg.w.item.ClipID, "download"); err != nil {
					o.svc.log.Warn("asset_processing.Complete failed",
						zap.String("clip_id", arg.w.item.ClipID),
						zap.Error(err))
				}
			}

			// Track asset version: create version record for the processed file.
			if o.svc.assetVersions != nil && result.FileHash != "" {
				fileSize := fileSizeFromPath(result.LocalPath)
				v := &asset.Version{
					AssetID:       arg.w.item.ClipID,
					FileHash:      result.FileHash,
					FileSizeBytes: fileSize,
					MimeType:      "video/mp4",
					MetadataJSON:  `{"pipeline":"artlist","source":"download","createdBy":"artlist-pipeline"}`,
				}
				if err := o.svc.assetVersions.Append(ctx, v); err != nil {
					o.svc.log.Warn("asset_versions.Append failed",
						zap.String("clip_id", arg.w.item.ClipID),
						zap.Error(err))
				}
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
// Routing goes through dispatchBridge so the canonical-vs-legacy decision
// lives in one place. See dispatch_bridge.go for canonical semantics.
func (o *RunOrchestratorService) stagePersistResults(ctx context.Context, resp *RunTagResponse) {
	if o.svc.assetStore == nil {
		return
	}

	for _, item := range resp.Items {
		if item.Status == "media_process_failed" || item.Status == "dry_run" {
			continue
		}
		existingClip, err := o.svc.assetStore.Get(ctx, item.ClipID)
		if err != nil {
			o.svc.log.Warn("stagePersistResults: artlistRepo.GetClip failed",
				zap.String("clip_id", item.ClipID), zap.Error(err))
			continue
		}
		if existingClip == nil {
			o.svc.log.Debug("stagePersistResults: clip absent in DB",
				zap.String("clip_id", item.ClipID))
			continue
		}
		existingClip.SetLocalPath(item.LocalPath)
		existingClip.SetDriveLink(item.DriveLink)
		existingClip.SetDriveFileID(item.DriveFileID)
		existingClip.SetFileHash(item.FileHash)
		existingClip.SetDownloadLink(item.DownloadLink)
		existingClip.SetMetadataString("status", "processed")
		existingClip.LifecycleState = asset.StateActive
		existingClip.Source = "artlist"
		existingClip.MediaType = "video" // ensure media_type is always set for Artlist clips
		bridge, err := o.svc.newDispatchBridge()
		if err != nil {
			o.svc.log.Warn("stagePersistResults: dispatcher not wired", zap.Error(err))
			continue
		}
		if err := bridge.Dispatch(ctx, existingClip, existingClip.FileHash()); err != nil {
			o.svc.log.Warn("stagePersistResults: dispatch failed",
				zap.String("clip_id", existingClip.ID), zap.Error(err))
		}
	}
}

// stageIndexAsync is a no-op. The canonical dispatcher (required) handles
// indexing atomically in stagePersistResults via Dispatch. This stage
// exists only as a documented no-op for callers that used to rely on the
// legacy SafeGoFunc(Indexer.IndexClip) path.
func (o *RunOrchestratorService) stageIndexAsync(_ context.Context, _ *RunTagResponse) {
}

// concurrencyFromRequest determines the concurrency level: default 3, max 10.
func concurrencyFromRequest(req *RunTagRequest) int {
	c := defaults.Int(req.Concurrency, 3)
	if c > 10 {
		return 10
	}
	return c
}

// fileSizeFromPath returns the file size in bytes, or 0 if the file cannot be stat'd.
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
