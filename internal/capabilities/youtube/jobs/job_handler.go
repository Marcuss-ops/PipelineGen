package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	jobtools "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"go.uber.org/zap"
)

// YouTubeExtractor is the bounded interface this handler needs from the
// usecase layer. The concrete usecase.Service satisfies it. Using this
// interface instead of importing youtube/usecase (which would create a
// cycle because usecase/orchestrator.go imports this jobs package).
type YouTubeExtractor interface {
	Extract(ctx context.Context, req *youtubetypes.ExtractRequest) (*youtubetypes.ExtractResponse, error)
}

// JobHandler wraps the usecase.Service to satisfy the job-system
// handler signature (func(ctx, *job.Job, *JobTools) (map[string]any, error)).
// Uses the local YouTubeExtractor interface instead of importing
// youtube/usecase, breaking the usecase↔jobs import cycle.
type JobHandler struct {
	svc YouTubeExtractor
	log *zap.Logger
}

// NewJobHandler constructs a job-system handler for youtube_clip.extract jobs.
func NewJobHandler(svc YouTubeExtractor, log *zap.Logger) *JobHandler {
	return &JobHandler{svc: svc, log: log}
}

// HandleJob processes a youtube_clip.extract job.
//
// Commit C (PR-C-YouTube-Cutover, June 2026): the previous implementation
// returned `result, nil` whenever the extractor returned without a Go error,
// even when `resp.OK==false && resp.Error!=""`. This produced the silent
// broker-success-on-all-failed bug (P2 #19, P0 #4 in the cutover
// roadmap). The new path runs every response through
// ClassifyExtractionResult and returns:
//   - nil          → full success (resp.Stats.Failed == 0 && Processed > 0);
//   - nil          → partial_success (typed PartialSuccessError caught via
//     errors.As, then logged + returned as result, nil);
//   - err          → terminal / retryable classification surfaced to the
//     broker so its retry/timeout policy can react.
func (h *JobHandler) HandleJob(ctx context.Context, job *job.Job, tools *jobtools.JobTools) (map[string]any, error) {
	h.log.Info("handling youtube_clip.extract job",
		zap.String("job_id", job.ID),
	)

	var req youtubetypes.ExtractRequest
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &req); err != nil {
			return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
		}
	}

	h.log.Info("YouTube extract job payload parsed",
		zap.String("job_id", job.ID),
		zap.String("url", req.URL),
		zap.Int("segments_provided", len(req.Segments)),
		zap.String("group", func() string {
			if req.Destination != nil {
				return req.Destination.Group
			}
			return ""
		}()),
	)

	if tools.Progress != nil {
		tools.Progress(5, "Job started, preparing extraction")
	}

	resp, err := h.svc.Extract(ctx, &req)
	if err != nil {
		h.log.Warn("YouTube extract job failed at higher layer",
			zap.String("job_id", job.ID),
			zap.String("url", req.URL),
			zap.Error(err))
		return nil, fmt.Errorf("extraction failed: %w", err)
	}
	if resp == nil {
		// A nil response without an error is an invalid extractor result.
		// Normalize it before classification so the broker receives the
		// same structured failure envelope instead of a handler panic.
		resp = &youtubetypes.ExtractResponse{
			Error: "extractor returned nil response",
			Items: []youtubetypes.ExtractItem{},
		}
	}

	// Commit C — classify every response. Removes the silent-success hole.
	classifyErr := ClassifyExtractionResult(resp)
	if classifyErr != nil {
		var partial *PartialSuccessError
		if errors.As(classifyErr, &partial) {
			h.log.Info("YouTube extract job finished with partial_success",
				zap.String("job_id", job.ID),
				zap.Int("processed", partial.Processed),
				zap.Int("failed", partial.Failed),
				zap.String("url", req.URL))
			result := h.buildResultMap(resp, "YouTube clip extraction finished with partial_success")
			result["partial_success"] = true
			result["processed"] = partial.Processed
			result["failed"] = partial.Failed
			return result, nil
		}
		// Retryable / terminal — propagate to broker so its retry policy can react.
		// The classification sentinels carry no per-item detail, and the broker's
		// Fail path persists ONLY the error text (result_json stays empty on Fail —
		// lifecycle_p1b invariant). So the original cause rides in the dispatch
		// error AND in a job event; the returned result map keeps the typed shape
		// (failure_class, stats, items) for any future consumer.
		retryable := errors.Is(classifyErr, ErrExtractionRetryable)
		failureClass := "terminal"
		if retryable {
			failureClass = "retryable"
		}
		detail := summarizeExtractionItems(resp.Items)
		h.log.Warn("YouTube extract job classified as failed",
			zap.String("job_id", job.ID),
			zap.String("classification", classifyErr.Error()),
			zap.Bool("retryable", retryable))
		if tools.Event != nil {
			tools.Event("extraction_failed", "YouTube clip extraction classified as "+failureClass,
				map[string]any{
					"failure_class":  failureClass,
					"classification": classifyErr.Error(),
					"stats":          resp.Stats,
					"items":          resp.Items,
				})
		}
		result := h.buildResultMap(resp, "YouTube clip extraction finished with "+failureClass+" failure")
		result["failure_class"] = failureClass
		return result, classifiedFailureError(classifyErr, failureClass, resp, detail)
	}

	if tools.Progress != nil {
		tools.Progress(100, "YouTube clip extraction completed")
	}

	result := h.buildResultMap(resp, "YouTube clip extraction completed")
	h.log.Info("YouTube extract job result",
		zap.String("job_id", job.ID),
		zap.Bool("ok", resp.OK),
		zap.Int("processed", resp.Stats.Processed),
		zap.Int("failed", resp.Stats.Failed),
		zap.Int("skipped", resp.Stats.Skipped),
		zap.String("drive_folder", resp.DriveFolderPath),
		zap.String("error", resp.Error),
	)
	return result, nil
}

