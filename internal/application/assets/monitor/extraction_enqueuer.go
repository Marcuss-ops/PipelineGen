// Package monitor — extraction_enqueuer.go: the concrete JobEnqueuer
// adapter for the Channel Monitor's durable-emission path.
//
// Step 9 follow-up (PR-MONITOR-ENQUEUER-WIRE, June 2026): this file
// REPLACES the `NewUnboundJobEnqueuer()` placeholder stub from
// ports.go with the real binding that wires jobs.Service +
// channels.Service through the canonical ActiveKey-driven idempotency
// surface.
//
// The adapter satisfies monitor.JobEnqueuer (declared in ports.go)
// so it can be dropped into CompositionDeps.Enqueuer without further
// changes. It lives inside the monitor package (rather than as a
// sibling like transcripts/ytdlp_subtitles.go or
// semantic/ollama_analyzer.go) for two architectural reasons:
//
//  1. The two siblings in Step 9 commit 2 (6442aacb) own concrete
//     adapters that wrap EXTERNAL systems — yt-dlp subprocess +
//     VTT regex (YTDLPSubtitleAdapter) and Ollama HTTP +
//     JSON parse (OllamaAnalyzer). Per the Step 9 design note in
//     AGENTS.md Pattern 0, sibling packages are reserved for
//     "external-concern adapters". The ExtractionEnqueuer wires two
//     internal‑to‑internal canonical services (jobs.Service +
//     channels.Service), so it does not fit the sibling-packaging
//     pattern.
//
//  2. The JobEnqueuer port, its DTO types (EnqueueExtractRequest),
//     and its placeholder stub (unboundJobEnqueuer) all live in
//     monitor/ports.go today. Colocating the concrete adapter with
//     the port + stub keeps the load-bearing compile-time assertion
//     `var _ JobEnqueuer = (*ExtractionEnqueuer)(nil)` in a single
//     ownership package. The alternative — placing the adapter in
//     `internal/application/jobs/extraction_enqueuer.go` and
//     importing monitor for the interface — would create a
//     monitor↔jobs import cycle (monitor/enqueue.go already imports
//     downstream of jobs/). Doc'd here so a future split of
//     sub-package (a hypothetical Blocco 7 cleanup) becomes the
//     natural moment to relocate this adapter too.
//
// 
// Commit H Phase 2 (June 2026): the the durable channel-sync method method +
// its job-type binding binding were removed from monitor/enqueue.go.
// The canonical channel-sync path now goes through monitor.scheduler.go
// directly (no durable channel-sync job round-trip).
//
// Three contracts this adapter MUST implement.
//
//  1. ActiveKey collision NO-OP — if a non-terminal job already
//     exists under ActiveKey "channel_sync_<VideoID>", return nil
//     immediately. This is per-tick dedup: the channel monitor's
//     exp-backoff retry can re-emit the same VideoID multiple times,
//     and we MUST NOT duplicate-post a job for it. The same
//     contract is pinned in monitor_enqueue_test.go for the
//     per-port stub, with `enqueueCalls==1 && enqueuedRequests==0
//     && cursorUpdates==0` as the verification shape.
//
//  2. Cursor update on success — if Enqueue succeeds, UpdateCursor
//     MUST be invoked with (channel_id, video_id) to persist the
//     per-channel sync state so the scheduler resumes from there on
//     the next tick.
//
//  3. Cursor update failure tolerance — if UpdateCursor fails, the
//     error is logged at WARN but the adapter returns nil. The
//     broker-recorded enqueue is the source of truth; cursor updates
//     are an observability convenience, not a correctness gate.
//     Pinning: cursor-failure path emits `enqueueCalls==1 &&
//     enqueuedRequests==1 && cursorUpdates==1 && returnErr==nil`.
//
// ActiveKey construction is delegated to a const so a future caller
// (e.g. a CLI backfill tool) can re-use the exact same prefix
// without grepping for the literal string.
package monitor

import (
	"context"
	"fmt"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	jobtools "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	"go.uber.org/zap"
)

// ActiveKeyPrefix is the canonical job.ActiveKey prefix for
// channel-sync extraction jobs. Per the Channel Monitor Step 9 design
// spec: every durable extraction job enqueued by the Channel Monitor
// uses "channel_sync_<VideoID>" so the broker's per-ActiveKey
// idempotency dedupes across the monitor's per-tick retry window.
//
// Exported so future tooling (admin CLIs, bulk re-enqueue scripts,
// the Wave 15 remote-worker fallback path for channel-sync) can
// re-use the same prefix without duplicating the literal.
const ActiveKeyPrefix = "channel_sync_"

