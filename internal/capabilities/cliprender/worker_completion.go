package cliprender

import (
	"context"
	"fmt"
	"math"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// completeRendered is the ONE post-render completion path for clip.render.
// Both the historical blocking path and the async settle continuation call
// this method, so probe/publication/result projection cannot drift between
// execution modes.
func (w *Worker) completeRendered(
	ctx context.Context,
	j *job.Job,
	tools *job.JobExecutionTools,
	jobStart time.Time,
	req *RenderRequest,
	prepared *Prepared,
	plan ClipRenderPlanV1,
	subtitleArtifact *SubtitleArtifact,
	publishFolderID string,
	subtitleCompileMS int64,
	outcome *RenderOutcome,
	renderMS int64,
) (job.Result, error) {
	progress := safeProgress(tools)
	emit := safeEvent(tools)

	if outcome == nil || outcome.OutputPath == "" || outcome.SizeBytes <= 0 {
		return nil, fmt.Errorf("clip.render: renderer returned an invalid output")
	}
	if req == nil {
		return nil, fmt.Errorf("clip.render: completion request is nil")
	}
	if prepared == nil || prepared.Contract == nil || prepared.Source == nil {
		return nil, fmt.Errorf("clip.render: completion preparation snapshot is incomplete")
	}

	// Fail-closed GPU gate. RenderingGen/Chronon is the sole supported render
	// authority; async completion must preserve exactly the same gate as the
	// blocking path.
	if req.Execution.RequireGPU && !outcome.Backend.IsGPUBackend() {
		return nil, fmt.Errorf("clip.render: execution.require_gpu=true but resolved backend %q is not a GPU backend", outcome.Backend)
	}
	if outcome.Backend != BackendChrononVulkan {
		return nil, fmt.Errorf("clip.render: backend resolved to %q; only Chronon (%s) is permitted", outcome.Backend, BackendChrononVulkan)
	}
	if req.Execution.RequireZeroCopy {
		return nil, fmt.Errorf("clip.render: execution.require_zero_copy=true is unsatisfiable: no backend certifies video_zero_copy (GPU compositing is Chronon-only via the RenderingGen queue)")
	}

	if outcome.Metrics == nil {
		outcome.Metrics = NewRenderMetricsV2()
	}
	outcome.Metrics.RenderWallMS = Metric(renderMS)
	if subtitleCompileMS >= 0 {
		outcome.Metrics.SubtitleCompileMS = Metric(subtitleCompileMS)
	}
	if assetMS := materializeWallMS(prepared.Timings); assetMS >= 0 {
		outcome.Metrics.AssetMaterializeMS = Metric(assetMS)
	}
	if outcome.Metrics.Frames == 0 && outcome.FPSNum > 0 && outcome.FPSDen > 0 {
		outcome.Metrics.Frames = int(math.Round(outcome.DurationSec * float64(outcome.FPSNum) / float64(outcome.FPSDen)))
	}
	projectRendererPhases(ctx, outcome.Backend, outcome.Metrics)

	if w.outputProber != nil {
		probeStart := time.Now()
		probe, err := w.outputProber.ProbeOutput(ctx, outcome.OutputPath)
		probeEnd := time.Now()
		probeStatus := kernobs.StageStatusCompleted
		if err != nil {
			probeStatus = kernobs.StageStatusFailed
		}
		kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipProbe}, probeStart, probeEnd, err)
		kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseHashProbe, probeStart, probeEnd, probeStatus, err)
		if err != nil {
			return nil, fmt.Errorf("clip.render: probe rendered output: %w", err)
		}
		if err := ValidateContract(prepared.Contract, probe); err != nil {
			return nil, fmt.Errorf("clip.render: rendered output violates contract: %w", err)
		}
		emit("clip.render.probe.certified", "rendered bytes certified exact", map[string]any{
			"output_path": outcome.OutputPath,
			"fps_num":     probe.FPSNum,
			"fps_den":     probe.FPSDen,
			"width":       probe.Width,
			"height":      probe.Height,
		})
	}

	// The render job id is the sealed plan RunID. In async mode j is the
	// settle child, so publication/result identity must stay attached to the
	// original render rather than the continuation job id.
	resultJob := &job.Job{ID: plan.RunID}
	if plan.RunID == "" && j != nil {
		resultJob.ID = j.ID
	}

	if w.publisher == nil {
		emit("clip.render.completed", "Chronon render completed without publication", map[string]any{
			"output_path":  outcome.OutputPath,
			"size_bytes":   outcome.SizeBytes,
			"duration_sec": outcome.DurationSec,
			"ffmpeg_ms":    outcome.FFmpegMS,
			"backend":      outcome.Backend,
		})
		progress(100, "clip.render completed")
		finalizeMetrics(outcome.Metrics, time.Since(jobStart).Milliseconds(), outcome.DurationSec)
		return renderedResult(resultJob, req, prepared, plan, subtitleArtifact, outcome, nil), nil
	}

	uploadSlotStart := time.Now()
	publishStart := uploadSlotStart
	publication, err := w.publisher.Publish(ctx, RenderPublishInput{
		RunID:              resultJob.ID,
		SourceAssetID:      req.SourceAssetID,
		SourceTitle:        prepared.Source.Title,
		OutputPath:         outcome.OutputPath,
		Outcome:            outcome,
		Contract:           prepared.Contract,
		Transcript:         prepared.Transcript,
		Subtitles:          subtitleArtifact,
		DriveFolderID:      publishFolderID,
		CertifiedSHA256:    outcome.SHA256,
		CertifiedSizeBytes: outcome.SizeBytes,
	})
	publishEnd := time.Now()
	publishStatus := kernobs.StageStatusCompleted
	if err != nil {
		publishStatus = kernobs.StageStatusFailed
	}
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseUploadSlot, uploadSlotStart, publishEnd, publishStatus, err)
	kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipPublish}, publishStart, publishEnd, err)
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseDrive, publishStart, publishEnd, publishStatus, err)
	if err != nil {
		return nil, fmt.Errorf("clip.render: publish result: %w", err)
	}
	if publication == nil {
		return nil, fmt.Errorf("clip.render: publisher returned a nil publication")
	}

	logPublishMS := int64(NotInstrumented)
	if outcome.Metrics != nil && publication != nil && publication.Publish != nil {
		pm := publication.Publish
		outcome.Metrics.PublicationTotalMS = Metric(pm.TotalMS)
		outcome.Metrics.ArtifactPublishMS = Metric(pm.HashMS + pm.TaxonomyResolveMS + pm.AssetCommitMS)
		driveMS := pm.VideoUploadMS
		if pm.SidecarUploadMS > driveMS {
			driveMS = pm.SidecarUploadMS
		}
		outcome.Metrics.DriveUploadMS = Metric(driveMS)
		logPublishMS = pm.TotalMS
	}
	if publication.AssetID == "" || (!publication.DrivePending && publication.DriveFileID == "") {
		return nil, fmt.Errorf("clip.render: publisher returned an invalid publication")
	}

	emit("clip.render.completed", "Chronon render completed", map[string]any{
		"output_path":  outcome.OutputPath,
		"size_bytes":   outcome.SizeBytes,
		"duration_sec": outcome.DurationSec,
		"ffmpeg_ms":    outcome.FFmpegMS,
		"backend":      outcome.Backend,
	})
	totalMS := time.Since(jobStart).Milliseconds()
	finalizeMetrics(outcome.Metrics, totalMS, outcome.DurationSec)
	workerJobID := resultJob.ID
	if j != nil {
		workerJobID = j.ID
	}
	w.log.Info("clip.render.job.completed",
		zap.String("subsystem", "clip_render_worker"),
		zap.String("job_id", workerJobID),
		zap.String("render_job_id", resultJob.ID),
		zap.String("source_asset_id", req.SourceAssetID),
		zap.String("asset_id", publication.AssetID),
		zap.String("drive_file_id", publication.DriveFileID),
		zap.String("drive_link", publication.DriveLink),
		zap.Bool("drive_pending", publication.DrivePending),
		zap.String("backend", string(outcome.Backend)),
		zap.Int64("total_ms", totalMS),
		zap.Int64("render_ms", renderMS),
		zap.Int64("renderer_finalize_ms", logPublishMS),
		zap.Int64("ffmpeg_ms", outcome.FFmpegMS),
		zap.Int64("size_bytes", outcome.SizeBytes),
	)
	progress(100, "clip.render completed")
	return renderedResult(resultJob, req, prepared, plan, subtitleArtifact, outcome, publication), nil
}
