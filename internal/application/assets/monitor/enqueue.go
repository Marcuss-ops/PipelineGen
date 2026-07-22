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
// Commit H Phase 2 (June 2026) + P0 #2 audit closure (July 2026): the
// durable channel-sync method + the job-type binding were both removed.
// The TypeYouTubeChannelSync constant + alias have been retired
// entirely from internal/domain/job/job.go + internal/application/jobs/
// registry.go (P0 #2 audit, see architecture/issues.yaml#
// PR-RETIRE-DORMANT-TYPEYOUTUBECHANNELSYNC). The canonical scheduler
// uses the channels_service + monitor.tick path (see scheduler.go)
// and emits youtube_clip.extract
// jobs directly via the JobEnqueuer port, bypassing the durable
// channel_sync indirection.
//
// FASE 1.2 (July 2026): Outbox transazionale. Il percorso non chiama più
// direttamente broker + MarkEnqueued in due passi non atomici. Invece:
//
//  1. TryReserve vince → row è in stato 'pending'
//  2. discoverys.ReserveAndEnqueueOutbox atomicamente:
//     UPDATE youtube_discoveries SET state='pending-delivery'
//     INSERT INTO job_outbox (discovery_id, event_key, payload_json)
//     COMMIT
//  3. JobOutboxDispatcher (background goroutine) polla job_outbox:
//     claim → chiama broker.Enqueue → on success MarkCompleted
//     (outbox+discovery atomici) → on failure MarkFailed con backoff
//
// L'idempotency key è deterministica:
//
//	"youtube-extract:{discovery_id}:{policy_version}"
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
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
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
		// FASE 3.7 Commit 1b (2026-07-04): pattern-match against the
		// canonical monitor-side sentinel ErrLedgerStateConflict
		// instead of the previous `youtubediscoveries.ErrStateConflict`. The
		// composition-root adapter
		// (`internal/app/lifecycle.go::monitorDiscoveriesAdapter` +
		// `mapDiscoveriesErr`) translates `youtubediscoveries.ErrStateConflict` →
		// `monitor.ErrLedgerStateConflict` via `fmt.Errorf("%w: %w", ...)`
		// multi-%w wrap (Go 1.20+). If the error chain contains the
		// infra sentinel, the monitor-side pattern-match still resolves
		// to true because errors.Is walks the entire chain. If it does
		// NOT (e.g. SQLite I/O errors that aren't a state-conflict),
		// the monitor-side match resolves to false → retryable stays
		// driven by retry.IsTransient (the canonical transient-error
		// taxonomy).
		retryable := retry.IsTransient(commitErr)
		if errors.Is(commitErr, ErrLedgerStateConflict) {
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

// isTransientEnqueueError was removed in Step 7 (July 2026) — migrated to
// pkg/retry.IsTransient. The canonical transient-error taxonomy now lives
// in pkg/retry/retry.go::transientSubstrings.