// JobsEnqueuerSvc is the minimum-surface port the ExtractionEnqueuer
// needs from jobs.Service. The concrete *jobtools.Service satisfies
// this implicitly via the compile-time assertion at the bottom of
// this file.
//
// Why an interface (not *jobtools.Service directly): unit tests must
// inject a fake without spinning up the durable-jobs SQLite broker;
// the interface is the standard AGENTS.md Pattern 0 (port abstraction
// layer) testability seam. Reducing to the 2-method surface also
// locks future drift — if jobs.Service adds a new arg to Enqueue,
// the assertion at the bottom breaks here, not at runtime in the
// per-tick retry path. New EnqueueRequest field additions that the
// adapter doesn't consume surface as a lint warning rather than a
// silent drop.
type JobsEnqueuerSvc interface {
	FindActiveByKey(ctx context.Context, activeKey string) (*jobservice.Job, error)
	Enqueue(ctx context.Context, req *jobservice.EnqueueRequest) (*jobservice.Job, error)
}

// ChannelsCursorSvc is the minimum-surface port the ExtractionEnqueuer
// needs from channels.Service. The concrete *channels.Service
// satisfies this implicitly via the compile-time assertion at the
// bottom of this file. Same Pattern 0 rationale as JobsEnqueuerSvc.
type ChannelsCursorSvc interface {
	UpdateCursor(ctx context.Context, cmd channels.UpdateCursorCommand) error
}

// ExtractionEnqueuer is the canonical concrete JobEnqueuer adapter.
// Holds the two port interfaces + zap.Logger; satisfies the
// monitor.JobEnqueuer surface (declared in ports.go) via the
// compile-time assertion at the bottom of this file.
type ExtractionEnqueuer struct {
	jobsSvc     JobsEnqueuerSvc
	channelsSvc ChannelsCursorSvc
	log         *zap.Logger
}

// NewExtractionEnqueuer constructs the concrete JobEnqueuer binding
// from canonical in-process services. Wired by internal/app/lifecycle.go
// into monitor.CompositionDeps.Enqueuer (replacing the
// NewUnboundJobEnqueuer placeholder). Production passes
// root.Jobs.Service (*jobtools.Service) and the canonical
// *channels.Service; both satisfy the port interfaces implicitly.
// Log defaults to zap.NewNop() if nil so unit tests don't need to inject.
//
// nil-tolerant on log only. At the field-init boundary: a nil
// jobsSvc is treated as a hard wiring failure (the adapter returns
// an error so the monitor's per-video error log captures the gap);
// a nil channelsSvc degrades to a cursor-update no-op (the broker-
// side enqueue is still recorded) so a transient channel-service
// outage cannot prevent new jobs from landing in the durable-jobs
// queue.
func NewExtractionEnqueuer(jobsSvc JobsEnqueuerSvc, channelsSvc ChannelsCursorSvc, log *zap.Logger) *ExtractionEnqueuer {
	if log == nil {
		log = zap.NewNop()
	}
	return &ExtractionEnqueuer{
		jobsSvc:     jobsSvc,
		channelsSvc: channelsSvc,
		log:         log,
	}
}

