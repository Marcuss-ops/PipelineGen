// Package stockpipeline — step_extract_clips_upload.go
// (PR-SPLIT-STEP-EXTRACT-CLIPS, August 2026).
//
// Extracted from step_extract_clips.go per godlike/06 SSOT
// one-canonical-owner-per-fact. Owns the concurrent upload worker
// pool and the per-task local types (clipUploadTask, clipUploadResult).
package stockpipeline

import (
	"context"
	"fmt"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// clipUploadTask is a prepared clip ready for concurrent Drive upload.
type clipUploadTask struct {
	clipIdx         int
	plan            ClipPlan
	cVA             finalization.VerifiedArtifact
	segmentFilename string
	leafName        string
}

// clipUploadResult pairs a published ChunkState with its leafName
// or carries an error when the upload step failed.
type clipUploadResult struct {
	chunk    ChunkState
	leafName string
	err      error
}

// uploadClipBatch uploads a batch of prepared clips concurrently with a
// bounded worker pool (max 3 concurrent Drive uploads). Results preserve
// input order so downstream publishedChunks and groupBuckets stay
// ordered by clipIdx.
func uploadClipBatch(
	ctx context.Context,
	artifactPrep finalization.ArtifactPreparationService,
	batchRepo StockBatchRepository,
	batchID string,
	uploadTasks []clipUploadTask,
) ([]clipUploadResult, error) {
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

				if batchRepo != nil {
					artifactID := StockArtifactID(batchID, task.plan.SourceID, task.clipIdx)
					pubErr := batchRepo.MarkArtifactPublished(ctx, artifactID,
						clipPublished.Location.FileID,
						clipPublished.Location.FolderID,
						clipPublished.Location.WebViewLink,
					)
					if pubErr != nil {
						uploadResults[taskIdx] = clipUploadResult{
							err: fmt.Errorf("%w: durable state save failed for chunk %d: %w",
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

	return uploadResults, nil
}
