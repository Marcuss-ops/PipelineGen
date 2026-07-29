package stockpipeline

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// publishCuts handles the post-cut pipeline for a single source group:
// SHA256 hashing, asset/outbox writes, Drive upload with bounded worker pool.
// Returns the cut paths and published chunks for this source.
func publishCuts(ctx context.Context, runner StepRunner, sourceID string, sourceIdx int,
	groupPlans []ClipPlan, result CutBatchResult,
	segmentCounts map[string]int, groupBuckets map[string]*timestampGroupBuffer,
	rootFolderName, rootFolderOverride, timestampGroupName string, in *RunInput, batchID string) ([]string, []ChunkState, error) {

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
				_ = batchRepo.MarkArtifactFailed(ctx, artifactID, ArtifactStateFailedPermanent, "cut failed or empty output")
			}
			continue
		}

		// Compute hash.
		hash := item.SHA256Hex
		if hash == "" {
			var hashErr error
			hash, hashErr = job.ComputeSHA256(item.OutputPath)
			if hashErr != nil {
				if batchRepo != nil {
					_ = batchRepo.MarkArtifactFailed(ctx, artifactID, ArtifactStateFailedPermanent, "SHA256: "+hashErr.Error())
				}
				return nil, nil, fmt.Errorf("orchestrator: stock.extract_clips: chunk %d SHA256: %w", clipIdx, hashErr)
			}
		}

		actualDurationMs := int(item.DurationSec * 1000)
		if batchRepo != nil {
			_ = batchRepo.MarkArtifactExtracted(ctx, artifactID, item.OutputPath, hash, actualDurationMs)
		}
		cutPaths = append(cutPaths, item.OutputPath)

		// Asset write + outbox.
		if writer != nil {
			clip := buildRichStockAsset(plan, sourceIdx, clipIdx, item.OutputPath, hash)
			if err := writer.WriteAndEnqueue(ctx, clip, hash); err != nil {
				if runner.Log() != nil {
					runner.Log().Warn("orchestrator: stock.extract_clips: WriteAndEnqueue failed",
						zap.String("logical_id", plan.OutputLogicalID),
						zap.Error(err))
				}
				return nil, nil, fmt.Errorf("%w: %w", ErrAtomicDispatchFailed, err)
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
					clipPublished, clipPrepErr := artifactPrep.Prepare(ctx, task.cVA)
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
