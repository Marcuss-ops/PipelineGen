// Package stockpipeline — step_extract_clips.go
// (PR-STOCK-ORCHESTRATOR-SPLIT, July 2026;
// PR-SPLIT-STEP-EXTRACT-CLIPS, August 2026).
//
// SOLE owner of StockExtractClipsStep — the canonical
// implementation of the stock.extract_clips step (Step 3 of the
// 6-step pipeline) per godlike/06 SSOT. Phase 1 (July 2026):
// rewired to use the real VideoCutter.Cut port instead of
// emitting logical IDs.
//
// The step:
//  1. Builds a sourceID → localPath map from StagedAssets.
//  2. Groups ClipPlan entries by SourceID.
//  3. For each group, calls the canonical
//     validateAndProbeSourceDuration helper (in
//     step_extract_clips_validation.go) to probe the source
//     duration and bounds-check every clip; then constructs
//     CutRequest with the real SourcePath and calls
//     runner.Cutter().Cut(ctx, req).
//  4. Collects OutputPath values from SuccessfulItems() →
//     CutPaths.
//  5. Writes asset/outbox via Writer.WriteAndEnqueue for each
//     successfully cut clip (via buildRichStockAsset +
//     composeStockChunkSearchText helpers in
//     step_extract_clips_assets.go).
//
// godlike/07 fail-closed contracts:
//   - Cutter nil + plans empty → test-fixture path (CutPaths = nil,
//     no error).
//   - Cutter nil + plans non-empty → ErrStockExtractClipsCutterRequired.
//   - plans empty → Debug + return nil (no work to do).
//   - Source not staged → Warn + skip (graceful degradation; other
//     sources may still have staged files).
//   - All cuts fail for a source → error (terminal, typed wrap
//     preserving cutter typed sentinel via %w).
//   - Zero cut files across all sources → terminal error
//     (production gate, closes false-success class).
//
// godlike/06 SSOT: this file is the slim orchestrator only. The
// asset-write helpers (buildRichStockAsset + composeStockChunkSearchText)
// live in step_extract_clips_assets.go; the pre-cut validation helper
// (validateAndProbeSourceDuration) lives in
// step_extract_clips_validation.go. Both helpers are pure
// functions in the same package — cross-file resolution via
// package-scope visibility (godlike/07 minimum-blast-radius:
// zero new exported symbols).
package stockpipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ErrStockClipsOutOfRange (PR-STOCK-TIMESTAMP-CLIPS Front 5,
// July 2026) surfaces a clip whose EndSec exceeds the probed
// source duration. The step validates every ClipPlan in the
// group BEFORE invoking VideoCutter.Cut so a malformed timestamp
// fails fast with a typed error instead of silently producing a
// half-broken artifact via ffmpeg. Per user spec literal
// "fallire subito con errore leggibile" — no auto-clamp, no
// truncation, the operator must fix the input. Wrap the typed
// sentinel via fmt.Errorf("...: %w", ErrStockClipsOutOfRange) so
// callers can errors.Is(err, ErrStockClipsOutOfRange) without
// parsing the human-readable prefix.
var ErrStockClipsOutOfRange = errors.New("stock.extract_clips: clip EndSec exceeds source duration — input out of range (PR-STOCK-TIMESTAMP-CLIPS Front 5, godlike/07 NO-FAKE-AVAILABILITY)")

// maxDriveUploadWorkers caps the concurrent Drive uploads issued by
// stock.extract_clips for a single source group.
const maxDriveUploadWorkers = 3

// StockExtractClipsStep is the canonical implementation of
// stock.extract_clips. Phase 1 (July 2026): rewired to use the
// real VideoCutter.Cut port instead of emitting logical IDs.
// Phase 2 (August 2026, PR-SPLIT-STEP-EXTRACT-CLIPS): the inline
// pre-cut validation block was extracted to
// validateAndProbeSourceDuration in step_extract_clips_validation.go
// per godlike/06 SSOT one-canonical-owner-per-fact. The asset-write
// helpers (buildRichStockAsset + composeStockChunkSearchText) were
// extracted to step_extract_clips_assets.go.
type StockExtractClipsStep struct{}

func (StockExtractClipsStep) Name() string { return StepKeyStockExtractClips }

func (StockExtractClipsStep) Run(ctx context.Context, runner StepRunner) error {
	cutter := runner.Cutter()
	plans := runner.State().Plan

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.extract_clips: starting",
			zap.Int("plan_count", len(plans)),
			zap.Int("staged_sources", len(runner.State().StagedAssets)))
	}

	// Test-fixture path: no cutter wired → skip (downstream
	// compose_chunks handles empty CutPaths gracefully).
	//
	// godlike/07 fail-closed gate (PR-STOCK-FAKE-AVAILABILITY-REMOVAL
	// follow-up, July 2026): when plans is non-empty (the step has
	// work to do) BUT cutter is nil, surface ErrStockExtractClipsCutterRequired
	// instead of silently returning CutPaths=nil. A nil cutter with
	// zero plans is still a valid test-fixture skip (empty plans →
	// zero cutPaths is the correct outcome).
	if cutter == nil {
		if len(plans) > 0 {
			return ErrStockExtractClipsCutterRequired
		}
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.extract_clips: VideoCutter nil + empty plan — skipping cut (test-fixture path)")
		}
		runner.State().CutPaths = nil
		return nil
	}

	if len(plans) == 0 {
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.extract_clips: empty plan — nothing to extract")
		}
		runner.State().CutPaths = nil
		return nil
	}

	// Build sourceID → *StagedAsset map from StagedAssets. We
	// keep the full pointer (not just LocalPath) so the pre-cut
	// probe in the loop can read DurationSec (PR-STOCK-TIMESTAMP-CLIPS
	// Front 5, July 2026) without a second iteration.
	stagedBySource := make(map[string]*assets.StagedAsset)
	for _, sa := range runner.State().StagedAssets {
		if sa.SourceID != "" && sa.LocalPath != "" {
			sa := sa
			stagedBySource[sa.SourceID] = sa
		}
	}

	// Group ClipPlan by SourceID.
	grouped := make(map[string][]ClipPlan)
	for _, plan := range plans {
		grouped[plan.SourceID] = append(grouped[plan.SourceID], plan)
	}

	in := runner.RunInput()
	noAudio := in != nil && in.NoAudio
	writer := runner.Writer()
	artifactPrep := runner.ArtifactPreparation()
	rootFolderName := stockRootFolderName(in)
	rootFolderOverride := stockRootFolderOverride(in)
	timestampGroupName := stockTimestampGroupName(in)
	if in != nil && len(in.Clips) > 0 {
		timestampGroupName = stockTimestampParentGroupName(in)
	}

	var cutPaths []string
	var publishedChunks []ChunkState
	type timestampGroupBuffer struct {
		leafName   string
		firstIndex int
		chunks     []ChunkState
	}
	groupBuckets := make(map[string]*timestampGroupBuffer)
	segmentCounts := make(map[string]int)
	sourceIdx := 0
	batchID := runner.JobID()
	batchRepo := runner.BatchRepository()
	batchEnsured := false

	for sourceID, groupPlans := range grouped {
		staged := stagedBySource[sourceID]
		if staged == nil {
			// Source not staged — skip gracefully. The upstream
			// stock.stage_sources step logs Warn on stage failure;
			// here we surface the downstream impact without aborting
			// (other sources may still have staged files).
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.extract_clips: source not staged — skipping cuts",
					zap.String("source_id", sourceID),
					zap.Int("clip_count", len(groupPlans)))
			}
			sourceIdx++
			continue
		}
		sourcePath := staged.LocalPath

		// Fase 2 durable state: ensure batch, group and artifact rows exist.
		if batchRepo != nil {
			if !batchEnsured {
				if batchErr := batchRepo.CreateBatch(ctx, &StockBatch{
					ID:          batchID,
					Fingerprint: runner.RunFingerprint(),
					SourceURL:   sourceID,
					Status:      BatchStateRunning,
				}); batchErr != nil && runner.Log() != nil {
					runner.Log().Warn("orchestrator: stock.extract_clips: failed to create batch row",
						zap.String("batch_id", batchID), zap.Error(batchErr))
				}
				batchEnsured = true
			}
			groupID := batchID + ":group:" + sourceID
			if groupErr := batchRepo.CreateGroup(ctx, &StockBatchGroup{
				ID:            groupID,
				BatchID:       batchID,
				GroupKey:      sourceID,
				Status:        GroupStateRunning,
				ExpectedClips: len(groupPlans),
			}); groupErr != nil && runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.extract_clips: failed to create group row",
					zap.String("group_id", groupID), zap.Error(groupErr))
			}
			for clipIdx, plan := range groupPlans {
				artifactID := groupID + ":clip:" + strconv.Itoa(clipIdx)
				if artErr := batchRepo.CreateArtifact(ctx, &StockArtifact{
					ID:          artifactID,
					BatchID:     batchID,
					GroupID:     groupID,
					Ordinal:     clipIdx,
					ArtifactKey: plan.OutputLogicalID,
					SourceURL:   plan.SourceID,
					StartSec:    plan.StartSec,
					EndSec:      plan.EndSec,
					Status:      ArtifactStatePlanned,
				}); artErr != nil && runner.Log() != nil {
					runner.Log().Warn("orchestrator: stock.extract_clips: failed to create artifact row",
						zap.String("artifact_id", artifactID), zap.Error(artErr))
				}
			}
		}

		// PR-STOCK-TIMESTAMP-CLIPS Front 5 (July 2026) + PR-SPLIT-STEP-EXTRACT-CLIPS
		// (August 2026): pre-cut duration validation extracted to
		// validateAndProbeSourceDuration (sister file
		// step_extract_clips_validation.go). The helper:
		//   1. Resolves the source duration via the 3-tier source-of-truth
		//      priority (staged.DurationSec fast-path → ffprobe
		//      SourceDurationProbe → Warn + skip).
		//   2. Bounds-checks every clip.EndSec against the duration
		//      with strict `>` boundary semantics (no epsilon).
		//   3. Returns the typed ErrStockClipsOutOfRange sentinel
		//      wrapped via fmt.Errorf on the first violation.
		// No auto-clamp per user spec literal "fallire subito con
		// errore leggibile".
		sourceDuration, _, validationErr := validateAndProbeSourceDuration(ctx, runner, sourceID, sourcePath, staged, groupPlans)
		if validationErr != nil {
			return validationErr
		}

		// Mark durable artifacts as EXTRACTING before invoking FFmpeg.
		if batchRepo != nil {
			groupID := batchID + ":group:" + sourceID
			for clipIdx := range groupPlans {
				artifactID := groupID + ":clip:" + strconv.Itoa(clipIdx)
				_ = batchRepo.MarkArtifactExtracting(ctx, artifactID)
			}
		}

		// Cut all clips of the source group in a single batch.
		jobs := make([]CutJob, len(groupPlans))
		workspaceDir, err := filepath.Abs(filepath.Join("data", "stock", "workspaces", runner.JobID(), "extracted"))
		if err != nil {
			return fmt.Errorf("orchestrator: stock.extract_clips: resolve persistent workspace: %w", err)
		}
		if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
			return fmt.Errorf("orchestrator: stock.extract_clips: create persistent workspace: %w", err)
		}
		for clipIdx, plan := range groupPlans {
			outputPath := filepath.Join(workspaceDir,
				fmt.Sprintf("stock_cut_%s_%d_%d.mp4", runner.JobID(), sourceIdx, clipIdx))
			jobs[clipIdx] = CutJob{
				StartSec:   plan.StartSec,
				EndSec:     plan.EndSec,
				OutputPath: outputPath,
			}
		}

		req := CutRequest{
			SourcePath:     sourcePath,
			SourceDuration: sourceDuration,
			Jobs:           jobs,
			NoAudio:        noAudio,
			Logger:         runner.Log(),
			SourceIdx:      sourceIdx,
		}

		result, cutErr := cutter.Cut(ctx, req)
		successful := result.SuccessfulItems()
		if cutErr != nil && len(successful) == 0 {
			return fmt.Errorf("orchestrator: stock.extract_clips: VideoCutter.Cut failed for source %s: %w",
				sourceID, cutErr)
		}

		// Collect upload tasks sequentially so that segment counts,
		// filenames, and chunk ordering remain deterministic before
		// any concurrent work starts.
		type clipUploadTask struct {
			clipIdx         int
			plan            ClipPlan
			cVA             finalization.VerifiedArtifact
			segmentFilename string
			leafName        string
		}
		type clipUploadResult struct {
			chunk    ChunkState
			leafName string
			err      error
		}

		var uploadTasks []clipUploadTask

		// Now process each clip.
		for clipIdx, plan := range groupPlans {
			item := result.Items[clipIdx]
			artifactID := batchID + ":group:" + sourceID + ":clip:" + strconv.Itoa(clipIdx)
			if item.Status == CutItemStatusFailed || item.OutputPath == "" {
				if runner.Log() != nil {
					runner.Log().Warn("orchestrator: stock.extract_clips: no playable clip produced",
						zap.String("source_id", sourceID),
						zap.Int("clip_index", clipIdx),
						zap.String("output_path", item.JobID),
						zap.Error(item.Err))
				}
				if batchRepo != nil {
					_ = batchRepo.MarkArtifactFailed(ctx, artifactID, ArtifactStateFailedPermanent, "cut failed or empty output")
				}
				continue
			}

			actualDurationMs := int(item.DurationSec * 1000)
			hash := item.SHA256Hex
			if hash == "" {
				var hashErr error
				hash, hashErr = job.ComputeSHA256(item.OutputPath)
				if hashErr != nil {
					if batchRepo != nil {
						_ = batchRepo.MarkArtifactFailed(ctx, artifactID, ArtifactStateFailedPermanent, "SHA256 compute failed: "+hashErr.Error())
					}
					if runner.Log() != nil {
						runner.Log().Error("orchestrator: stock.extract_clips: SHA256 compute failed — aborting rich-asset write",
							zap.String("source_id", sourceID),
							zap.Int("clip_index", clipIdx),
							zap.String("output_path", item.OutputPath),
							zap.Error(hashErr))
					}
					return fmt.Errorf("orchestrator: stock.extract_clips: chunk %d (artifact=%s) SHA256 compute: %w",
						clipIdx, plan.OutputLogicalID, hashErr)
				}
			}
			if batchRepo != nil {
				_ = batchRepo.MarkArtifactExtracted(ctx, artifactID, item.OutputPath, hash, actualDurationMs)
			}

			cutPaths = append(cutPaths, item.OutputPath)

			// Write asset/outbox for this successfully cut clip.
			// Asset ID uses the planner's OutputLogicalID (stable
			// across retries) so retry dedupe works; the real file
			// path is in CutPaths for downstream consumption.
			if writer != nil {
				// PR-STOCK-TIMESTAMP-CLIPS Front 4 (July 2026):
				// rich-asset write — compute SHA256 fail-closed
				// (P0 2.4 hardening: a malformed digest short-circuits
				// at the cut step rather than silently reaching the
				// indexer which would terminal-reject it), then build
				// the canonical 10-field asset via buildRichStockAsset
				// (sister file step_extract_clips_assets.go) and pass
				// the hash to WriteAndEnqueue (replaces the prior ""
				// literal).
				// Fase 1: reuse the hash already computed by the cutter
				// during validation to avoid hashing the clip twice.

				clip := buildRichStockAsset(plan, sourceIdx, clipIdx, item.OutputPath, hash)

				if err := writer.WriteAndEnqueue(ctx, clip, hash); err != nil {
					if runner.Log() != nil {
						runner.Log().Warn("orchestrator: stock.extract_clips: WriteAndEnqueue failed — aborting atomic dispatch",
							zap.String("logical_id", plan.OutputLogicalID),
							zap.String("output_path", item.OutputPath),
							zap.String("source_id", sourceID),
							zap.Int("clip_index", clipIdx),
							zap.Error(err))
					}
					return fmt.Errorf("orchestrator: stock.extract_clips: %w: %w", ErrAtomicDispatchFailed, err)
				}

				if artifactPrep != nil {
					leafName := timestampGroupName
					if in != nil && len(in.Clips) > 0 {
						leafName = stockClipFolderName(in, plan, timestampGroupName)
					}
					segmentCount := segmentCounts[leafName] + 1
					segmentCounts[leafName] = segmentCount

					segmentFilename := fmt.Sprintf("clip_%03d.mp4", segmentCount)
					clipVA := finalization.VerifiedArtifact{
						ArtifactID:         plan.OutputLogicalID,
						Kind:               finalization.KindVideo,
						Filename:           segmentFilename,
						MIMEType:           "video/mp4",
						LocalPath:          item.OutputPath,
						SizeBytes:          item.SizeBytes,
						SHA256:             hash,
						Requirement:        finalization.ArtifactRequirementRequired,
						IdempotencyKey:     clip.ID + ":" + hash,
						Description:        plan.Description,
						RootFolderName:     rootFolderName,
						RootFolderOverride: rootFolderOverride,
						RootFolderResolved: in != nil && in.DriveFolderResolved,
						PathLeafName:       leafName,
					}

					uploadTasks = append(uploadTasks, clipUploadTask{
						clipIdx:         clipIdx,
						plan:            plan,
						cVA:             clipVA,
						segmentFilename: segmentFilename,
						leafName:        leafName,
					})
				}
			}
		}

		// Upload prepared clips with a bounded worker pool (max 3
		// concurrent Drive uploads per source group). Results are
		// written into a pre-allocated slice and aggregated
		// sequentially afterwards so publishedChunks and
		// groupBuckets stay ordered by clipIdx.
		if artifactPrep != nil && len(uploadTasks) > 0 {
			uploadResults := make([]clipUploadResult, len(uploadTasks))

			taskCh := make(chan int, len(uploadTasks))
			for i := range uploadTasks {
				taskCh <- i
			}
			close(taskCh)

			numWorkers := maxDriveUploadWorkers
			if len(uploadTasks) < numWorkers {
				numWorkers = len(uploadTasks)
			}

			var wg sync.WaitGroup
			for w := 0; w < numWorkers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for taskIdx := range taskCh {
						task := uploadTasks[taskIdx]
						clipPublished, clipPrepErr := artifactPrep.Prepare(ctx, task.cVA)
						if clipPrepErr != nil {
							uploadResults[taskIdx] = clipUploadResult{
								err: fmt.Errorf("%w: clip publish for chunk %d (artifact=%s): %w",
									ErrStockPublishArtifactFailed, task.clipIdx, task.plan.OutputLogicalID, clipPrepErr),
							}
							continue
						}

						publishedChunk := ChunkState{
							Index:              task.clipIdx,
							ArtifactID:         task.plan.OutputLogicalID,
							Filename:           task.segmentFilename,
							LocalPath:          task.cVA.LocalPath,
							SizeBytes:          task.cVA.SizeBytes,
							SHA256:             task.cVA.SHA256,
							Description:        task.plan.Description,
							Title:              task.plan.Title,
							SourceURL:          task.plan.SourceID,
							SourceProvider:     task.plan.SourceProvider,
							SourceVideoID:      task.plan.SourceVideoID,
							StartSec:           task.plan.StartSec,
							EndSec:             task.plan.EndSec,
							Round:              task.plan.Round,
							Tags:               append([]string(nil), task.plan.Tags...),
							Category:           task.plan.Category,
							Slug:               task.plan.Slug,
							RemoteFileID:       clipPublished.Location.FileID,
							RemoteWebViewLink:  clipPublished.Location.WebViewLink,
							DrivePath:          clipPublished.Location.WebViewLink,
							RemoteDownloadLink: clipPublished.Location.DownloadLink,
						}

						uploadResults[taskIdx] = clipUploadResult{
							chunk:    publishedChunk,
							leafName: task.leafName,
						}
					}
				}()
			}
			wg.Wait()

			for _, res := range uploadResults {
				if res.err != nil {
					return res.err
				}
				publishedChunks = append(publishedChunks, res.chunk)
				bucket := groupBuckets[res.leafName]
				if bucket == nil {
					bucket = &timestampGroupBuffer{leafName: res.leafName, firstIndex: res.chunk.Index}
					groupBuckets[res.leafName] = bucket
				}
				bucket.chunks = append(bucket.chunks, res.chunk)
			}
		}

		if runner.Log() != nil {
			runner.Log().Info("orchestrator: stock.extract_clips: source complete",
				zap.String("source_id", sourceID),
				zap.Int("planned", len(groupPlans)),
				zap.Int("produced", len(cutPaths)))
		}

		sourceIdx++
	}

	if artifactPrep != nil && len(groupBuckets) > 0 {
		type orderedGroup struct {
			leafName   string
			firstIndex int
			chunks     []ChunkState
		}
		ordered := make([]orderedGroup, 0, len(groupBuckets))
		for leafName, bucket := range groupBuckets {
			if bucket == nil || len(bucket.chunks) == 0 {
				continue
			}
			ordered = append(ordered, orderedGroup{
				leafName:   leafName,
				firstIndex: bucket.firstIndex,
				chunks:     append([]ChunkState(nil), bucket.chunks...),
			})
		}
		sort.Slice(ordered, func(i, j int) bool {
			if ordered[i].firstIndex == ordered[j].firstIndex {
				return ordered[i].leafName < ordered[j].leafName
			}
			return ordered[i].firstIndex < ordered[j].firstIndex
		})
		for _, group := range ordered {
			metaPath, metaHash, metaSize, metaErr := writeAndHashMetadata(in, group.chunks, runner.RunFingerprint())
			if metaErr != nil {
				return fmt.Errorf("%w: parent metadata stage for %s: %w",
					ErrStockPublishArtifactFailed, group.leafName, metaErr)
			}
			defer func(p string) {
				if rmErr := os.Remove(p); rmErr != nil && !os.IsNotExist(rmErr) {
					if runner.Log() != nil {
						runner.Log().Warn("orchestrator: stock.extract_clips: failed to remove parent metadata temp file",
							zap.String("path", p), zap.Error(rmErr))
					}
				}
			}(metaPath)

			metaIdem, metaIdemErr := asset.SHA256IdempotencyKey("stock:"+runner.RunFingerprint()+":timestamp-group-metadata:"+group.leafName, metaHash)
			if metaIdemErr != nil {
				return fmt.Errorf("%w: parent metadata idem-key for %s: %w",
					ErrStockPublishArtifactFailed, group.leafName, metaIdemErr)
			}
			metaArtifactID := TimestampArtifactID(runner.RunFingerprint(), group.firstIndex, "metadata")
			metaVA := finalization.VerifiedArtifact{
				ArtifactID:         metaArtifactID,
				Kind:               finalization.KindMetadata,
				Filename:           "metadata.json",
				MIMEType:           "application/json",
				LocalPath:          metaPath,
				SizeBytes:          metaSize,
				SHA256:             metaHash,
				Requirement:        finalization.ArtifactRequirementRequired,
				IdempotencyKey:     metaIdem,
				RootFolderName:     rootFolderName,
				RootFolderOverride: rootFolderOverride,
				RootFolderResolved: in != nil && in.DriveFolderResolved,
				PathLeafName:       group.leafName,
			}
			if _, metaPrepErr := artifactPrep.Prepare(ctx, metaVA); metaPrepErr != nil {
				return fmt.Errorf("%w: parent metadata upload for %s (artifact=%s): %w",
					ErrStockPublishArtifactFailed, group.leafName, metaArtifactID, metaPrepErr)
			}
		}
	}

	// Production gate: cutter wired (non-nil) + zero cut files
	// across all sources → terminal error. This closes the
	// false-success class where extract_clips "succeeds" without
	// producing any real files on disk.
	if len(cutPaths) == 0 {
		return fmt.Errorf("orchestrator: stock.extract_clips: zero cut files produced across %d sources — all sources either unstaged or all cuts failed", len(grouped))
	}

	runner.State().CutPaths = cutPaths
	if len(publishedChunks) > 0 {
		runner.State().Published = publishedChunks
	}

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.extract_clips: SUCCEEDED",
			zap.Int("cut_paths", len(cutPaths)),
			zap.Int("sources_processed", sourceIdx))
	}
	return nil
}

// time package usage is preserved per the existing asset
// LifecycleState / Duration / time.Duration patterns in
// buildRichStockAsset (sister file step_extract_clips_assets.go).
// The blank import is the canonical godlike/06 SSOT pin keeping
// this file's dependency on the time package explicit.
var _ = time.Time{} // pin import (used transitively via time.Duration in sister file)