// buildResultMap centralises the response → map[string]any mapping that
// the broker serialises as the job result. Extracted from HandleJob so
// the full-success, partial-success, and classifier-return paths can
// share the same shape without duplicating keys.
func (h *JobHandler) buildResultMap(resp *youtubetypes.ExtractResponse, message string) map[string]any {
	result := map[string]any{
		"ok":                resp.OK,
		"source_url":        resp.SourceURL,
		"video_id":          resp.VideoID,
		"folder":            resp.Folder,
		"stats":             resp.Stats,
		"items":             resp.Items,
		"drive_folder_id":   resp.DriveFolderID,
		"drive_folder_path": resp.DriveFolderPath,
		"message":           message,
	}
	if resp.Error != "" {
		result["error"] = resp.Error
	}
	return result
}

// classifiedFailureError returns the classifier sentinel together with a
// machine-readable failure_details JSON payload. The broker's failed-job
// path persists the dispatch error text but not the result map, so the
// structured payload is deliberately embedded in the error that reaches
// the broker. This preserves the failure class, aggregate stats, and every
// original per-item error in jobs.error and retry/dead-letter diagnostics.
func classifiedFailureError(classifyErr error, failureClass string, resp *youtubetypes.ExtractResponse, detail string) error {
	failureDetails := map[string]any{
		"failure_class":  failureClass,
		"classification": classifyErr.Error(),
		"stats":          resp.Stats,
		"items":          resp.Items,
		"error":          resp.Error,
	}
	if payload, err := json.Marshal(failureDetails); err == nil {
		return fmt.Errorf("extraction classified: %w — failure_details=%s", classifyErr, payload)
	}
	if detail != "" {
		return fmt.Errorf("extraction classified: %w — %s", classifyErr, detail)
	}
	return fmt.Errorf("extraction classified: %w", classifyErr)
}

// summarizeExtractionItems renders the per-item failure detail (name,
// start/end, status, and the ORIGINAL error — which carries the typed
// FailureCode prefix from ProcessYouTubeSegmentUseCase, e.g.
// `writer_failed: writer rejected locale-not-ready: ...`). It is the
// human-readable fallback if structured failure serialization ever fails.
func summarizeExtractionItems(items []youtubetypes.ExtractItem) string {
	var b strings.Builder
	for i, item := range items {
		if item.Status != "failed" && item.Error == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "[%d] name=%q start=%q end=%q status=%q error=%q",
			i, item.Name, item.Start, item.End, item.Status, item.Error)
	}
	return b.String()
}
