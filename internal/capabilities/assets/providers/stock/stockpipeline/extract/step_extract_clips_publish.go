package extract

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/publish"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// stockPendingCommit is one verified cut awaiting its canonical media commit.
// It is produced by the hashing pass and consumed by the commit pass that runs
// after every Drive upload of the same source group has succeeded.
type stockPendingCommit struct {
	clip      *asset.Asset
	hash      string
	plan      core.ClipPlan
	sizeBytes int64
}

// publishCuts handles the post-cut pipeline for a single source group:
// SHA256 hashing, Drive publication, then the canonical media commit.
//
// ORDERING CONTRACT (P0 stock-acquisition certification, September 2026):
// the canonical commit — media_assets plus the `asset.index.requested` outbox
// event — happens ONLY after the Drive publication of the same bytes has
// succeeded. The published order is the runbook order:
//
//	cut VERIFIED
//	  → Drive publication VERIFIED
//	  → canonical media commit (media_assets + outbox)
//	  → indexing
//
// Committing before the upload (the former order) emitted an indexing request
// for a clip that had no addressable Drive location yet: the asset became
// searchable — index_state could reach INDEXED — while its publication was
// still incomplete, or permanently failed. Every upload of the group is
// therefore attempted first and a single upload failure aborts the group
// before ANY commit, so the group is either fully committed or not committed
// at all.
//
// Recovery remains intact: a PUBLISHED artifact is not terminal for
// FindIncompleteArtifacts (only VERIFIED / FAILED_PERMANENT / QUARANTINED
// are), so a resumed run re-drives publishCuts for it, and the Drive upload is
// idempotent by construction (VerifiedArtifact.IdempotencyKey).
//
// GOP-16 (November 2026): the phases are explicit — scanGroupItems (state,
// hash, resume, assets without Drive identity), runUploadPool (publication,
// no commit), mergePublishedChunks, commitPendingGroup (the only commit
// caller). The ordering invariant follows from publishCuts calling the commit
// pass last, not from statement order inside one 330-line body.
//
// Returns the cut paths and published chunks for this source.
func publishCuts(ctx context.Context, runner core.StepRunner, sourceID string, sourceIdx int,
	groupPlans []core.ClipPlan, result core.CutBatchResult,
	segmentCounts map[string]int, groupBuckets map[string]*timestampGroupBuffer,
	rootFolderName, resolvedFolderID, timestampGroupName string, in *core.RunInput, batchID string) ([]string, []core.ChunkState, error) {

	scan, err := scanGroupItems(ctx, runner, sourceID, sourceIdx, groupPlans, result,
		segmentCounts, rootFolderName, resolvedFolderID, timestampGroupName, in, batchID)
	if err != nil {
		return nil, nil, err
	}

	// GATE: Drive publication. A failure here returns before the commit pass,
	// so the media SSOT keeps no asset — and no indexing request — for an
	// unpublished clip.
	uploaded, err := runUploadPool(ctx, runner, scan.uploadTasks, batchID)
	if err != nil {
		return nil, nil, err
	}

	// Uploaded chunks first, then resumed ones, merged into the same buckets
	// in plan order; the canonical commit upserts the same deterministic rows
	// again instead of duplicating Drive files.
	publishedChunks := mergePublishedChunks(uploaded, scan.resumedResults, groupBuckets)

	// GATE: canonical media commit. media_assets + the media index outbox
	// event are written atomically by the canonical dispatcher — after every
	// published artifact of this group carries a real Drive location.
	if err := commitPendingGroup(ctx, runner, scan.pending); err != nil {
		return nil, nil, err
	}

	return scan.cutPaths, publishedChunks, nil
}

// publishScan is the result of the per-group scan: cut paths, assets awaiting
// commit (no Drive identity yet), fresh upload tasks and already-published
// rows reused by resume.
type publishScan struct {
	cutPaths       []string
	pending        []stockPendingCommit
	uploadTasks    []clipUploadTask
	resumedResults []clipUploadResult
}

