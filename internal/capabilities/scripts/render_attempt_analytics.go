// Package scriptgeneration — render_attempt_analytics.go owns the durable
// per-render-attempt analytics contract. It is the PipelineGen-side record of
// one Chronon render attempt produced through the RenderingGen queue: what
// content the plan carried, how long the render/encode phases took, what the
// certified output metrics were, and where the artifact landed (SHA-256 +
// Google Drive identity).
//
// The record is a pure projection of (OverlayPlan content counts + the queue
// artifact the worker returned): the builder never invents a number. Render/encode
// durations and Drive identity come verbatim from the artifact; when the worker
// did not report a phase or did not publish to Drive, the corresponding fields
// stay zero/empty.
package scriptgeneration

import (
	"context"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// RenderAttemptAnalytics is one durable analytics row for a render attempt.
// AttemptID is the idempotency key: re-recording the same attempt converges on
// the same row instead of appending a duplicate.
type RenderAttemptAnalytics struct {
	AttemptID string `json:"attempt_id"`
	JobID     string `json:"job_id"`

	// Content census (from the semantic OverlayPlan, never the item list).
	Content capoverlay.ContentCounts `json:"content"`

	// Render/encode durations (worker-measured wall time in ms). RenderMS is
	// the actual Chronon render duration and is intentionally separate from
	// CompletionWaitMS, which is client-side queue observation time.
	RenderMS int64 `json:"render_ms,omitempty"`
	EncodeMS int64 `json:"encode_ms,omitempty"`

	// Queue observation metrics. PollingSleepMS is the time spent sleeping
	// between status polls; with the production 2s cadence it quantifies the
	// polling-induced latency directly rather than attributing it to Chronon.
	CompletionWaitMS int64 `json:"completion_wait_ms,omitempty"`
	PollingSleepMS   int64 `json:"polling_sleep_ms,omitempty"`
	PollingIntervalMS int64 `json:"polling_interval_ms,omitempty"`
	PollCount        int   `json:"poll_count,omitempty"`

	// Output metrics (certified artifact facts).
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	FPSNum     int    `json:"fps_num,omitempty"`
	FPSDen     int    `json:"fps_den,omitempty"`
	FrameCount int    `json:"frame_count,omitempty"`
	DurationUS int64  `json:"duration_us,omitempty"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	SHA256     string `json:"sha256,omitempty"`

	// Google Drive publication identity (empty when not published).
	DriveFileID string `json:"drive_file_id,omitempty"`
	DriveLink   string `json:"drive_link,omitempty"`
}

// RenderAttemptRecorder persists one render-attempt analytics record. The
// production concrete is the platform SQLite adapter; the capability stays
// independent of the storage engine.
type RenderAttemptRecorder interface {
	RecordAttempt(ctx context.Context, attempt RenderAttemptAnalytics) error
}

// BuildRenderAttemptAnalytics derives the analytics record from a semantic
// OverlayPlan (content counts) and the certified queue artifact (durations,
// output metrics, SHA-256, Drive identity). It is pure and deterministic: the
// same inputs always produce the same record. A nil artifact is treated as an
// empty artifact (no certified output yet) — the content census is still
// recorded.
func BuildRenderAttemptAnalytics(attemptID string, plan capoverlay.OverlayPlan, artifact *RenderArtifact) RenderAttemptAnalytics {
	return BuildRenderAttemptAnalyticsWithWait(attemptID, plan, artifact, RenderCompletionMetrics{})
}

// BuildRenderAttemptAnalyticsWithWait projects both independent timing
// authorities into one analytics record: RenderMS/EncodeMS come from the
// RenderingGen artifact, while the completion/polling metrics come from the
// PipelineGen queue observer.
func BuildRenderAttemptAnalyticsWithWait(attemptID string, plan capoverlay.OverlayPlan, artifact *RenderArtifact, wait RenderCompletionMetrics) RenderAttemptAnalytics {
	rec := RenderAttemptAnalytics{
		AttemptID:         attemptID,
		JobID:             plan.PlanID,
		Content:           capoverlay.CountContent(plan),
		CompletionWaitMS:  wait.CompletionWait.Milliseconds(),
		PollingSleepMS:    wait.PollingSleep.Milliseconds(),
		PollingIntervalMS: wait.PollInterval.Milliseconds(),
		PollCount:         wait.PollCount,
	}
	if artifact == nil {
		return rec
	}
	rec.RenderMS = artifact.RenderMS
	rec.EncodeMS = artifact.EncodeMS
	rec.Width = artifact.Width
	rec.Height = artifact.Height
	rec.FPSNum = artifact.FPSNum
	rec.FPSDen = artifact.FPSDen
	rec.FrameCount = artifact.FrameCount
	rec.DurationUS = artifact.DurationUS
	rec.SizeBytes = artifact.SizeBytes
	rec.SHA256 = artifact.SHA256
	rec.DriveFileID = artifact.DriveFileID
	rec.DriveLink = artifact.DriveLink
	return rec
}
