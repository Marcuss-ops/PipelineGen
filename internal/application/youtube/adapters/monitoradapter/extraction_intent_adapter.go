// Package monitoradapter — extraction_intent_adapter.go: the canonical
// concrete JobEnqueuer implementation that lives in the youtube domain
// so the monitor package can drop its direct youtubetypes dependency.
//
// Fase 8 (July 2026, Spina Dorsale): previously, the concrete binding
// that wires jobs.Service + channels.Service to the monitor's
// JobEnqueuer port lived at internal/application/assets/monitor/
// extraction_enqueuer.go. That file imported youtubetypes for the
// marshal step (EnqueueExtractRequest → youtubetypes.ExtractRequest),
// creating a one-way adverse coupling between monitor and youtube.
// This commit moves the binding into youtube's sibling package as
// `monitoradapter`, so:
//
//   - monitor package: CompositionDeps.Enqueuer is satisfied by any
//     monitor.JobEnqueuer (this adapter is one). The monitor package
//     no longer imports the youtube package directly. monitor owns
//     its ExtractionIntent + ExtractionSegment DTOs.
//   - monitoradapter package (here): owns the marshal
//     monitor.ExtractionIntent → youtubetypes.ExtractRequest. The
//     youtubetypes import is now local to monitoradapter (a sibling
//     adapter within youtube's domain tree) — the right direction
//     for the dependency inverse rule: monitor no longer pulls in
//     youtube transitively, the canonical marshal site here owns
//     the dto import surface.
//   - composition root (lifecycle.go): wraps
//     root.Jobs.Service + channelsSvc into the adapter and drops it
//     into CompositionDeps.Enqueuer. Replaces the pre-Fase-8 call
//     site monitor.NewExtractionEnqueuer(...).
//
// Compile-time assertion `var _ monitor.JobEnqueuer = (*ExtractionIntentAdapter)(nil)`
// pins the port surface so signature drift on monitor.JobEnqueuer
// surfaces at build time here, not as a nil-deref panic inside
// checkChannel's per-video worker.
//
// Three contracts pinned by extraction_intent_adapter_test.go (moved
// verbatim from monitor/extraction_enqueuer_test.go):
//
//  1. ActiveKey collision NO-OP — if a non-terminal job already exists
//     under ActiveKey "channel_sync_<VideoID>", return nil immediately.
//  2. Broker-side enqueue on happy path — Marshal ExtractionIntent to
//     youtubetypes.ExtractRequest and emit via jobs.Service.Enqueue.
//  3. Per-video channels.UpdateCursor REMOVED (Commit D, June 2026) —
//     cycle-end MAX(discovered_at) → category_channels.last_cursor
//     replaced it; this adapter no longer calls UpdateCursor.
package monitoradapter

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	monitor "github.com/Marcuss-ops/PipelineGen/internal/application/assets/monitor"
	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	jobtools "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	jobyoutube "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// JobsEnqueuerSvc is the minimum-surface port from jobs.Service.
// Pattern 0 invariant: the concrete *jobtools.Service satisfies this
// implicitly via the compile-time assertion at the bottom of this file.
// New EnqueueRequest field additions that the adapter doesn't consume
// surface as a lint warning rather than a silent drop.
type JobsEnqueuerSvc interface {
	FindActiveByKey(ctx context.Context, activeKey string) (*job.Job, error)
	Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error)
}

// ChannelsCursorSvc is the minimum-surface port from channels.Service.
// Pattern 0 invariant: *channels.Service satisfies this via the
// compile-time assertion at the bottom of this file.
//
// NOTE on Commit D (June 2026, PR-D YouTube Channel Monitor cutover):
// the per-video cursor update path is REMOVED from this adapter.
// Cycle-end MAX(discovered_at) → category_channels.last_cursor is
// the new path (see monitor/discovery.go::recordCycleEndWatermark).
// The channelsSvc field on the struct is RETAINED for backward compat
// (composition root + tests still wire it); removal is tracked as
// a follow-up so this commit lands as a focused diff.
type ChannelsCursorSvc interface {
	UpdateCursor(ctx context.Context, cmd channels.UpdateCursorCommand) error
}

