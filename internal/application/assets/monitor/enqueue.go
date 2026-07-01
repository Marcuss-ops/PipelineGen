// Package monitor — enqueue.go: durable-job emission + channel cursor update.
//
// Step 9 (June 2026, Channel Monitor Blocco 6 architectural rewrite):
// the package is now exactly 5 production files (scheduler.go,
// discovery.go, analyzer.go, enqueue.go, ports.go). This file owns:
//
//   - enqueueFromAnalysis: builds the EnqueueExtractRequest from the
//     analyzeVideo result, resolves the DriveFolderID (channel-level
//     override + ClipsFolder global fallback), and delegates the actual
//     marshal + jobs.Enqueue + channels.UpdateCursor call to the
//     JobEnqueuer port.
//   - HandleChannelSyncJob: the durable youtube.channel.sync handler.
//     Registered via jobsSvc.RegisterHandler(TypeYouTubeChannelSync, ...)
//     so a queued channel-sync tick goes through this rather than inline.
//   - RegisterChannelSyncHandler: the public binding the lifecycle
//     calls after NewChannelMonitor.
//   - tryReserve: the per-channel budget CAS, only consumed after
//     passing the AI gate (see discovery.go processVideo).
//
// Why a port? Splitting the Enqueue logic out of the analyzer lets the
// analyzer nonchalantly return "no segments → skip" without paying for
// marshal + enqueue. The actual marshaling + jobs.Enqueue + cursor
// update + metrics observation go through m.enqueuer.EnqueueExtract so
// the concrete adapter can own ActiveKey construction + payload shape
// in the cleanest place.
package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	jobtools "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
)

