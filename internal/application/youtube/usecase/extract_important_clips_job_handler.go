// Package usecase - extract_important_clips_job_handler.go: PR-GEMMA-EXTRACT-IMPORTANT Step 2.
//
// ExtractImportantClipsJobHandler is the canonical broker-facing handler.
// Per AGENTS.md Git-Lesson-2 direct-to-main, Steps 1+2 land atomically;
// Step 4 (this follow-up commit) flips the hardcoded literal to the
// canonical const job.TypeYouTubeClipExtractImportant from
// internal/domain/job/job.go via godlike/06 SSOT one-canonical-const-per-jobType.
//
// CANONICAL BROKER SIGNATURE (per voiceover/jobs/generate_handler.go):
//
//	appjobs.HandlerFunc == func(ctx, *appjobs.Job, *appjobs.JobTools) (map[string]any, error)
//
// Behavior:
//   - return (nil, err)        -> dispatcher auto-marks job FAILED
//   - return (resultMap, nil)  -> dispatcher auto-marks job SUCCEEDED
package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"

	jobyoutube "github.com/Marcuss-ops/PipelineGen/internal/domain/youtube"
	"go.uber.org/zap"
)

type ExtractImportantClipsJobHandler struct {
	useCase *ExtractImportantClipsUseCase
	log     *zap.Logger
}

func NewExtractImportantClipsJobHandler(
	useCase *ExtractImportantClipsUseCase,
	log *zap.Logger,
) *ExtractImportantClipsJobHandler {
	if useCase == nil || log == nil {
		panic("ExtractImportantClipsJobHandler.New: useCase and log required")
	}
	return &ExtractImportantClipsJobHandler{useCase: useCase, log: log}
}

// Register: hand the handler to the canonical jobs.Service. The composition
// wiring site is internal/app/build_bundles_youtube.go::wireYoutubeCatalogJobBindings
// (mirrors the existing YoutubeClipService.Register pattern).
//
// Uses the canonical const job.TypeYouTubeClipExtractImportant from
// internal/domain/job/job.go (godlike/06 SSOT one-canonical-const-per-jobType).
func (h *ExtractImportantClipsJobHandler) Register(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		return errors.New("ExtractImportantClipsJobHandler.Register: jobsSvc is nil")
	}
	return jobsSvc.RegisterHandler(jobyoutube.TypeClipExtractImportant, appjobs.HandlerFunc(h.HandleJob))
}

// HandleJob: canonical broker entry-point.
//
// Progress emit pattern (mirrors voiceover/jobs/generate_handler.go):
//
//	pf := appjobs.SafeProgressFn(tools)
//	pf(0, "starting extract_important_clips")
//	defer pf(100, "extract_important_clips done")
//
// Error classification (godlike/07 typed-error contract):
//   - 5 use-case sentinels -> TERMINAL (no retry benefit per FASE-3 retry policy)
//   - all other errors     -> RETRYABLE per broker's default policy
func (h *ExtractImportantClipsJobHandler) HandleJob(
	ctx context.Context,
	j *appjobs.Job,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	pf := appjobs.SafeProgressFn(tools)
	pf(0, "starting extract_important_clips")
	defer pf(100, "extract_important_clips done")

	// 1) JSON payload decode
	var cmd ExtractImportantClipsCommand
	if err := json.Unmarshal(j.Payload, &cmd); err != nil {
		// payload decode failure is ALWAYS terminal (corrupt broker input).
		return nil, fmt.Errorf("extract_important_clips: payload decode: %w", err)
	}
	h.log.Info("extract_important_clips.start",
		zap.String("job_id", j.ID),
		zap.String("video_id", cmd.VideoID),
		zap.Int("max_segments", cmd.MaxSegments),
	)

	// 2) delegate to use case Execute
	res, err := h.useCase.Execute(ctx, cmd)
	if err != nil {
		pf(100, fmt.Sprintf("failed: %v", err))
		return nil, h.classifyError(err)
	}

	// 3) build result map
	out := map[string]any{
		"video_id":        res.VideoID,
		"language":        res.Language,
		"segments_total":  res.SegmentsTotal,
		"clips_processed": res.ClipsProcessed,
		"clips_failed":    res.ClipsFailed,
		"clips":           res.Clips,
	}
	h.log.Info("extract_important_clips.success",
		zap.String("job_id", j.ID),
		zap.String("video_id", res.VideoID),
		zap.Int("segments_total", res.SegmentsTotal),
		zap.Int("clips_processed", res.ClipsProcessed),
		zap.Int("clips_failed", res.ClipsFailed),
	)
	return out, nil
}

// classifyError: TERMINAL vs RETRYABLE split. The 5 typed sentinels are ALWAYS
// terminal (no retry benefit). All other errors are retryable.
func (h *ExtractImportantClipsJobHandler) classifyError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidInput):
		return fmt.Errorf("terminal: %w", err)
	case errors.Is(err, ErrSubtitleUnavailable):
		return fmt.Errorf("terminal: %w", err)
	case errors.Is(err, ErrAnalyzerUnavailable):
		return fmt.Errorf("terminal: %w", err)
	case errors.Is(err, ErrNoSegments):
		return fmt.Errorf("terminal: %w", err)
	case errors.Is(err, ErrClipPublishFailed):
		return fmt.Errorf("terminal: %w", err)
	default:
		// unknown / transient -- broker default retry policy decides.
		return err
	}
}