// ExtractionIntentAdapter is the canonical concrete JobEnqueuer
// adapter for the channel monitor's durable-emission path. Implements
// monitor.JobEnqueuer (declared in monitor/ports.go) via the
// compile-time assertion at the bottom of this file.
//
// Holds the two port interfaces + zap.Logger. Translation
// monitor.ExtractionIntent → youtubetypes.ExtractRequest happens
// inside EnqueueExtract via translateSegments.
type ExtractionIntentAdapter struct {
	jobsSvc     JobsEnqueuerSvc
	channelsSvc ChannelsCursorSvc
	log         *zap.Logger
}

// NewExtractionIntentAdapter constructs the concrete JobEnqueuer
// binding from canonical in-process services. Wired by
// internal/app/lifecycle.go into monitor.CompositionDeps.Enqueuer
// (replacing the pre-Fase-8 monitor.NewExtractionEnqueuer call site).
//
// Production wires root.Jobs.Service (*jobtools.Service) + the
// canonical *channels.Service, both satisfying the port interfaces
// implicitly.
//
// nil-tolerant on log only. A nil jobsSvc is treated as a hard wiring
// failure (the adapter returns an error so the monitor's per-video
// error log captures the gap). A nil channelsSvc degrades to a
// cursor-update no-op (the broker-side enqueue is still recorded)
// so a transient channel-service outage cannot prevent new jobs
// from landing in the durable-jobs queue.
func NewExtractionIntentAdapter(jobsSvc JobsEnqueuerSvc, channelsSvc ChannelsCursorSvc, log *zap.Logger) *ExtractionIntentAdapter {
	if log == nil {
		log = zap.NewNop()
	}
	return &ExtractionIntentAdapter{
		jobsSvc:     jobsSvc,
		channelsSvc: channelsSvc,
		log:         log,
	}
}

// EnqueueExtract implements monitor.JobEnqueuer.
//
//  1. Build canonical ActiveKey = monitor.ActiveKeyPrefix + intent.VideoID.
//     monitor.ActiveKeyPrefix is the source of truth for the prefix
//     string (godlike/06 "one owner per fact" — owned by monitor
//     domain; sibling adapter reads across the boundary, not
//     redefining locally).
//  2. Pre-check via jobsSvc.FindActiveByKey; if a non-terminal job
//     already exists, return nil immediately (contract 1, idempotency
//     pre-check). The pre-check keeps UpdateCursor (and any other
//     per-video side-effect) from advancing on a video we did not
//     actually POST in this tick. The broker-level dedup is the
//     safety net; the pre-check is the explicit gate.
//  3. Marshal monitor.ExtractionIntent → youtubetypes.ExtractRequest.
//     Field-by-field copy via translateSegments.
//  4. Emit durable job via jobsSvc.Enqueue with ActiveKey set.
//     Genuine broker errors (e.g. payload too large, repo write
//     failure) surface here — they are NOT best-effort.
//  5. (REMOVED in Commit D, June 2026) per-video channels.UpdateCursor
//     is replaced by the cycle-end watermark; see
//     monitor/discovery.go::recordCycleEndWatermark.
//
// Return-shape:
//   - nil on the happy path AND on the collision-NO-OP path.
//   - wrapped error on hard-wiring failures (nil jobsSvc) and on
//     conservative gate failures (FindActiveByKey error).
//   - wrapped error on broker-side failures (NOT best-effort).
func (a *ExtractionIntentAdapter) EnqueueExtract(ctx context.Context, intent monitor.ExtractionIntent) error {
	activeKey := monitor.ActiveKeyPrefix + intent.VideoID

	// ── 1. Hard wiring guard (defensive) ──────────────────────────
	if a.jobsSvc == nil {
		a.log.Error("extraction_intent_adapter: jobsSvc is nil, refusing to enqueue (composition bug)",
			zap.String("video_id", intent.VideoID))
		return fmt.Errorf("extraction_intent_adapter: jobsSvc is nil (composition bug)")
	}

	// ── 2. Collision pre-check (contract 1) ────────────────────────
	existing, err := a.jobsSvc.FindActiveByKey(ctx, activeKey)
	if err != nil {
		// Conservative: a FindActiveByKey failure means we cannot
		// distinguish collision from non-collision. Surface the error
		// so the monitor's per-video log captures the gap rather than
		// silently double-posting.
		a.log.Warn("extraction_intent_adapter: FindActiveByKey failed; refusing to proceed (collision check broken)",
			zap.String("video_id", intent.VideoID),
			zap.String("active_key", activeKey),
			zap.Error(err))
		return fmt.Errorf("collision check failed for active_key=%q: %w", activeKey, err)
	}
	if existing != nil && !existing.IsTerminal() {
		a.log.Info("extraction_intent_adapter: ActiveKey collision — no-op per contract 1",
			zap.String("video_id", intent.VideoID),
			zap.String("active_key", activeKey),
			zap.String("existing_job_id", existing.ID),
			zap.String("existing_status", string(existing.Status)))
		return nil
	}

	// ── 3. Marshal ExtractionIntent → youtubetypes.ExtractRequest ───
	extractReq := youtubetypes.ExtractRequest{
		URL:      intent.URL,
		Segments: translateSegments(intent.Segments),
		Destination: &youtubetypes.DestinationRequest{
			Group:    intent.Group,
			FolderID: intent.DriveFolderID,
		},
	}

	// ── 4. Emit durable job via the broker ─────────────────────────
	if _, err := a.jobsSvc.Enqueue(ctx, &job.EnqueueRequest{
		Type:      jobyoutube.TypeClipExtract,
		VideoName: intent.Title,
		ActiveKey: activeKey,
		Payload:   extractReq,
	}); err != nil {
		return fmt.Errorf("enqueue channel_sync job (active_key=%q): %w", activeKey, err)
	}

	a.log.Debug("extraction_intent_adapter: enqueued youtube_clip.extract job",
		zap.String("video_id", intent.VideoID),
		zap.String("active_key", activeKey),
		zap.String("title", intent.Title),
		zap.Int("segments", len(intent.Segments)),
		zap.String("destination_group", intent.Group),
		zap.String("destination_folder_id", intent.DriveFolderID))

	// ── 5. Cursor update — REMOVED in Commit D ──────────────────────
	// (No-op: cycle-end MAX(discovered_at) → category_channels.last_cursor
	// replaced this. See monitor/discovery.go::recordCycleEndWatermark.)
	return nil
}