// enqueueFromAnalysis builds the canonical ExtractRequest-shaped payload
// and delegates emission to the JobEnqueuer port, then records the
// per-outcome ledger state. Returns nil on the enqueued path; returns
// the underlying error on the rejected path.
//
// DriveFolderID resolution rule (preserved verbatim from the pre-Step-9
// process_video.go::enqueueClipExtract):
//   - channel.DriveFolderID if set;
//   - cfg.Drive.ClipsFolder() fallback if channel-level is empty;
//   - empty string is also OK (extraction service will route via
//     category-root + group subfolder as before).
//
// Commit D (June 2026, PR-D YouTube Channel Monitor cutover):
//   - The function now returns error so recordDiscoveryAndClassify can
//     classify the outcome as Enqueued vs Rejected strictly.
//   - ledgerID is the row id from the canonical TryReserve that won
//     the (channel_id, video_id) leader-election INSERT. The MarkEnqueued
//     / MarkRejected updates are issued here so the ledger row's outcome
//     arrives at `enqueued` only when the broker-side emit succeeds.
//     This eliminates silent-success paths where the broker emitted but
//     the ledger stayed at `pending` (pre-Commit D) or where the broker
//     failed but the ledger still showed `pending` (no audit trail).
//   - The per-video channels.UpdateCursor (extraction_enqueuer contract 3)
//     is REMOVED. Cycle-end MAX(discovered_at) → category_channels.
//     last_cursor is the new monotonic write path; see
//     discovery.go::recordCycleEndWatermark.
func (m *ChannelMonitor) enqueueFromAnalysis(ctx context.Context, info downloader.VideoInfo, channel channels.Channel, analysis Analysis, ledgerID string) error {
	videoID := info.ID
	title := info.Title
	videoURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	// ── Drive folder resolution ──────────────────────────────────────
	driveFolderID := channel.DriveFolderID
	if driveFolderID == "" && m.cfg != nil {
		driveFolderID = m.cfg.Drive.ClipsFolder()
	}

	// ── Log prefix ───────────────────────────────────────────────────
	channelHandle := extractChannelHandle(channel.ChannelURL)
	if channelHandle == "" {
		channelHandle = "unknown"
	}
	m.log.Debug("enqueueFromAnalysis: dispatching via JobEnqueuer",
		zap.String("video_id", videoID),
		zap.String("channel_handle", channelHandle),
		zap.Int("segments", len(analysis.Segments)),
		zap.String("ledger_id", ledgerID))

	// ── Delegate to the JobEnqueuer port ─────────────────────────────
	if m.enqueuer == nil {
		m.log.Warn("enqueueFromAnalysis: enqueuer port not wired, cannot emit extract job",
			zap.String("video_id", videoID))
		// Defensive ledger update on missing composition: record the
		// misconfiguration as a rejection so the ledger's audit trail
		// reflects the missing-emit case.
		if m.discoveries != nil && ledgerID != "" {
			_ = m.discoveries.MarkRejected(ctx, ledgerID, "enqueuer port not wired", false)
		}
		return fmt.Errorf("enqueueFromAnalysis: enqueuer port not wired for video=%q ledger_id=%q", videoID, ledgerID)
	}
	if emitErr := m.enqueuer.EnqueueExtract(ctx, EnqueueExtractRequest{
		VideoID:       videoID,
		Title:         title,
		URL:           videoURL,
		Group:         analysis.Category,
		DriveFolderID: driveFolderID,
		Segments:      analysis.Segments,
		Channel:       channel,
	}); emitErr != nil {
		// Commit 3/6 (P1 #5/#6/#7): compute retryable from a typed
		// transient-error predicate. A transient error (timeout,
		// 429, connection refused, EOF) → retryable=true, the ledger
		// row will re-enter TryReserve when next_retry_at fires. A
		// terminal error (validation reject, payload marshal failure,
		// missing channel-id from the jobs.broker) → retryable=false,
		// the row's state is rejected_terminal and no further retries.
		//
		// The repository must NOT know the transient taxonomy
		// (domain purity); the caller (this file) is the boundary.
		retryable := isTransientEnqueueError(emitErr)
		m.log.Error("enqueueFromAnalysis: JobEnqueuer.EnqueueExtract failed, recording rejection on ledger",
			zap.String("video_id", videoID),
			zap.String("ledger_id", ledgerID),
			zap.Bool("retryable", retryable),
			zap.Error(emitErr))
		if m.discoveries != nil && ledgerID != "" {
			if markErr := m.discoveries.MarkRejected(ctx, ledgerID, emitErr.Error(), retryable); markErr != nil {
				m.log.Error("enqueueFromAnalysis: MarkRejected failed",
					zap.String("ledger_id", ledgerID),
					zap.Error(markErr))
			}
		}
		return fmt.Errorf("enqueueFromAnalysis: emit failed for video=%q ledger_id=%q retryable=%v: %w",
			videoID, ledgerID, retryable, emitErr)
	}

	// Successful broker emit → flip the ledger row from `pending` to
	// `enqueued` so the cycle-end MAX(discovered_at) query + the audit
	// trail both reflect that the broker side reached. Idempotent on
	// repeat: a row with enqueued=1 stays 1.
	if m.discoveries != nil && ledgerID != "" {
		if markErr := m.discoveries.MarkEnqueued(ctx, ledgerID, time.Now().UTC().Format(time.RFC3339)); markErr != nil {
			// The job IS emitted; the ledger row's outcome just didn't
			// flip. Loud log so an operator can reconcile.
			m.log.Error("enqueueFromAnalysis: MarkEnqueued failed (broker emitted, ledger stuck in pending)",
				zap.String("ledger_id", ledgerID),
				zap.Error(markErr))
		}
	}
	m.log.Info("enqueued youtube_clip.extract job",
		zap.String("video_id", videoID),
		zap.String("title", title),
		zap.Int("segments", len(analysis.Segments)),
		zap.String("destination_group", analysis.Category),
		zap.String("ledger_id", ledgerID))
	return nil
}

