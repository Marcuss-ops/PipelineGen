// Package monitor — enqueue.go: durable-job emission + channel cursor update.
//
// Step 9 (June 2026, Channel Monitor Blocco 6 architectural rewrite):
// the package is now exactly 5 production files (scheduler.go,
// discovery.go, analyzer.go, enqueue.go, ports.go). This file owns:
//
//   - enqueueFromAnalysis: builds the EnqueueExtractRequest from the
//     analyzeVideo result, resolves the DriveFolderID (channel-level
//     override + ClipsFolder global fallback), and delegates the actual
//     marshal + jobs.Enqueue call to the JobEnqueuer port.
//   - tryReserve: the per-channel budget CAS, only consumed after
//     passing the AI gate (see discovery.go processVideo).
//
// Commit H Phase 2 (June 2026): the durable channel-sync method method + the
// its job-type binding binding were removed. jobs of type
// TypeYouTubeChannelSync still register in jobs/registry.go but have
// no handler — the canonical scheduler uses the channels_service +
// monitor.tick path (see scheduler.go) and emits youtube_clip.extract
// jobs directly via the JobEnqueuer port, bypassing the durable
// channel_sync indirection.
//
// FASE 1.2 (July 2026): Outbox transazionale. Il percorso non chiama più
// direttamente broker + MarkEnqueued in due passi non atomici. Invece:
//
//   1. TryReserve vince → row è in stato 'pending'
//   2. discoverys.ReserveAndEnqueueOutbox atomicamente:
//      UPDATE youtube_discoveries SET state='pending-delivery'
//      INSERT INTO job_outbox (discovery_id, event_key, payload_json)
//      COMMIT
//   3. JobOutboxDispatcher (background goroutine) polla job_outbox:
//      claim → chiama broker.Enqueue → on success MarkCompleted
//      (outbox+discovery atomici) → on failure MarkFailed con backoff
//
// L'idempotency key è deterministica:
//   "youtube-extract:{discovery_id}:{policy_version}"
//
// Questo garantisce che anche se il dispatcher crasha tra broker publish
// e outbox ACK, il retry userà la stessa event_key e l'ActiveKey dedup
// del broker previene la creazione di job duplicati.
package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	sqlassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
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
func (m *ChannelMonitor) enqueueFromAnalysis(ctx context.Context, info VideoInfo, channel channels.Channel, analysis Analysis, ledgerID string) error {
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

	// ── Blocco 3 (July 2026, audit P0 #2): transactional outbox ─────
	// Replaces the pre-Blocco-3 two-step flow:
	//   1. EnqueueExtract (broker emit)
	//   2. MarkEnqueued (ledger flip)
	// with a single atomic CommitEnqueueOutbox that does BOTH in
	// one SQLite transaction: MarkEnqueued + INSERT outbox entry.
	// The outbox drainer goroutine (startOutboxDrainer in scheduler.go)
	// picks up the entry and dispatches it to the durable-jobs broker
	// asynchronously. This eliminates the torn-write path where the
	// broker had the job but the ledger stayed 'pending'.

	if m.discoveries == nil {
		m.log.Warn("enqueueFromAnalysis: discoveries port not wired, cannot commit outbox",
			zap.String("video_id", videoID))
		return fmt.Errorf("enqueueFromAnalysis: discoveries port not wired for video=%q", videoID)
	}

	// Build the EnqueueExtractRequest that the outbox drainer will
	// deserialize and dispatch.
	extractReq := EnqueueExtractRequest{
		VideoID:       videoID,
		Title:         title,
		URL:           videoURL,
		Group:         analysis.Category,
		DriveFolderID: driveFolderID,
		Segments:      analysis.Segments,
		Channel:       channel,
	}
	payloadJSON, marshalErr := json.Marshal(extractReq)
	if marshalErr != nil {
		m.log.Error("enqueueFromAnalysis: failed to marshal extract request",
			zap.String("video_id", videoID),
			zap.Error(marshalErr))
		if ledgerID != "" {
			_ = m.discoveries.MarkRejected(ctx, ledgerID, marshalErr.Error(), false)
		}
		return fmt.Errorf("enqueueFromAnalysis: marshal payload for video=%q: %w", videoID, marshalErr)
	}

	// Idempotency key: youtube-extract:{discovery_id}:{policy_version}
	// The UNIQUE constraint on idempotency_key prevents duplicate
	// outbox entries for the same (discovery_id, policy_version).
	idempotencyKey := fmt.Sprintf("youtube-extract:%s:%s", ledgerID, ChannelMonitorPolicyVersion)

	enqueuedAt := time.Now().UTC().Format(time.RFC3339)
	m.log.Debug("enqueueFromAnalysis: committing outbox entry",
		zap.String("video_id", videoID),
		zap.String("channel_handle", channelHandle),
		zap.Int("segments", len(analysis.Segments)),
		zap.String("ledger_id", ledgerID),
		zap.String("idempotency_key", idempotencyKey))

	// Atomic MarkEnqueued + outbox INSERT. On success, the ledger is
	// flipped to 'enqueued' and the outbox entry is pending.
	// On duplicate idempotency_key, returns nil (idempotent).
	if commitErr := m.discoveries.CommitEnqueueOutbox(ctx, ledgerID, enqueuedAt, idempotencyKey, string(payloadJSON)); commitErr != nil {
		m.log.Error("enqueueFromAnalysis: CommitEnqueueOutbox failed",
			zap.String("video_id", videoID),
			zap.String("ledger_id", ledgerID),
			zap.Error(commitErr))
		// If the error is an ErrStateConflict (row not in pending/analyzing),
		// record as terminal — the row is in an unexpected state.
		retryable := isTransientEnqueueError(commitErr)
		if errors.Is(commitErr, sqlassets.ErrStateConflict) {
			retryable = false
		}
		if ledgerID != "" {
			if markErr := m.discoveries.MarkRejected(ctx, ledgerID, commitErr.Error(), retryable); markErr != nil {
				m.log.Error("enqueueFromAnalysis: MarkRejected failed after CommitEnqueueOutbox failure",
					zap.String("ledger_id", ledgerID),
					zap.Error(markErr))
			}
		}
		return fmt.Errorf("enqueueFromAnalysis: commit outbox for video=%q ledger_id=%q: %w",
			videoID, ledgerID, commitErr)
	}

	m.log.Info("committed outbox entry for youtube_clip.extract",
		zap.String("video_id", videoID),
		zap.String("title", title),
		zap.Int("segments", len(analysis.Segments)),
		zap.String("destination_group", analysis.Category),
		zap.String("ledger_id", ledgerID))
	return nil
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