// scanGroupItems walks the group in plan order: failed cuts are marked
// FAILED_PERMANENT, successful ones hashed, resumed rows inspected and
// reattached, extracted rows marked durably, and assets built WITHOUT their
// Drive identity. It performs no Drive upload and no media commit.
func scanGroupItems(ctx context.Context, runner core.StepRunner, sourceID string, sourceIdx int,
	groupPlans []core.ClipPlan, result core.CutBatchResult, segmentCounts map[string]int,
	rootFolderName, resolvedFolderID, timestampGroupName string, in *core.RunInput, batchID string) (publishScan, error) {

	writer := runner.Writer()
	artifactPrep := runner.ArtifactPreparation()
	batchRepo := runner.BatchRepository()

	var scan publishScan
	for clipIdx, plan := range groupPlans {
		item := result.Items[clipIdx]
		artifactID := core.StockArtifactID(batchID, sourceID, clipIdx)

		if item.Status == core.CutItemStatusFailed || item.OutputPath == "" {
			if err := markUnplayableClip(ctx, runner, batchRepo, artifactID, sourceID, clipIdx, item); err != nil {
				return publishScan{}, err
			}
			continue
		}

		hash, err := resolveClipHash(ctx, runner, batchRepo, artifactID, item, clipIdx)
		if err != nil {
			return publishScan{}, err
		}

		// Resume: an artifact already PUBLISHED/VERIFIED by a dead attempt
		// must not be re-uploaded (that would publish a duplicate Drive
		// file). Its persisted Drive identity is reused for the commit
		// below; the upload pool only ever sees fresh artifacts.
		resumedFileID, resumedFolderID, resumedLink, resumed, err := reusablePublication(ctx, batchRepo, artifactID)
		if err != nil {
			return publishScan{}, err
		}
		if batchRepo != nil && !resumed {
			if err := batchRepo.MarkArtifactExtracted(ctx, artifactID, item.OutputPath, hash, int(item.DurationSec*1000)); err != nil {
				return publishScan{}, fmt.Errorf("%w: mark artifact %s extracted: %w", core.ErrStockExtractClipsDurableStateFailed, artifactID, err)
			}
		}
		scan.cutPaths = append(scan.cutPaths, item.OutputPath)

		if writer == nil {
			continue
		}

		// The asset is built WITHOUT its Drive identity: the publication
		// phase attaches it, immediately before the commit. Nothing is
		// written to the media SSOT (and no indexing request is emitted)
		// until that publication has returned successfully.
		clip := buildRichStockAsset(plan, sourceIdx, clipIdx, item.OutputPath, hash)
		scan.pending = append(scan.pending, stockPendingCommit{
			clip:      clip,
			hash:      hash,
			plan:      plan,
			sizeBytes: item.SizeBytes,
		})

		if artifactPrep == nil {
			continue
		}

		task, resumedResult := prepareClipPublication(clip, plan, clipIdx, item, hash,
			timestampGroupName, in, segmentCounts, rootFolderName, resolvedFolderID,
			resumed, resumedFileID, resumedFolderID, resumedLink)
		if resumedResult != nil {
			scan.resumedResults = append(scan.resumedResults, *resumedResult)
			continue
		}
		scan.uploadTasks = append(scan.uploadTasks, *task)
	}

	return scan, nil
}

// markUnplayableClip records a clip that produced no playable output.
func markUnplayableClip(ctx context.Context, runner core.StepRunner, batchRepo core.StockBatchRepository, artifactID, sourceID string, clipIdx int, item core.CutItemResult) error {
	if runner.Log() != nil {
		runner.Log().Warn("orchestrator: stock.extract_clips: no playable clip produced",
			zap.String("source_id", sourceID),
			zap.Int("clip_index", clipIdx),
			zap.Error(item.Err))
	}
	if batchRepo == nil {
		return nil
	}
	if err := batchRepo.MarkArtifactFailed(ctx, artifactID, core.ArtifactStateFailedPermanent, "cut failed or empty output"); err != nil {
		return fmt.Errorf("%w: mark artifact %s failed after cut failure: %w", core.ErrStockExtractClipsDurableStateFailed, artifactID, err)
	}
	return nil
}

// resolveClipHash returns the cut's SHA256, computing it when the cutter did
// not supply one. A hashing failure marks the artifact FAILED_PERMANENT and
// aborts the group.
func resolveClipHash(ctx context.Context, runner core.StepRunner, batchRepo core.StockBatchRepository, artifactID string, item core.CutItemResult, clipIdx int) (string, error) {
	hash := item.SHA256Hex
	if hash != "" {
		return hash, nil
	}
	hash, hashErr := job.ComputeSHA256(runner.LocalFS(), item.OutputPath)
	if hashErr == nil {
		return hash, nil
	}
	if batchRepo != nil {
		if stateErr := batchRepo.MarkArtifactFailed(ctx, artifactID, core.ArtifactStateFailedPermanent, "SHA256: "+hashErr.Error()); stateErr != nil {
			return "", fmt.Errorf("%w: mark artifact %s failed after SHA256 error: %w", core.ErrStockExtractClipsDurableStateFailed, artifactID, stateErr)
		}
	}
	return "", fmt.Errorf("orchestrator: stock.extract_clips: chunk %d SHA256: %w", clipIdx, hashErr)
}

