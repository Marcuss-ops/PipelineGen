package artlist

import (
	"context"
	"fmt"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"os"
	"strings"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assetop"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	defaults "github.com/Marcuss-ops/PipelineGen/pkg/defaults"

	"go.uber.org/zap"
)

type clipWork struct {
	item         RunTagItem
	processInput *detail.ProcessInput
	stagedAsset  *acquisition.PrepareContext
}

type pipelineState struct {
	resp        *RunTagResponse
	workItems   []clipWork
	concurrency int
}

func (o *RunOrchestratorService) stageResolveDestination(ctx context.Context, req *RunTagRequest, resp *RunTagResponse) (string, error) {
	rootFolderID := defaults.String(req.RootFolderID, o.svc.cfg.Drive.ArtlistFolder())
	dest, err := o.svc.destinationService.ResolveDestination(ctx, resp.Term, rootFolderID)
	if err != nil {
		return "", fmt.Errorf("failed to resolve destination: %w", err)
	}
	resp.TagFolderID = dest.FolderID
	return rootFolderID, nil
}

func (o *RunOrchestratorService) stageDiscoverClips(ctx context.Context, req *RunTagRequest, resp *RunTagResponse) (*SearchResponse, error) {
	// First check local DB search to allow offline dry-runs and bypass scraper timeouts on replay.
	// Only bypass if the strategy is NOT "replace" (which explicitly demands fresh live search + acquisition).
	if req.Strategy != "replace" {
		localSearchReq := &SearchRequest{Term: resp.Term, Limit: req.Limit}
		dbResp, err := o.svc.searchService.Search(ctx, localSearchReq)
		if err == nil && dbResp != nil && len(dbResp.Clips) > 0 {
			o.svc.log.Info("artlist discovery: cache-hit via local DB (bypassing live scraper)",
				zap.String("term", resp.Term),
				zap.Int("clips_found", len(dbResp.Clips)))
			resp.Found = len(dbResp.Clips)
			return dbResp, nil
		}
	}

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

func (o *RunOrchestratorService) stageBuildProcessInputs(ctx context.Context, req *RunTagRequest, resp *RunTagResponse, clips []asset.Asset) []clipWork {
	workItems := make([]clipWork, 0, len(clips))

	for _, clip := range clips {
		item := RunTagItem{
			ClipID: clip.ID,
			Name:   clip.Name,
			// Search discovery persists the canonical drive_*/file_hash/
			// download_link metadata keys via the typed accessors. Keep the
			// underscored keys as a compatibility fallback for older staged
			// assets and test fixtures.
			DownloadLink:  defaults.String(clip.DownloadLink(), clip.GetMetadataString("_download_link")),
			DriveLink:     defaults.String(clip.DriveLink(), clip.GetMetadataString("_drive_link")),
			DriveFileID:   defaults.String(clip.DriveFileID(), clip.GetMetadataString("_drive_file_id")),
			LocalPath:     defaults.String(clip.LocalPath(), clip.GetMetadataString("_local_path")),
			LegacyFileMD5: defaults.String(clip.LegacyFileMD5(), clip.GetMetadataString("_file_hash")),
			Metadata:      cloneMetadata(clip.Metadata),
		}
		// A durable Drive hit is enough to skip reacquisition, but a stale
		// local_path must never be returned as if the file were still usable.
		// The response remains honest about the available location.
		if item.LocalPath != "" {
			if _, statErr := os.Stat(item.LocalPath); statErr != nil {
				item.LocalPath = ""
			}
		}
		item.ClipID = defaults.String(item.ClipID, clip.ID)
		item.Name = defaults.String(item.Name, clip.Name)
		item.Name = defaults.String(item.Name, item.ClipID)

		if req.DryRun {
			decision := assetop.ResolveExistingAssetStrategy(req.Strategy, assetop.ExistingAssetEvidence{
				DriveFileID:   item.DriveFileID,
				DriveLink:     item.DriveLink,
				LegacyFileMD5: item.LegacyFileMD5,
			})
			item.Status = "dry_run"
			if decision.Skip {
				resp.WouldSkip++
			} else {
				resp.WouldProcess++
			}
			resp.Skipped++
			resp.Items = append(resp.Items, item)
			continue
		}

		decision := assetop.ResolveExistingAssetStrategy(req.Strategy, assetop.ExistingAssetEvidence{
			DriveFileID:   item.DriveFileID,
			DriveLink:     item.DriveLink,
			LegacyFileMD5: item.LegacyFileMD5,
		})
		if decision.Skip {
			item.Status = "skipped_existing"
			resp.Skipped++
			resp.Items = append(resp.Items, item)
			o.svc.log.Info("artlist acquisition skipped by canonical existing-asset strategy",
				zap.String("clip_id", item.ClipID),
				zap.String("strategy", req.Strategy),
				zap.String("reason", decision.Reason),
				zap.String("drive_file_id", item.DriveFileID))
			continue
		}

		sourceURL := defaults.String(item.DownloadLink, clip.ExternalURL())
		outputDir := ""
		if o.svc.cfg != nil {
			externalID := DeriveExternalAssetID(item.ClipID, sourceURL)
			layout := NewStorageLayout(o.svc.cfg.Storage.DataDir, "artlist", externalID)
			outputDir = layout.BaseDir()
		}
		rootFolderID := defaults.String(req.RootFolderID, o.svc.cfg.Drive.ArtlistFolder())
		processInput := &detail.ProcessInput{
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
			KeepAudio:       true,
			Metadata: map[string]any{
				"source":         "artlist",
				"strategy":       req.Strategy,
				"root_folder_id": rootFolderID,
			},
		}
		if o.svc.cfg != nil {
			processInput.Duration = defaults.Int(processInput.Duration, o.svc.cfg.Video.Duration)
		}
		workItems = append(workItems, clipWork{item: item, processInput: processInput})
	}

	return workItems
}

func (o *RunOrchestratorService) stageProcessBatch(ctx context.Context, ps *pipelineState) error {
	if len(ps.workItems) == 0 {
		return nil
	}
	if o.svc.mediaProcessor == nil {
		return fmt.Errorf("media processor is not configured")
	}

	var mu sync.Mutex
	sem := make(chan struct{}, ps.concurrency)
	var wg sync.WaitGroup

	for _, work := range ps.workItems {
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

			if o.svc.assetProcessing != nil {
				if err := o.svc.assetProcessing.Start(ctx, arg.w.item.ClipID, "download"); err != nil {
					o.svc.log.Warn("asset_processing.Start failed",
						zap.String("clip_id", arg.w.item.ClipID),
						zap.Error(err))
				}
			}

			if o.svc.stager != nil && arg.w.processInput.SourceURL != "" {
				source := acquisition.SourceRef{URL: arg.w.processInput.SourceURL, PolicyVersion: "artlist-v1"}
				staged, stageErr := o.svc.stager.Prepare(ctx, acquisition.PrepareRequest{
					Source:         source,
					IdempotencyKey: "artlist.clip." + acquisition.DeriveIdempotencyKey(source),
					CallerRef:      "artlist.run",
				})
				if stageErr != nil {
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
					if staged == nil || !staged.HasLocal() {
						o.svc.log.Warn("acquisition stager returned no local path",
							zap.String("clip_id", arg.w.item.ClipID),
							zap.String("source_url", arg.w.processInput.SourceURL))
					} else {
						arg.w.stagedAsset = staged
						arg.w.processInput.LocalPath = staged.LocalPath
						o.svc.log.Info("shared acquisition SourceStager replaced mediaProcessor download",
							zap.String("clip_id", arg.w.item.ClipID),
							zap.String("source_url", arg.w.processInput.SourceURL),
							zap.String("local_path", staged.LocalPath),
							zap.Int64("bytes", staged.SizeBytes))
					}
				}
			}
			if arg.w.stagedAsset != nil {
				defer func(staged *acquisition.PrepareContext) {
					if cleanupErr := o.svc.stager.Release(context.WithoutCancel(ctx), staged.CleanupToken); cleanupErr != nil {
						o.svc.log.Warn("shared acquisition release failed (best-effort)",
							zap.String("local_path", staged.LocalPath),
							zap.Error(cleanupErr))
					}
				}(arg.w.stagedAsset)
			}

			result, procErr := o.svc.mediaProcessor.Process(ctx, arg.w.processInput)
			if procErr != nil {
				if o.svc.assetProcessing != nil {
					if err := o.svc.assetProcessing.Fail(ctx, arg.w.item.ClipID, "download", procErr.Error()); err != nil {
						o.svc.log.Warn("asset_processing.Fail failed",
							zap.String("clip_id", arg.w.item.ClipID),
							zap.Error(err))
					}
				}
				mu.Lock()
				arg.w.item.Status = "media_process_failed"
				arg.w.item.Error = procErr.Error()
				ps.resp.Failed++
				ps.resp.Items = append(ps.resp.Items, arg.w.item)
				mu.Unlock()
				return
			}

			if o.svc.cfg != nil && o.svc.cfg.External.ArtlistSkipTranscription {
				o.svc.log.Info("artlist transcription skipped (operator opt-in via artlist_skip_transcription)",
					zap.String("clip_id", arg.w.item.ClipID),
					zap.String("local_path", result.LocalPath))
			} else {
				transcript, detectedLang, transcribeErr := o.svc.transcriber.Transcribe(ctx, result.LocalPath)
				if transcribeErr != nil {
					o.svc.log.Warn("artlist transcription failed",
						zap.String("clip_id", arg.w.item.ClipID),
						zap.String("local_path", result.LocalPath),
						zap.Error(transcribeErr))
					if o.svc.assetProcessing != nil {
						if err := o.svc.assetProcessing.Fail(ctx, arg.w.item.ClipID, "transcription", transcribeErr.Error()); err != nil {
							o.svc.log.Warn("asset_processing.Fail failed",
								zap.String("clip_id", arg.w.item.ClipID),
								zap.Error(err))
						}
					}
					mu.Lock()
					arg.w.item.Status = "transcription_failed"
					arg.w.item.Error = fmt.Sprintf("transcription failed: %v", transcribeErr)
					ps.resp.Failed++
					ps.resp.Items = append(ps.resp.Items, arg.w.item)
					mu.Unlock()
					return
				}

				lang, _ := asset.Normalize(detectedLang)
				if lang == "" {
					lang = "und"
				}
				hash := detail.TextHash(transcript, lang, detail.TextTrackTranscript)
				track := detail.TextTrack{
					AssetID:            arg.w.item.ClipID,
					LanguageCode:       lang,
					TextKind:           detail.TextTrackTranscript,
					TextContent:        transcript,
					SourceType:         detail.TextSourceWhisper,
					SourceLanguageCode: lang,
					IsOriginal:         true,
					ModelName:          "tiny",
					TextHash:           hash,
					SourceVersion:      detail.SourceVersion(hash, lang, lang, "", "tiny", "", ""),
					IsCurrent:          true,
					Status:             detail.TextTrackReady,
				}
				if err := o.svc.textTrackRepo.UpsertBatch(ctx, []detail.TextTrack{track}); err != nil {
					o.svc.log.Warn("artlist transcript persist failed",
						zap.String("clip_id", arg.w.item.ClipID),
						zap.Error(err))
					if o.svc.assetProcessing != nil {
						if failErr := o.svc.assetProcessing.Fail(ctx, arg.w.item.ClipID, "transcription", err.Error()); failErr != nil {
							o.svc.log.Warn("asset_processing.Fail failed",
								zap.String("clip_id", arg.w.item.ClipID),
								zap.Error(failErr))
						}
					}
					mu.Lock()
					arg.w.item.Status = "transcript_persist_failed"
					arg.w.item.Error = fmt.Sprintf("transcript persist failed: %v", err)
					ps.resp.Failed++
					ps.resp.Items = append(ps.resp.Items, arg.w.item)
					mu.Unlock()
					return
				}
			}

			if o.svc.assetProcessing != nil {
				if err := o.svc.assetProcessing.Complete(ctx, arg.w.item.ClipID, "download"); err != nil {
					o.svc.log.Warn("asset_processing.Complete failed",
						zap.String("clip_id", arg.w.item.ClipID),
						zap.Error(err))
				}
			}

			mu.Lock()
			arg.w.item.Status = defaults.String(result.Status, "processed")
			arg.w.item.Filename = result.Filename
			arg.w.item.LocalPath = result.LocalPath
			arg.w.item.LegacyFileMD5 = result.LegacyFileMD5
			arg.w.item.DriveLink = result.DriveLink
			arg.w.item.DriveFileID = result.DriveFileID
			arg.w.item.DownloadLink = result.DownloadLink
			arg.w.item.Renditions = result.Renditions
			ps.resp.Items = append(ps.resp.Items, arg.w.item)
			mu.Unlock()
		})
	}

	wg.Wait()
	return nil
}

func (o *RunOrchestratorService) stageIndexAsync(_ context.Context, _ *RunTagResponse) {}

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

	resp := &RunTagResponse{OK: true, Term: term, TagFolderID: destination.FolderID}
	workItems := o.stageBuildProcessInputs(ctx, &RunTagRequest{
		Term:         term,
		RootFolderID: rootFolderID,
		Limit:        1,
		ClipDuration: 0,
		Width:        0,
		Height:       0,
		FPSNum:       0,
		FPSDen:       0,
		Concurrency:  1,
	}, resp, []asset.Asset{*clip})

	if len(workItems) == 0 {
		if len(resp.Items) == 1 && resp.Items[0].Status == "skipped_existing" {
			return &resp.Items[0], nil
		}
		return nil, fmt.Errorf("clip was skipped (dry-run not supported in import)")
	}

	ps := &pipelineState{resp: resp, workItems: workItems, concurrency: 1}
	if err := o.stageProcessBatch(ctx, ps); err != nil {
		return nil, err
	}
	o.stagePersistResults(ctx, resp)
	o.stageIndexAsync(ctx, resp)
	if len(resp.Items) == 0 {
		return nil, fmt.Errorf("no item produced by single-clip import")
	}
	return &resp.Items[0], nil
}
