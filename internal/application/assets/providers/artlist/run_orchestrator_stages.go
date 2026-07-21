package artlist

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
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
					// Fase 6 / Commit 1 (July 2026): typed gate-block
					// short-circuit. godlike/07 fail-closed: when the
					// stager fires a typed gate-block error (ErrAcquisition
					// ModeBlocked today; the remaining sentinel entries land
					// in Commits 2/3/4 via the classifier), the helper
					// stamps the per-item audit (item.Status + item.Error)
					// verbatim and bumps resp.Failed so EvaluateRunState
					// (Rule 5 PARTIAL_SUCCESS / Rule 3 FAILED) reports the
					// truth instead of paper-over a partial-blocks run as
					// "all good".
					//
					// godlike/06 SSOT: the classification logic lives in
					// gate_block_classifier.go (the SINGLE canonical owner
					// of the (sentinel, per-item status) mapping). Adding a
					// new typed gate-block error MUST extend the
					// classifier, NOT introduce a parallel check here.
					shortStatus := gateBlockShortCircuit(
						&arg.w.item,
						stageErr,
						newGateBlockCounterFor(ps.resp),
						o.svc.log,
						func(stage, msg string) error {
							if o.svc.assetProcessing != nil {
								return o.svc.assetProcessing.Fail(ctx, arg.w.item.ClipID, stage, msg)
							}
							return nil
						},
					)
					if shortStatus != "" {
						// The gate-block helper already stamped item.Status
						// + item.Error + bumped resp.Failed. The orchestr
						// ator MUST append the item to resp.Items + RETURN
						// early so mediaProcessor.Process is NOT invoked —
						// continuing would silently overwrite the typed
						// block with the transport-layer outcome (a
						// godlike/07 fake-availability violation).
						mu.Lock()
						ps.resp.Items = append(ps.resp.Items, arg.w.item)
						mu.Unlock()
						return
					}
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

// stageIndexAsync is a no-op. The canonical AssetFinalizerTx emits the
// outbox event inside stagePersistResults, so no async indexing stage
// is required.
func (o *RunOrchestratorService) stageIndexAsync(_ context.Context, _ *RunTagResponse) {
}

// ImportSingleClip runs the full download/normalize/upload/persist pipeline
// for a single Artlist clip discovered via the detail endpoint. It reuses
// the same stageBuildProcessInputs + stageProcessBatch machinery as the
// tag-pipeline, but for exactly one clip so the caller can return a
// synchronous response.
func (o *RunOrchestratorService) ImportSingleClip(ctx context.Context, req *ImportClipRequest, clip *asset.Asset) (*RunTagItem, error) {
	if clip == nil {
		return nil, fmt.Errorf("asset is required")
	}
	if strings.TrimSpace(clip.ID) == "" {
		return nil, fmt.Errorf("asset has no ID")
	}

	term := clip.Name
	if term == "" {
		term = clip.ID
	}

	rootFolderID := o.svc.cfg.Drive.ArtlistFolder()
	if strings.TrimSpace(req.RootFolderID) != "" {
		rootFolderID = strings.TrimSpace(req.RootFolderID)
	}

	destination, err := o.svc.destinationService.ResolveDestination(ctx, term, rootFolderID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve destination: %w", err)
	}

	resp := &RunTagResponse{
		OK:          true,
		Term:        term,
		TagFolderID: destination.FolderID,
	}

	workItems := o.stageBuildProcessInputs(ctx, &RunTagRequest{
		Term:         term,
		RootFolderID: rootFolderID,
		Limit:        1,
		ClipDuration: 0,
		Width:        0,
		Height:       0,
		FPS:          0,
		Concurrency:  1,
	}, resp, []asset.Asset{*clip})

	if len(workItems) == 0 {
		return nil, fmt.Errorf("clip was skipped (dry-run not supported in import)")
	}

	ps := &pipelineState{
		resp:        resp,
		workItems:   workItems,
		concurrency: 1,
	}
	if err := o.stageProcessBatch(ctx, ps); err != nil {
		return nil, err
	}

	if len(resp.Items) == 0 {
		return nil, fmt.Errorf("no item produced by single-clip import")
	}
	return &resp.Items[0], nil
}

// concurrencyFromRequest determines the concurrency level: default 3, max 10.
func concurrencyFromRequest(req *RunTagRequest) int {
	c := defaults.Int(req.Concurrency, 3)
	if c > 10 {
		return 10
	}
	return c
}
