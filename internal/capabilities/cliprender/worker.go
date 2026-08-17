package cliprender

// worker.go is the canonical Master job handler for clip.render.
//
// Pipeline (this step):
//
//	decode payload → Normalize + Validate → parallel preparation
//	(Preparer) → emit prepared artifacts as job events → fail closed
//	with ErrRenderPhaseNotImplemented.
//
// The terminal failure is deliberate (godlike/07 fail-closed): the
// preparation phase is real and observable, but the render phase
// (ClipRenderPlanV1 compilation + single-pass Rust render_clip +
// contract validation + Drive upload + derived asset commit) lands in
// the follow-up step. A job that prepared successfully but could not
// render must NEVER report success — the typed sentinel keeps the
// queue honest until the render phase replaces it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// ErrRenderPhaseNotImplemented is the typed terminal sentinel returned by
// the worker after a successful preparation. The render phase (single Rust
// render pass + validation + Drive upload + derived asset commit) replaces
// this failure in the follow-up step.
var ErrRenderPhaseNotImplemented = errors.New("clip.render: render phase not implemented yet (preparation completed — render_clip lands in the follow-up step)")

// ErrInvalidJobPayload is the typed sentinel for an undecodable job payload.
// Terminal: retrying the same payload can never succeed.
var ErrInvalidJobPayload = errors.New("clip.render: invalid job payload")

// Worker is the canonical clip.render job handler. It is constructed with
// the Preparer and bound to the Master via
// job.Service.RegisterHandler(TypeClipRender, job.HandlerFunc(worker.Handle)).
type Worker struct {
	preparer *Preparer
	log      *zap.Logger
}

// NewWorker constructs the canonical worker. Fail-closed: preparer and log
// are mandatory.
func NewWorker(preparer *Preparer, log *zap.Logger) (*Worker, error) {
	if preparer == nil {
		return nil, fmt.Errorf("cliprender.NewWorker: Preparer is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Worker{preparer: preparer, log: log}, nil
}

// Handle is the job.Handler-shaped entry point bound to the Master.
func (w *Worker) Handle(ctx context.Context, j *job.Job, tools *job.JobExecutionTools) (job.Result, error) {
	progress := safeProgress(tools)
	emit := safeEvent(tools)

	progress(0, "clip.render started")

	var req RenderRequest
	if err := json.Unmarshal(j.Payload, &req); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJobPayload, err)
	}
	req.Normalize()
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJobPayload, err)
	}

	w.log.Info("clip.render.job.start",
		zap.String("job_id", j.ID),
		zap.String("source_asset_id", req.SourceAssetID),
	)
	progress(10, "request validated; running parallel preparation")

	prepared, err := w.preparer.Prepare(ctx, &req, j.ID)
	if err != nil {
		w.log.Error("clip.render.job.prepare_failed",
			zap.String("job_id", j.ID),
			zap.String("source_asset_id", req.SourceAssetID),
			zap.Error(err),
		)
		emit("clip.render.prepare.failed", "parallel preparation failed", map[string]any{
			"source_asset_id": req.SourceAssetID,
			"error":           err.Error(),
		})
		return nil, fmt.Errorf("clip.render: prepare: %w", err)
	}

	// Emit the prepared artifacts so the run is observable even though the
	// render phase is not yet implemented (fail-closed terminal below).
	emit("clip.render.prepare.done", "parallel preparation completed", map[string]any{
		"source_asset_id":  req.SourceAssetID,
		"source_path":      prepared.Source.LocalPath,
		"source_sha256":    prepared.Source.SHA256,
		"transcript_mode":  req.Transcript.Mode,
		"transcript_reuse": prepared.Transcript.Reused,
		"contract_id":      prepared.Contract.ContractID,
		"total_wall_ms":    prepared.Timings.TotalWallMS,
		"total_work_ms":    prepared.Timings.TotalWorkMS,
		"parallel":         prepared.Timings.Parallel,
	})
	progress(90, "preparation complete; render phase pending")

	result := preparedResult(j, &req, prepared)
	// Fail closed: preparation is not a rendered clip. The follow-up step
	// replaces this terminal error with the single-pass render.
	return result, fmt.Errorf("%w: job_id=%s source_asset_id=%s (prepared artifacts emitted; render_clip lands in the follow-up step)",
		ErrRenderPhaseNotImplemented, j.ID, req.SourceAssetID)
}

// preparedResult projects the *Prepared into the canonical job result map.
// Only JSON-safe values — the result envelope is persisted by the Master.
func preparedResult(j *job.Job, req *RenderRequest, prepared *Prepared) job.Result {
	result := job.Result{
		"job_id":          j.ID,
		"source_asset_id": req.SourceAssetID,
		"phase":           "prepared",
		"transcript_mode": req.Transcript.Mode,
		"contract_id":     prepared.Contract.ContractID,
		"contract": map[string]any{
			"width":        prepared.Contract.Width,
			"height":       prepared.Contract.Height,
			"fps":          prepared.Contract.FPS,
			"video_codec":  prepared.Contract.VideoCodec,
			"audio_codec":  prepared.Contract.AudioCodec,
			"pixel_format": prepared.Contract.PixelFormat,
		},
		"source": map[string]any{
			"asset_id":   prepared.Source.AssetID,
			"path":       prepared.Source.LocalPath,
			"sha256":     prepared.Source.SHA256,
			"size_bytes": prepared.Source.SizeBytes,
			"from_cache": prepared.Source.FromCache,
		},
		"transcript": map[string]any{
			"language":            prepared.Transcript.Language,
			"reused":              prepared.Transcript.Reused,
			"text_sha256":         prepared.Transcript.TextSHA256,
			"cues":                len(prepared.Transcript.Cues),
			"source_audio_sha256": prepared.Transcript.SourceAudioSHA256,
		},
		"timings": map[string]any{
			"total_wall_ms": prepared.Timings.TotalWallMS,
			"total_work_ms": prepared.Timings.TotalWorkMS,
			"parallel":      prepared.Timings.Parallel,
		},
	}
	if prepared.Watermark != nil {
		result["watermark"] = map[string]any{
			"asset_id": prepared.Watermark.AssetID,
			"path":     prepared.Watermark.LocalPath,
			"sha256":   prepared.Watermark.SHA256,
		}
	}
	if prepared.Background != nil {
		result["background"] = map[string]any{
			"asset_id": prepared.Background.AssetID,
			"path":     prepared.Background.LocalPath,
			"sha256":   prepared.Background.SHA256,
		}
	}
	return result
}

// safeProgress returns a nil-safe progress callback.
func safeProgress(tools *job.JobExecutionTools) func(int, string) {
	return func(progress int, message string) {
		if tools != nil && tools.Progress != nil {
			tools.Progress(progress, message)
		}
	}
}

// safeEvent returns a nil-safe event callback.
func safeEvent(tools *job.JobExecutionTools) func(string, string, map[string]any) {
	return func(eventType, message string, data map[string]any) {
		if tools != nil && tools.Event != nil {
			tools.Event(eventType, message, data)
		}
	}
}
