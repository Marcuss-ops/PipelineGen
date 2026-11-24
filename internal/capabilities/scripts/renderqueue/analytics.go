// Package renderqueue — analytics.go owns the durable
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
package renderqueue

import (
	"encoding/json"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	kernelrenderqueue "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// RenderAttemptAnalytics is one durable analytics row for a render attempt.
// AttemptID is the idempotency key: re-recording the same attempt converges on
// the same row instead of appending a duplicate.
type RenderAttemptAnalytics = kernelrenderqueue.RenderAttemptAnalytics

type ContentCounts = kernelrenderqueue.ContentCounts

// RenderRunID derives the run identity from an analytics job id, using the
// exact same derivation as the migration-287 backfill (write path and
// historical backfill must produce identical values):
//   - `<run>:item:<nnn>:<item>` → the `<run>` prefix (per-item fan-out);
//   - `run_...` without fan-out → the id itself (legacy full-timeline);
//   - anything else → "" (no recoverable run identity).
func RenderRunID(jobID string) string { return kernelrenderqueue.RenderRunID(jobID) }

// RenderAttemptRecorder persists one render-attempt analytics record. The
// production concrete is the platform SQLite adapter; the capability stays
// independent of the storage engine.
type RenderAttemptRecorder = kernelrenderqueue.RenderAttemptRecorder

// BuildRenderAttemptAnalytics derives the analytics record from a semantic
// OverlayPlan (content counts) and the certified queue artifact (durations,
// output metrics, SHA-256, Drive identity). It is pure and deterministic: the
// same inputs always produce the same record. A nil artifact is treated as an
// empty artifact (no certified output yet) — the content census is still
// recorded.
func BuildRenderAttemptAnalytics(attemptID string, plan capmodel.OverlayPlan, artifact *contract.RenderArtifact) RenderAttemptAnalytics {
	return BuildRenderAttemptAnalyticsWithWait(attemptID, plan, artifact, RenderCompletionMetrics{})
}

// BuildRenderAttemptAnalyticsWithWait projects both independent timing
// authorities into one analytics record: RenderMS/EncodeMS come from the
// RenderingGen artifact, while the completion/polling metrics come from the
// PipelineGen queue observer.
func BuildRenderAttemptAnalyticsWithWait(attemptID string, plan capmodel.OverlayPlan, artifact *contract.RenderArtifact, wait RenderCompletionMetrics) RenderAttemptAnalytics {
	rec := RenderAttemptAnalytics{
		AttemptID:         attemptID,
		JobID:             plan.PlanID,
		RunID:             RenderRunID(plan.PlanID),
		Content:           capoverlay.CountContent(plan),
		CompletionWaitMS:  wait.CompletionWait.Milliseconds(),
		PollingSleepMS:    wait.PollingSleep.Milliseconds(),
		PollingIntervalMS: wait.PollInterval.Milliseconds(),
		PollCount:         wait.PollCount,
		CacheLookupUS:     wait.CacheLookup.Microseconds(),
		CacheProbeUS:      wait.CacheProbe.Microseconds(),
		CacheStoreUS:      wait.CacheStore.Microseconds(),
		CacheOutcome:      wait.CacheOutcome,
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
