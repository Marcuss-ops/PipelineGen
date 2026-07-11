package artlist

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	defaults "github.com/Marcuss-ops/PipelineGen/pkg/defaults"

	"go.uber.org/zap"
)

// clipWork pairs a RunTagItem with its processor input.
type clipWork struct {
	item         RunTagItem
	processInput *asset.ProcessInput
	stagedAsset  *assets.StagedAsset // set when shared SourceStager pre-staged the source
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
			externalID := DeriveExternalAssetID(item.ClipID, sourceURL)
			layout := NewStorageLayout(o.svc.cfg.Storage.DataDir, "artlist", externalID)
			outputDir = layout.BaseDir()
		}

		rootFolderID := defaults.String(req.RootFolderID, o.svc.cfg.Drive.ArtlistFolder())

		processInput := &asset.ProcessInput{
			ID:              item.ClipID,
			Name:            item.Name,
			SourceURL:       sourceURL,
			ClipPageURL:     clip.ClipPageURL,
			Term:            resp.Term,
			OutputDir:       outputDir,
			Filename:        item.Name + ".mp4",
			FolderID:        resp.TagFolderID,
			Duration:        req.ClipDuration,
			Width:           req.Width,
			Height:          req.Height,
			DriveFileID:     item.DriveFileID,
			RenditionLayout: true,
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
					// Step 9/12 wire-up (July 2026): now that the Processor honors
					// ProcessInput.LocalPath (asset/processor.go + processor.go),
					// the staged file is NOT just a probe — mediaProcessor.Process
					// will SKIP its internal downloadStep and use this file as
					// the raw input to ffmpeg normalize. Eliminates the redundant
					// bandwidth double-download that the pre-refactor probe caused.
					arg.w.stagedAsset = staged
					arg.w.processInput.LocalPath = staged.LocalPath
					o.svc.log.Info("shared SourceStager replaced mediaProcessor download",
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

			arg.w.item.Status = defaults.String(result.Status, "processed")
			arg.w.item.Filename = result.Filename
			arg.w.item.LocalPath = result.LocalPath
			arg.w.item.FileHash = result.FileHash
			arg.w.item.DriveLink = result.DriveLink
			arg.w.item.DriveFileID = result.DriveFileID
			arg.w.item.DownloadLink = result.DownloadLink
			arg.w.item.Renditions = result.Renditions
			// PR-ARTLIST-PERSIST-FIX (2026-07-04): ps.resp.Processed++ is
			// intentionally moved out of stageProcessBatch. The counter is
			// now the canonical "items actually persisted to media_assets"
			// tally and is incremented in stagePersistResults AFTER a
			// successful finalizer call. The pre-fix code incremented here
			// BEFORE persist was attempted, producing fake-success when
			// stagePersistResults could not write through.
			ps.resp.Items = append(ps.resp.Items, arg.w.item)
		})
	}

	wg.Wait()
	return nil
}

// persistRenditions records each generated rendition as an
// asset_locations row + asset_renditions row. The location is marked
// as primary only for the mezzanine (the canonical edited master).
func (o *RunOrchestratorService) persistRenditions(ctx context.Context, assetID string, renditions []asset.RenditionOutput) error {
	if o.svc.locationRepo == nil || o.svc.renditionRepo == nil {
		return nil
	}
	for _, r := range renditions {
		if r.LocalPath == "" {
			continue
		}
		loc := &asset.Location{
			AssetID:       assetID,
			LocationKind:  asset.LocationKindLocal,
			URI:           r.LocalPath,
			MimeType:      r.MimeType,
			FileSizeBytes: r.SizeBytes,
			FileHash:      r.FileHash,
			IsPrimary:     r.Kind == asset.RenditionKindMezzanine,
		}
		if err := o.svc.locationRepo.Upsert(ctx, loc); err != nil {
			return fmt.Errorf("upsert location for %s/%s: %w", assetID, r.Kind, err)
		}

		rend := &asset.AssetRendition{
			AssetID:    assetID,
			LocationID: &loc.ID,
			Kind:       r.Kind,
			Container:  r.Container,
			Codec:      r.Codec,
			Width:      r.Width,
			Height:     r.Height,
			FPS:        r.FPS,
			Bitrate:    r.Bitrate, SHA256: r.FileHash,
			SizeBytes: r.SizeBytes,
		}
		if _, err := o.svc.renditionRepo.Create(ctx, rend); err != nil {
			return fmt.Errorf("create rendition for %s/%s: %w", assetID, r.Kind, err)
		}
	}
	return nil
}