// EnqueueExtract implements monitor.JobEnqueuer.
//
//  1. Build canonical ActiveKey = "channel_sync_<req.VideoID>".
//  2. Pre-check via jobsSvc.FindActiveByKey; if a non-terminal job
//     already exists under that ActiveKey, return nil immediately
//     (contract 1). The broker-side dedup is a defence-in-depth
//     safety net, but the pre-check here keeps UpdateCursor from
//     advancing the channel cursor on a video we did not actually
//     POST in this tick.
//  3. Marshal monitor.EnqueueExtractRequest → youtubetypes.ExtractRequest
//     payload (consumed by the durable youtube_clip.extract handler
//     in internal/application/youtube/jobs/job_handler.go). The
//     Group + DriveFolderID collapse into the ExtractRequest's
//     Destination struct.
//  4. Emit durable job via jobsSvc.Enqueue with ActiveKey set.
//     Genuine broker errors (e.g. payload too large, repo write
//     failure) surface here — they are NOT best-effort.
//  5. Call channelsSvc.UpdateCursor with (channel_id, video_id)
//     on a nil-tolerant-degrade path. Cursor errors are logged at
//     WARN and swallowed — broker-recorded enqueue is the source of
//     truth for the next scheduler tick (contract 2, 3).
//  6. Return nil on the happy path. Surface genuine broker errors.
//     Cursor errors NEVER surface to the caller.
func (e *ExtractionEnqueuer) EnqueueExtract(ctx context.Context, req EnqueueExtractRequest) error {
	activeKey := ActiveKeyPrefix + req.VideoID

	// ── 1. Hard wiring guard (defensive) ──────────────────────────
	if e.jobsSvc == nil {
		e.log.Error("extraction_enqueuer: jobsSvc is nil, refusing to enqueue (composition bug)",
			zap.String("video_id", req.VideoID))
		return fmt.Errorf("extraction_enqueuer: jobsSvc is nil (composition bug)")
	}

	// ── 2. Collision pre-check (contract 1) ────────────────────────
	// jobsSvc.Enqueue() also internally does FindActiveByKey +
	// short-circuit when ActiveKey is set, but the pre-check here is
	// required so we can skip BOTH the broker call AND the
	// per-contract-3 UpdateCursor before deciding the watchpoint
	// should not advance. The broker-level dedup is the safety net;
	// the pre-check is the explicit gate.
	existing, err := e.jobsSvc.FindActiveByKey(ctx, activeKey)
	if err != nil {
		// Conservative: a FindActiveByKey failure means we cannot
		// distinguish collision from non-collision. Surface the error
		// so the monitor's per-video log captures the gap rather than
		// silently double-posting.
		e.log.Warn("extraction_enqueuer: FindActiveByKey failed; refusing to proceed (collision check broken)",
			zap.String("video_id", req.VideoID),
			zap.String("active_key", activeKey),
			zap.Error(err))
		return fmt.Errorf("collision check failed for active_key=%q: %w", activeKey, err)
	}
	if existing != nil && !existing.IsTerminal() {
		e.log.Info("extraction_enqueuer: ActiveKey collision — no-op per contract 1",
			zap.String("video_id", req.VideoID),
			zap.String("active_key", activeKey),
			zap.String("existing_job_id", existing.ID),
			zap.String("existing_status", string(existing.Status)))
		return nil
	}

	// ── 3. Marshal canonical ExtractRequest payload ────────────────
	// Destination struct carries both Group (Drive category) and
	// FolderID (channel-level override + ClipsFolder global
	// fallback, populated by monitor.enqueueFromAnalysis upstream).
	extractReq := youtubetypes.ExtractRequest{
		URL:      req.URL,
		Segments: req.Segments,
		Destination: &youtubetypes.DestinationRequest{
			Group:    req.Group,
			FolderID: req.DriveFolderID,
		},
	}

	// ── 4. Emit durable job via the broker ─────────────────────────
	if _, err := e.jobsSvc.Enqueue(ctx, &jobservice.EnqueueRequest{
		Type:      jobservice.TypeYouTubeClipExtract,
		VideoName: req.Title,
		ActiveKey: activeKey,
		Payload:   extractReq,
	}); err != nil {
		return fmt.Errorf("enqueue channel_sync job (active_key=%q): %w", activeKey, err)
	}

	e.log.Debug("extraction_enqueuer: enqueued youtube_clip.extract job",
		zap.String("video_id", req.VideoID),
		zap.String("active_key", activeKey),
		zap.String("title", req.Title),
		zap.Int("segments", len(req.Segments)),
		zap.String("destination_group", req.Group),
		zap.String("destination_folder_id", req.DriveFolderID))

	// ── 5. Cursor update — REMOVED in Commit D ──────────────────────
	// Pre-Commit-D contracts 2 + 3 activated a per-video channels.UpdateCursor
	// call right after the broker-side Enqueue succeeded. Commit D (June 2026,
	// PR-D YouTube Channel Monitor cutover) replaces this best-effort
	// per-video write with the cycle-end watermark: discovery.go::recordCycleEndWatermark
	// (defer in checkChannel) writes category_channels.last_cursor to
	// MAX(discovered_at) from the youtube_discoveries ledger exactly ONCE
	// per scheduler cycle. The new path is durable at the table level
	// (no per-row best-effort degrade), and a SQLite transient error
	// there is a single, observable ledger write rather than N per-video
	// silent degrades.
	//
	// The channelsSvc field on this struct is RETAINED for backward compat
	// (lifecycle.go + extraction_enqueuer_test.go still wire it). Removal is
	// tracked as a follow-up so this commit lands as a focused diff.
	return nil
}

// Compile-time assertions (Pattern 0 invariants from
// AGENTS.md / godlike/06 §"Database and config ownership"):
//
//   - *ExtractionEnqueuer must satisfy monitor.JobEnqueuer
//     (the upstream port surface). Signature drift on
//     JobEnqueuer.EnqueueExtract → build failure HERE.
//
//   - *jobtools.Service must satisfy JobsEnqueuerSvc
//     (the downstream port surface over the jobs package).
//     Signature drift on jobs.Service.Enqueue OR
//     jobs.Service.FindActiveByKey → build failure HERE.
//
//   - *channels.Service must satisfy ChannelsCursorSvc (the
//     downstream port surface over the channels package).
//     Signature drift on channels.Service.UpdateCursor →
//     build failure HERE.
//
// Each assertion converts what would be a runtime panic into a
// build-time error, which is the load-bearing guarantee that
// future field additions on EnqueueExtractRequest, JobEnqueuer,
// jobs.Service.Enqueue, or channels.Service.UpdateCursor surface
// at compile time rather than in a per-tick retry path.
var (
	_ JobEnqueuer       = (*ExtractionEnqueuer)(nil)
	_ JobsEnqueuerSvc   = (*jobtools.Service)(nil)
	_ ChannelsCursorSvc = (*channels.Service)(nil)
)