// reusablePublication inspects the artifact row for a publication left by a
// dead attempt. ok=false means "upload normally"; an error is fail-closed
// (the row could not be inspected, so a duplicate upload must not be risked).
func reusablePublication(ctx context.Context, batchRepo core.StockBatchRepository, artifactID string) (fileID, folderID, webLink string, ok bool, err error) {
	fileID, folderID, webLink, ok, err = publishedRowReusable(ctx, batchRepo, artifactID)
	if err != nil {
		return "", "", "", false, fmt.Errorf("%w: inspect artifact %s for resume: %w", core.ErrStockExtractClipsDurableStateFailed, artifactID, err)
	}
	return fileID, folderID, webLink, ok, nil
}

// prepareClipPublication builds the verified artifact and either a fresh
// upload task or, for a resumed row, the already-published result with the
// persisted Drive identity reattached to the asset.
func prepareClipPublication(clip *asset.Asset, plan core.ClipPlan, clipIdx int, item core.CutItemResult, hash, timestampGroupName string, in *core.RunInput, segmentCounts map[string]int, rootFolderName, resolvedFolderID string, resumed bool, resumedFileID, resumedFolderID, resumedLink string) (*clipUploadTask, *clipUploadResult) {
	leafName := timestampGroupName
	if in != nil && len(in.Clips) > 0 {
		leafName = publish.RunInputClipFolderName(in, plan, timestampGroupName)
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

	chunk := stockChunkState(plan, clipIdx, item.OutputPath, segmentFilename, hash, item.SizeBytes)
	if resumed {
		// Reattach the persisted Drive identity (the commit below
		// persists it as the row's primary location) and record the
		// chunk without touching the upload pool.
		downloadLink := "https://drive.google.com/uc?id=" + resumedFileID
		clip.SetDriveFileID(resumedFileID)
		clip.SetDriveLink(resumedLink)
		clip.SetDownloadLink(downloadLink)
		clip.SetFolderID(resumedFolderID)
		chunk.RemoteFileID = resumedFileID
		chunk.RemoteWebViewLink = resumedLink
		chunk.DrivePath = resumedLink
		chunk.RemoteDownloadLink = downloadLink
		chunk.TimestampFolderID = resumedFolderID
		return nil, &clipUploadResult{chunk: chunk, leafName: leafName}
	}

	return &clipUploadTask{
		clipIdx:         clipIdx,
		plan:            plan,
		cVA:             clipVA,
		segmentFilename: segmentFilename,
		leafName:        leafName,
		clip:            clip,
	}, nil
}

// mergePublishedChunks merges fresh upload results and resumed results into
// the group buckets (in that order, preserving plan order inside each list).
func mergePublishedChunks(uploaded, resumed []clipUploadResult, groupBuckets map[string]*timestampGroupBuffer) []core.ChunkState {
	publishedChunks := make([]core.ChunkState, 0, len(uploaded)+len(resumed))
	appendResult := func(res clipUploadResult) {
		publishedChunks = append(publishedChunks, res.chunk)
		bucket := groupBuckets[res.leafName]
		if bucket == nil {
			bucket = &timestampGroupBuffer{leafName: res.leafName, firstIndex: res.chunk.Index}
			groupBuckets[res.leafName] = bucket
		}
		bucket.chunks = append(bucket.chunks, res.chunk)
	}
	for _, res := range uploaded {
		appendResult(res)
	}
	for _, res := range resumed {
		appendResult(res)
	}
	return publishedChunks
}

// commitPendingGroup is the ONLY caller of the canonical commit. It writes
// media_assets + the index outbox event atomically per asset and records the
// semantic phases around that one operation.
func commitPendingGroup(ctx context.Context, runner core.StepRunner, pending []stockPendingCommit) error {
	writer := runner.Writer()
	for i := range pending {
		c := &pending[i]

		databaseMetric := core.StartStockPhase(ctx, runner, "stock.database_save")
		writeErr := writer.WriteAndEnqueue(ctx, c.clip, c.hash)
		if databaseMetric != nil {
			out := int64(0)
			if writeErr == nil {
				out = 1
			}
			databaseMetric.SetItems(1, out)
			databaseMetric.SetBytes(c.sizeBytes, c.sizeBytes)
		}
		core.FinishStockPhase(runner, databaseMetric, "stock.database_save", writeErr)

		// EnqueueAndIndex is one atomic database+outbox operation;
		// record index as a post-commit event so its metric does not
		// double-count database_save duration. The detail names the
		// measured boundary; actual vector-store consumption remains async.
		indexMetric := core.StartStockPhase(ctx, runner, "stock.index")
		if indexMetric != nil {
			indexMetric.SetItems(1, core.BoolToInt64(writeErr == nil))
		}
		core.FinishStockPhase(runner, indexMetric, "stock.index", writeErr)
		if writeErr != nil {
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.extract_clips: WriteAndEnqueue failed",
					zap.String("logical_id", c.plan.OutputLogicalID),
					zap.Error(writeErr))
			}
			return fmt.Errorf("%w: %w", core.ErrAtomicDispatchFailed, writeErr)
		}
	}
	return nil
}