// stagePersistResults persists each processed clip through the canonical
// AssetFinalizerTx. This replaces the legacy dispatchBridge path and
// writes media_assets, asset_versions, asset_locations, and
// asset_renditions inside a single SQLite transaction per clip.
//
// PR-ARTLIST-FINALIZER (July 2026): the legacy dispatchBridge +
// persistRenditions custom writer are retired. Artlist now uses the
// same AssetFinalizerTx as every other capability, ensuring the ledger
// tables are written by one canonical implementation.
func (o *RunOrchestratorService) stagePersistResults(ctx context.Context, resp *RunTagResponse) {
	if o.svc.assetFinalizer == nil || o.svc.mainDB == nil {
		o.svc.log.Warn("stagePersistResults: asset finalizer or main DB not wired (cannot persist)")
		return
	}

	for i := range resp.Items {
		item := &resp.Items[i]
		if item.Status == "media_process_failed" || item.Status == "dry_run" {
			continue
		}

		// PR-ARTLIST-DOD-GATE-02 (2026-07-07): Drive field gate.
		// Skip items whose processor returned Status="processed" but
		// left Drive fields empty — the processor's Drive upload step
		// failed silently.
		if item.DriveFileID == "" || item.DriveLink == "" {
			o.svc.log.Warn("stagePersistResults: skipping clip with missing Drive fields",
				zap.String("clip_id", item.ClipID),
				zap.String("drive_file_id", item.DriveFileID),
				zap.String("drive_link", item.DriveLink))
			item.Status = "drive_upload_failed"
			item.Error = "Drive upload failed: missing Drive fields after processing"
			continue
		}

		// PR-ARTLIST-HASH-FIX (July 2026): reject assets without a real
		// SHA-256. The legacy fallback (clipID:source) is retired per
		// the hash-system refactor.
		if item.FileHash == "" {
			o.svc.log.Warn("stagePersistResults: skipping clip with missing SHA-256",
				zap.String("clip_id", item.ClipID))
			item.Status = "hash_missing"
			item.Error = "SHA-256 missing after processing"
			continue
		}

		artifact := o.buildPublishedArtifact(item)

		tx, err := o.svc.mainDB.BeginTx(ctx, nil)
		if err != nil {
			o.svc.log.Warn("stagePersistResults: begin tx failed",
				zap.String("clip_id", item.ClipID), zap.Error(err))
			continue
		}

		_, _, err = o.svc.assetFinalizer.FinalizeAsset(ctx, assetfinalizer.WrapTx(tx), artifact)
		if err != nil {
			_ = tx.Rollback()
			o.svc.log.Warn("stagePersistResults: finalizer failed",
				zap.String("clip_id", item.ClipID), zap.Error(err))
			item.Status = "persist_failed"
			item.Error = err.Error()
			continue
		}

		if err := tx.Commit(); err != nil {
			o.svc.log.Warn("stagePersistResults: commit failed",
				zap.String("clip_id", item.ClipID), zap.Error(err))
			item.Status = "persist_failed"
			item.Error = err.Error()
			continue
		}

		resp.Processed++
	}
}

// buildPublishedArtifact maps a processed RunTagItem into the canonical
// finalization.PublishedArtifact consumed by AssetFinalizerTx.
func (o *RunOrchestratorService) buildPublishedArtifact(item *RunTagItem) finalization.PublishedArtifact {
	artifact := finalization.PublishedArtifact{
		ArtifactID:  item.ClipID,
		Kind:        finalization.KindVideo,
		Filename:    item.Filename,
		MIMEType:    "video/mp4",
		SizeBytes:   fileSizeFromPath(item.LocalPath),
		SHA256:      item.FileHash,
		Source:      "artlist",
		Description: item.Name,
		Location: finalization.AssetLocation{
			Provider:     "drive",
			FileID:       item.DriveFileID,
			WebViewLink:  item.DriveLink,
			DownloadLink: item.DownloadLink,
			FolderID:     o.svc.cfg.Drive.ArtlistFolder(),
			Action:       finalization.PublishCreated,
		},
		ArtifactMetadata: map[string]any{
			"source":   "artlist",
			"status":   "processed",
			"filename": item.Filename,
		},
	}

	for _, r := range item.Renditions {
		artifact.Renditions = append(artifact.Renditions, finalization.AssetRenditionLocation{
			Kind:      string(r.Kind),
			Provider:  "local",
			URI:       r.LocalPath,
			MimeType:  r.MimeType,
			SizeBytes: r.SizeBytes,
			FileHash:  r.FileHash,
			Width:     r.Width,
			Height:    r.Height,
			FPS:       r.FPS,
			Bitrate:   r.Bitrate,
			Container: r.Container,
			Codec:     r.Codec,
		})
	}

	return artifact
}

// stageIndexAsync is a no-op. The canonical AssetFinalizerTx emits the
// outbox event inside stagePersistResults, so no async indexing stage
// is required.
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
