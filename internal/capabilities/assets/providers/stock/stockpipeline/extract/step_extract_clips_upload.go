// Package stockpipeline — step_extract_clips_upload.go
// (PR-SPLIT-STEP-EXTRACT-CLIPS, August 2026; GOP-16 November 2026).
//
// Extracted from step_extract_clips.go per godlike/06 SSOT
// one-canonical-owner-per-fact. Owns the concurrent upload worker
// pool, the per-task local types (clipUploadTask, clipUploadResult)
// and the shared ChunkState projection.
package extract

import (
	"context"
	"fmt"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// clipUploadTask is a prepared clip ready for concurrent Drive upload.
//
// clip is the canonical asset awaiting its media commit. The upload worker
// attaches the published Drive identity to it, so the commit that follows the
// publication carries the Drive location as its primary location instead of a
// local-only one (see publishCuts for the ordering contract).
type clipUploadTask struct {
	clipIdx         int
	plan            core.ClipPlan
	cVA             finalization.VerifiedArtifact
	segmentFilename string
	leafName        string
	clip            *asset.Asset
}

// clipUploadResult pairs a published ChunkState with its leafName
// or carries an error when the upload step failed.
type clipUploadResult struct {
	chunk    core.ChunkState
	leafName string
	err      error
}

// runUploadPool publishes every task with a bounded worker pool. It performs
// NO commit: the caller aborts before commitPendingGroup when any result
// carries an error, preserving the group's all-or-nothing publication.
func runUploadPool(ctx context.Context, runner core.StepRunner, tasks []clipUploadTask, batchID string) ([]clipUploadResult, error) {
	if len(tasks) == 0 {
		return nil, nil
	}

	uploadResults := make([]clipUploadResult, len(tasks))
	taskCh := make(chan int, len(tasks))
	for i := range tasks {
		taskCh <- i
	}
	close(taskCh)

	numWorkers := maxDriveUploadWorkers
	if len(tasks) < numWorkers {
		numWorkers = len(tasks)
	}
	batchRepo := runner.BatchRepository()

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		concurrent.SafeGo("stock-publish-cut", func() {
			defer wg.Done()
			for taskIdx := range taskCh {
				uploadResults[taskIdx] = uploadOneClip(ctx, runner, tasks[taskIdx], batchID, batchRepo)
			}
		})
	}
	wg.Wait()

	for _, res := range uploadResults {
		if res.err != nil {
			return nil, res.err
		}
	}
	return uploadResults, nil
}

// uploadOneClip publishes one clip, records the durable PUBLISHED state and
// attaches the Drive identity to the asset for the commit pass.
func uploadOneClip(ctx context.Context, runner core.StepRunner, task clipUploadTask, batchID string, batchRepo core.StockBatchRepository) clipUploadResult {
	clipPublished, clipPrepErr := core.PrepareStockDriveArtifact(ctx, runner, task.cVA, map[string]any{
		"assets_generated":        1,
		"output_duration_seconds": task.plan.EndSec - task.plan.StartSec,
	})
	if clipPrepErr != nil {
		return clipUploadResult{
			err: fmt.Errorf("%w: chunk %d (artifact=%s): %w",
				core.ErrStockPublishArtifactFailed, task.clipIdx, task.plan.OutputLogicalID, clipPrepErr),
		}
	}

	if batchRepo != nil {
		artifactID := core.StockArtifactID(batchID, task.plan.SourceID, task.clipIdx)
		pubErr := batchRepo.MarkArtifactPublished(ctx, artifactID,
			clipPublished.Location.FileID,
			clipPublished.Location.FolderID,
			clipPublished.Location.WebViewLink)
		if pubErr != nil {
			return clipUploadResult{
				err: fmt.Errorf("%w: durable state save for chunk %d: %w",
					core.ErrStockPublishArtifactFailed, task.clipIdx, pubErr),
			}
		}
	}

	// Attach the published Drive identity to the asset so the canonical
	// commit below persists the Drive location as the primary location.
	if task.clip != nil {
		task.clip.SetDriveFileID(clipPublished.Location.FileID)
		task.clip.SetDriveLink(clipPublished.Location.WebViewLink)
		task.clip.SetDownloadLink(clipPublished.Location.DownloadLink)
		task.clip.SetFolderID(clipPublished.Location.FolderID)
	}

	chunk := stockChunkState(task.plan, task.clipIdx, task.cVA.LocalPath,
		task.segmentFilename, task.cVA.SHA256, task.cVA.SizeBytes)
	chunk.RemoteFileID = clipPublished.Location.FileID
	chunk.RemoteWebViewLink = clipPublished.Location.WebViewLink
	chunk.DrivePath = clipPublished.Location.WebViewLink
	chunk.RemoteDownloadLink = clipPublished.Location.DownloadLink
	// The parent Drive folder of the published chunk. Carried on the chunk so
	// the stock job finalizer's spine write can persist the same
	// media_assets.folder_id this commit does: the media upsert writes
	// folder_id unconditionally, so a spine write without the folder would
	// blank it. Before this the field was never populated, so every stock row
	// kept folder_id="".
	chunk.TimestampFolderID = clipPublished.Location.FolderID

	return clipUploadResult{chunk: chunk, leafName: task.leafName}
}

// stockChunkState builds the provider-agnostic projection shared by the
// resumed and freshly-published paths. Remote fields are filled by the caller.
func stockChunkState(plan core.ClipPlan, index int, localPath, filename, hash string, sizeBytes int64) core.ChunkState {
	return core.ChunkState{
		Index:          index,
		ArtifactID:     plan.OutputLogicalID,
		Filename:       filename,
		LocalPath:      localPath,
		SizeBytes:      sizeBytes,
		SHA256:         hash,
		Description:    plan.Description,
		Title:          plan.Title,
		SourceURL:      plan.SourceID,
		SourceProvider: plan.SourceProvider,
		SourceVideoID:  plan.SourceVideoID,
		StartSec:       plan.StartSec,
		EndSec:         plan.EndSec,
		Round:          plan.Round,
		Tags:           append([]string(nil), plan.Tags...),
		Category:       plan.Category,
		Slug:           plan.Slug,
	}
}