// HandleChannelSyncJob is the durable youtube.channel.sync handler.
//
// PR 3 (June 2026): the monitor enqueues one sync job per channel instead
// of processing channels inline. The job handler performs the channel
// check, video filtering, and clip extraction enqueue.
//
// Blocco 1a (July 2026): the handler now routes through safeCheckChannel +
// recordCheckOutcome — the same single canonical outcome path the
// scheduler uses. Pre-fix the handler discarded checkChannel's return
// values and unconditionally wrote MarkChecked(Success=true), hiding
// yt-dlp failures, panics, and SQLite errors from the job status. Now:
//   - checkErr != nil → job returns error + status:"failed", and
//     recordCheckOutcome writes Success=false with the error message
//     so the exponential backoff curve applies.
//   - checkErr == nil → job returns status:"synced" as before.
func (m *ChannelMonitor) HandleChannelSyncJob(ctx context.Context, j *jobservice.Job, tools *jobtools.JobTools) (map[string]any, error) {
	var payload struct {
		ChannelID string `json:"channel_id"`
	}
	if len(j.Payload) > 0 {
		if err := json.Unmarshal(j.Payload, &payload); err != nil {
			return nil, fmt.Errorf("channel_sync: invalid payload: %w", err)
		}
	}
	if payload.ChannelID == "" {
		return nil, fmt.Errorf("channel_sync: missing channel_id in payload")
	}

	ch, err := m.channelsSvc.GetByID(ctx, payload.ChannelID)
	if err != nil {
		return nil, fmt.Errorf("channel_sync: channel lookup failed for %q: %w", payload.ChannelID, err)
	}

	m.log.Info("handling youtube.channel.sync job",
		zap.String("job_id", j.ID),
		zap.String("channel_id", payload.ChannelID),
		zap.String("channel_url", ch.ChannelURL))

	// Route through safeCheckChannel (panic-recovery → typed error) +
	// recordCheckOutcome (MarkChecked with Success/LastError/backoff).
	// This is the same canonical outcome path the scheduler uses in
	// checkDueChannels; the job handler no longer duplicates the logic.
	result, checkErr := m.safeCheckChannel(ctx, ch)
	m.log.Info("channel sync job check completed",
		zap.String("channel_id", payload.ChannelID),
		zap.Bool("success", checkErr == nil),
		zap.Int("videos_discovered", result.VideosDiscovered),
		zap.Int("videos_enqueued", result.VideosEnqueued))

	// Persist the outcome through the canonical recordCheckOutcome path.
	// On recordCheckOutcome failure, we still return checkErr so the job
	// status reflects the real infra outcome; the MarkChecked write
	// failure is surfaced as a secondary error.
	if recErr := m.recordCheckOutcome(ctx, ch, checkErr); recErr != nil {
		m.log.Error("failed to mark channel checked after sync job",
			zap.String("channel_id", ch.ID),
			zap.Error(recErr))
		if checkErr != nil {
			return map[string]any{
				"channel_id": payload.ChannelID,
				"status":     "failed",
				"error":      checkErr.Error(),
			}, fmt.Errorf("channel_sync: check failed AND recordCheckOutcome failed: check=%w, record=%w", checkErr, recErr)
		}
		return nil, fmt.Errorf("channel_sync: recordCheckOutcome failed for %q: %w", payload.ChannelID, recErr)
	}

	if checkErr != nil {
		return map[string]any{
			"channel_id": payload.ChannelID,
			"status":     "failed",
			"error":      checkErr.Error(),
		}, checkErr
	}

	return map[string]any{
		"channel_id": payload.ChannelID,
		"status":     "synced",
	}, nil
}

// RegisterChannelSyncHandler registers the youtube.channel.sync job handler
// with the job service. Called from the lifecycle after the monitor is
// initialized (PR 3, June 2026).
func (m *ChannelMonitor) RegisterChannelSyncHandler(jobsSvc *jobtools.Service) {
	if jobsSvc == nil || m == nil {
		return
	}
	jobsSvc.RegisterHandler(jobservice.TypeYouTubeChannelSync, m.HandleChannelSyncJob)
	m.log.Info("registered youtube.channel.sync job handler")
}

// tryReserve is the per-channel budget check (atomic CAS). It is
// consumed only AFTER the AI gate (see discovery.go::processVideo) so
// a transient transcript miss does not waste a budget slot.
func tryReserve(counter *atomic.Int32, limit int) bool {
	for {
		current := counter.Load()
		if current >= int32(limit) {
			return false
		}
		if counter.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

// isTransientEnqueueError returns true when the EnqueueExtract error
// is a transient infrastructure failure (timeout / 429 / 5xx /
// connection-class errors) that warrants a retry on a later cycle.
//
// Commit 3/6 (P1 #5/#6/#7): the predicate runs at the repository
// boundary (monitor package enqueue.go) so the persistence layer
// (YoutubeDiscoveriesRepository) stays free of domain knowledge.
// Any error not matching the transient taxonomy is treated as terminal:
// a lease-rejection, payload-marshal failure, or business-rule reject
// will simply re-fail on retry, so the row is marked rejected_terminal
// with retryable=false to avoid wasting scheduler cycles on it.
//
// The substring taxonomy mirrors the one used in
// pkg/retry.DoWithValue + the Channel Monitor's own retry.Do policy
// (scheduler.go::checkChannel) so retries behave consistently across
// the pipeline.
func isTransientEnqueueError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	for _, s := range []string{
		"timeout",
		"connection refused",
		"connection reset",
		"eof",
		"429",
		"503",
		"502",
		"504",
		"rate limit",
		"quota exceeded",
		"temporarily unavailable",
		"resource temporarily unavailable",
	} {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}
