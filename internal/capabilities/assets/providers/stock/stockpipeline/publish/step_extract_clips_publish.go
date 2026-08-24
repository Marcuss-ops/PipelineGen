package publish

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// publishCuts handles the post-cut pipeline for a single source group:
// SHA256 hashing, asset/outbox writes, Drive upload with bounded worker pool.
// Returns the cut paths and published chunks for this source.
func publishCuts(ctx context.Context, runner StepRunner, sourceID string, sourceIdx int,
	groupPlans []ClipPlan, result CutBatchResult,
	segmentCounts map[string]int, groupBuckets map[string]*timestampGroupBuffer,
	rootFolderName, resolvedFolderID, timestampGroupName string, in *RunInput, batchID string) ([]string, []ChunkState, error) {

	writer := runner.Writer()
	artifactPrep := runner.ArtifactPreparation()
	batchRepo := runner.BatchRepository()

	var cutPaths []string
	var publishedChunks []ChunkState
	var uploadTasks []clipUploadTask

	for clipIdx, plan := range groupPlans {
		item := result.Items[clipIdx]
		artifactID := StockArtifactID(batchID, sourceID, clipIdx)

		if item.Status == CutItemStatusFailed || item.OutputPath == "" {
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.extract_clips: no playable clip produced",
					zap.String("source_id", sourceID),
					zap.Int("clip_index", clipIdx),
					zap.Error(item.Err))
			}
			if batchRepo != nil {
				if err := batchRepo.MarkArtifactFailed(ctx, artifactID, ArtifactStateFailedPermanent, "cut failed or empty output"); err != nil {
					return nil, nil, fmt.Errorf("%w: mark artifact %s failed after cut failure: %w", ErrStockExtractClipsDurableStateFailed, artifactID, err)
				}
			}
			continue
		}

		// Compute hash.
		hash := item.SHA256Hex
		if hash == "" {
			var hashErr error
			hash, hashErr = job.ComputeSHA256(runner.LocalFS(), item.OutputPath)
			if hashErr != nil {
				if batchRepo != nil {
					if stateErr := batchRepo.MarkArtifactFailed(ctx, artifactID, ArtifactStateFailedPermanent, "SHA256: "+hashErr.Error()); stateErr != nil {
						return nil, nil, fmt.Errorf("%w: mark artifact %s failed after SHA256 error: %w", ErrStockExtractClipsDurableStateFailed, artifactID, stateErr)
					}
				}
				return nil, nil, fmt.Errorf("orchestrator: stock.extract_clips: chunk %d SHA256: %w", clipIdx, hashErr)
			}
		}

		actualDurationMs := int(item.DurationSec * 1000)
		if batchRepo != nil {
			if err := batchRepo.MarkArtifactExtracted(ctx, artifactID, item.OutputPath, hash, actualDurationMs); err != nil {
				return nil, nil, fmt.Errorf("%w: mark artifact %s extracted: %w", ErrStockExtractClipsDurableStateFailed, artifactID, err)
			}
		}
		cutPaths = append(cutPaths, item.OutputPath)

		// Asset write + outbox. The canonical dispatcher performs the
		// database save and index-outbox enqueue atomically; record both
		// semantic phases around that one operation without duplicating it.
		if writer != nil {
			clip := buildRichStockAsset(plan, sourceIdx, clipIdx, item.OutputPath, hash)
			databaseMetric := startStockPhase(ctx, runner, "stock.database_save")
			writeErr := writer.WriteAndEnqueue(ctx, clip, hash)
			if databaseMetric != nil {
				out := int64(0)
				if writeErr == nil {
					out = 1
				}
				databaseMetric.SetItems(1, out)
				databaseMetric.SetBytes(item.SizeBytes, item.SizeBytes)
			}
			finishStockPhase(runner, databaseMetric, "stock.database_save", writeErr)

			// EnqueueAndIndex is one atomic database+outbox operation;
			// record index as a post-commit event so its metric does not
			// double-count database_save duration. The detail names the
			// measured boundary; actual Qdrant consumption remains async.
			indexMetric := startStockPhase(ctx, runner, "stock.index")
			if indexMetric != nil {
				indexMetric.SetItems(1, boolToInt64(writeErr == nil))
			}
			finishStockPhase(runner, indexMetric, "stock.index", writeErr)
			if writeErr != nil {
				if runner.Log() != nil {
					runner.Log().Warn("orchestrator: stock.extract_clips: WriteAndEnqueue failed",
						zap.String("logical_id", plan.OutputLogicalID),
						zap.Error(writeErr))
				}
				return nil, nil, fmt.Errorf("%w: %w", ErrAtomicDispatchFailed, writeErr)
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
					ResolvedFolderID:   resolvedFolderID,
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

	// Concurrent upload with bounded worker pool.
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
					clipPublished, clipPrepErr := prepareStockDriveArtifact(ctx, runner, task.cVA, map[string]any{
						"assets_generated":        1,
						"output_duration_seconds": task.plan.EndSec - task.plan.StartSec,
					})
					if clipPrepErr != nil {
						uploadResults[taskIdx] = clipUploadResult{
							err: fmt.Errorf("%w: chunk %d (artifact=%s): %w",
								ErrStockPublishArtifactFailed, task.clipIdx, task.plan.OutputLogicalID, clipPrepErr),
						}
						continue
					}

					if batchRepo != nil {
						artifactID := StockArtifactID(batchID, task.plan.SourceID, task.clipIdx)
						pubErr := batchRepo.MarkArtifactPublished(ctx, artifactID,
							clipPublished.Location.FileID,
							clipPublished.Location.FolderID,
							clipPublished.Location.WebViewLink)
						if pubErr != nil {
							uploadResults[taskIdx] = clipUploadResult{
								err: fmt.Errorf("%w: durable state save for chunk %d: %w",
									ErrStockPublishArtifactFailed, task.clipIdx, pubErr),
							}
							continue
						}
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
				return nil, nil, res.err
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

	return cutPaths, publishedChunks, nil
}
