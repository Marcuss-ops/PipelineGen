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
	"encoding/json"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// RenderAttemptAnalytics is one durable analytics row for a render attempt.
// AttemptID is the idempotency key: re-recording the same attempt converges on
// the same row instead of appending a duplicate.
type RenderAttemptAnalytics struct {
	AttemptID string `json:"attempt_id"`
	JobID     string `json:"job_id"`
	// ItemID is the per-overlay correlation key: the semantic OverlayItem.ID
	// that this short video renders. Empty for the legacy full-timeline path
	// (multiple items in one job) and non-empty for the production per-item
	// path (one child plan per item, each with a fresh queue id).
	ItemID string `json:"item_id,omitempty"`

	// Content census (from the semantic OverlayPlan, never the item list).
	Content capoverlay.ContentCounts `json:"content"`

	// Render/encode durations (worker-measured wall time in ms). RenderMS is
	// the actual Chronon render duration and is intentionally separate from
	// CompletionWaitMS, which is client-side queue observation time.
	RenderMS int64 `json:"render_ms,omitempty"`
	EncodeMS int64 `json:"encode_ms,omitempty"`

	// Producer and queue lifecycle timestamps are optional observed facts.
	// Pointers preserve the distinction between "timestamp absent" and a
	// fabricated zero time; consumers must only derive durations when both
	// endpoints are present and ordered.
	SubmitStartedAt     *time.Time `json:"submit_started_at,omitempty"`
	SubmitAcceptedAt    *time.Time `json:"submit_accepted_at,omitempty"`
	WaitStartedAt       *time.Time `json:"wait_started_at,omitempty"`
	WaitFinishedAt      *time.Time `json:"wait_finished_at,omitempty"`
	QueueQueuedAt       *time.Time `json:"queue_queued_at,omitempty"`
	QueueStartedAt      *time.Time `json:"queue_started_at,omitempty"`
	QueueCompletedAt    *time.Time `json:"queue_completed_at,omitempty"`
	ArtifactAvailableAt *time.Time `json:"artifact_available_at,omitempty"`
	Outcome             string     `json:"outcome,omitempty"` // bounded: success | failure

	// Queue observation metrics. PollingSleepMS is the time spent sleeping
	// between status polls; it quantifies polling-induced latency directly
	// rather than attributing it to Chronon. On the event-driven path (the
	// queue's job-status long poll) there is no client-side sleep, so it stays
	// 0 while CompletionWaitMS still measures the true end-to-end observation
	// wait.
	CompletionWaitMS  int64 `json:"completion_wait_ms,omitempty"`
	PollingSleepMS    int64 `json:"polling_sleep_ms,omitempty"`
	PollingIntervalMS int64 `json:"polling_interval_ms,omitempty"`
	PollCount         int   `json:"poll_count,omitempty"`

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

	// Per-overlay identity and artifact profile. Preserved verbatim from the
	// certified artifact so a per-item trace can answer "which backend/version
	// produced this item, what profile/codec/container it certified, and what
	// resolution it carried" without re-probing bytes.
	Backend        string `json:"backend,omitempty"`
	ChrononVersion string `json:"chronon_version,omitempty"`
	ProfileID      string `json:"profile_id,omitempty"`
	Codec          string `json:"codec,omitempty"`
	CodecProfile   string `json:"codec_profile,omitempty"`
	Container      string `json:"container,omitempty"`
	PixelFormat    string `json:"pixel_format,omitempty"`

	// Worker-measured per-phase durations (ms) from the queue artifact's
	// metrics map. Zero means the worker did not report the phase; a missing
	// measurement is never fabricated as zero in the phase breakdown (the run
	// projection skips it, the analytics row stores 0).
	MaterializeMS  int64 `json:"materialize_ms,omitempty"`
	PlanMS         int64 `json:"plan_ms,omitempty"`
	ProbeMS        int64 `json:"probe_ms,omitempty"`
	HashMS         int64 `json:"hash_ms,omitempty"`
	UploadMS       int64 `json:"objectstore_upload_ms,omitempty"`
	DrivePublishMS int64 `json:"drive_publish_ms,omitempty"`

	// Full Chronon telemetry: the bounded telemetry summary (chronon_telemetry,
	// schema chronon3d.render-telemetry-summary.v1) plus the numeric metrics
	// projection. Stored verbatim (no re-derivation) so per-overlay analysis
	// can separate queue wait, GPU lane wait, decode/composite/encode, GPU/
	// NVENC utilization and VRAM from the worker's own phase walls.
	MetricsJSON          string `json:"metrics_json,omitempty"`
	ChrononTelemetryJSON string `json:"chronon_telemetry,omitempty"`

	// Raw deep-profile sidecar reference (content-addressed preservation).
	// Only the small reference rides the artifact — the per-frame array is
	// never inlined. Empty when the worker could not preserve the sidecar
	// (fail-open).
	ChrononTimingStorageKey  string `json:"chronon_timing_storage_key,omitempty"`
	ChrononTimingURL         string `json:"chronon_timing_url,omitempty"`
	ChrononTimingSHA256      string `json:"chronon_timing_sha256,omitempty"`
	ChrononTimingSizeBytes   int64  `json:"chronon_timing_size_bytes,omitempty"`
	ChrononTimingContentType string `json:"chronon_timing_content_type,omitempty"`
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
	// Per-overlay correlation: when the plan carries exactly one item (the
	// production separate-item path builds one child plan per semantic item),
	// that item's ID is the correlation key that lets a trace distinguish
	// parallel renders of the same run. For the legacy full-timeline path
	// (multiple items in one plan) the row remains unattributed to a single
	// item.
	if len(plan.Items) == 1 {
		rec.ItemID = plan.Items[0].ID
	}
	if artifact == nil {
		return rec
	}
	// Preserve the item identity the caller may have attributed via the
	// fresh-render path (the job's own artifact is still the authority for
	// output metrics, but the plan's single-item derivation is the correlation).
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
	rec.Backend = artifact.Backend
	rec.ChrononVersion = artifact.ChrononVersion
	rec.ProfileID = artifact.ProfileID
	rec.Codec = artifact.Codec
	rec.CodecProfile = artifact.CodecProfile
	rec.Container = artifact.Container
	rec.PixelFormat = artifact.PixelFormat
	rec.MaterializeMS = artifact.MaterializeMS
	rec.PlanMS = artifact.PlanMS
	rec.ProbeMS = artifact.ProbeMS
	rec.HashMS = artifact.HashMS
	rec.UploadMS = artifact.UploadMS
	rec.DrivePublishMS = artifact.DrivePublishMS
	if len(artifact.Metrics) > 0 {
		if b, err := json.Marshal(artifact.Metrics); err == nil {
			rec.MetricsJSON = string(b)
		}
	}
	if len(artifact.ChrononTelemetry) > 0 {
		rec.ChrononTelemetryJSON = string(artifact.ChrononTelemetry)
	}
	rec.ChrononTimingStorageKey = artifact.ChrononTimingStorageKey
	rec.ChrononTimingURL = artifact.ChrononTimingURL
	rec.ChrononTimingSHA256 = artifact.ChrononTimingSHA256
	rec.ChrononTimingSizeBytes = artifact.ChrononTimingSizeBytes
	rec.ChrononTimingContentType = artifact.ChrononTimingContentType
	return rec
}