// translateSegments converts []monitor.ExtractionSegment (monitor-owned,
// byte-stable mirror) to []youtubetypes.Segment (youtube-owned). The
// translation is a field-by-field copy so the youtube_clip.extract
// handler can still decode the canonical payload shape.
//
// Nil-safe: nil input returns nil (matches JSON-marshal round-trip
// behaviour — a missing segments array round-trips as nil, not []).
// The allocation is sized at len(in); pre-existing sinks (the broker
// payload marshal) accept any slice length.
func translateSegments(in []monitor.ExtractionSegment) []youtubetypes.Segment {
	if in == nil {
		return nil
	}
	out := make([]youtubetypes.Segment, len(in))
	for i, s := range in {
		out[i] = youtubetypes.Segment{
			Start:            s.Start,
			End:              s.End,
			Name:             s.Name,
			Category:         s.Category,
			SourceTitle:      s.SourceTitle,
			SourceChannel:    s.SourceChannel,
			Tags:             s.Tags,
			Summary:          s.Summary,
			Topics:           s.Topics,
			Speakers:         s.Speakers,
			MentionedPeople:  s.MentionedPeople,
			Hook:             s.Hook,
			QualityScore:     s.QualityScore,
			SearchVisibility: s.SearchVisibility,
			Texts:            s.Texts,
		}
	}
	return out
}

// Compile-time assertions (Pattern 0 invariants from
// AGENTS.md / godlike/06 §"Database and config ownership"):
//
//   - *ExtractionIntentAdapter must satisfy monitor.JobEnqueuer
//     (the upstream port surface). Signature drift on
//     JobEnqueuer.EnqueueExtract → build failure HERE.
//   - *jobtools.Service must satisfy JobsEnqueuerSvc
//     (the downstream port surface over the jobs package).
//     Signature drift on jobs.Service.Enqueue OR
//     jobs.Service.FindActiveByKey → build failure HERE.
//   - *channels.Service must satisfy ChannelsCursorSvc (the
//     downstream port surface over the channels package).
//     Signature drift on channels.Service.UpdateCursor →
//     build failure HERE.
//
// Each assertion converts what would be a runtime panic into a
// build-time error.
var (
	_ monitor.JobEnqueuer = (*ExtractionIntentAdapter)(nil)
	_ JobsEnqueuerSvc     = (*jobtools.Service)(nil)
	_ ChannelsCursorSvc   = (*channels.Service)(nil)
)
