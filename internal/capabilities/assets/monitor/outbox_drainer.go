// Package monitor — outbox_drainer.go: outbox drain loop with lease-
// based claim and reclamation.
//
// God-object decomposition (PR-GODOBJ-2, July 2026): extracted from
// scheduler.go per the action-plan split topology. This file owns:
//   - startOutboxDrainer: background ticker that drains the outbox.
//   - drainOutboxOnce: claims and dispatches a single batch.
//   - dispatchOutboxEntry: deserializes and emits one outbox entry.
//
// Blocco 3 (July 2026, audit P0 #2): the drainer replaces the
// pre-Blocco-3 inline EnqueueExtract call in enqueueFromAnalysis.
// The per-video worker now calls CommitEnqueueOutbox (atomic
// MarkEnqueued + INSERT outbox in one transaction); this drainer
// handles the actual broker emission asynchronously. On failure,
// the outbox entry is marked failed so the operator's dashboard
// can surface undelivered entries.
//
// FASE 3.7 Commit 1b (2026-07-04): dispatchOutboxEntry's parameter
// is `monitor.OutboxEntry` (was `assetsdb.OutboxEntry`) — the
// composition-root adapter in
// `internal/app/lifecycle.go::monitorDiscoveriesAdapter` translates
// the infra-row projection into the monitor canonical projection so
// this file NEVER imports `internal/platform/*`. Zero infra
// import in this file.
package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// startOutboxDrainer is the background goroutine that drains pending
// outbox entries and dispatches them to the durable-jobs broker.
//
// Tick interval: 5s (hardcoded — tighter than the scheduler's
// default 30s because the drainer only reads a pre-committed
// outbox; no yt-dlp subprocess or AI gate is involved).
func (m *ChannelMonitor) startOutboxDrainer(ctx context.Context) {
	m.log.Info("outbox drainer started (Blocco 3)")

	const drainInterval = 5 * time.Second
	ticker := time.NewTicker(drainInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.drainOutboxOnce(ctx)
		case <-ctx.Done():
			m.log.Info("outbox drainer stopped")
			return
		}
	}
}

// drainOutboxOnce drains a single batch of pending outbox entries
// and reclaims any stuck-in-dispatching entries with expired leases.
//
// Step 7/12: added DrainDispatched reclamation and lease-based claim.
func (m *ChannelMonitor) drainOutboxOnce(ctx context.Context) {
	if m.discoveries == nil {
		return
	}

	// Lease identity: unique per drain cycle so a crashed drainer's
	// lease can be reclaimed by the next cycle.
	now := time.Now().UTC()
	leaseID := fmt.Sprintf("outbox-drainer-%d", now.UnixNano()%100000)
	// The SQLite outbox compares lease_until with datetime('now').
	// Use SQLite's space-separated timestamp format so expired leases
	// are reclaimable; RFC3339's 'T' would sort after SQLite's space.
	leaseUntil := now.Add(30 * time.Second).Format("2006-01-02 15:04:05")

	// Step 1: Reclaim stuck entries (crashed drainer recovery).
	reclaimed, err := m.discoveries.DrainDispatched(ctx, 5, leaseID, leaseUntil)
	if err != nil {
		m.log.Warn("outbox drainer: DrainDispatched reclaim failed", zap.Error(err))
	} else if len(reclaimed) > 0 {
		m.log.Info("outbox drainer: reclaimed stuck entries", zap.Int("count", len(reclaimed)))
	}

	// Step 2: Atomically claim pending entries.
	entries, err := m.discoveries.DrainPendingOutbox(ctx, 10, leaseID, leaseUntil)
	if err != nil {
		m.log.Warn("outbox drainer: DrainPendingOutbox failed", zap.Error(err))
		return
	}
	if len(entries) == 0 {
		return
	}

	m.log.Debug("outbox drainer: draining entries", zap.Int("count", len(entries)))

	for _, entry := range entries {
		if err := m.dispatchOutboxEntry(ctx, entry); err != nil {
			m.log.Error("outbox drainer: entry dispatch did not complete durably",
				zap.Int64("outbox_id", entry.ID),
				zap.Error(err))
		}
	}
}

// dispatchOutboxEntry deserializes the outbox payload and emits a
// durable job via the JobEnqueuer port. On success, marks the outbox
// entry as dispatched; on failure, marks it as failed.
func (m *ChannelMonitor) dispatchOutboxEntry(ctx context.Context, entry OutboxEntry) error {
	if m.enqueuer == nil {
		m.log.Warn("outbox drainer: enqueuer port not wired, cannot dispatch entry",
			zap.Int64("outbox_id", entry.ID),
			zap.String("discovery_id", entry.DiscoveryID))
		return m.markOutboxFailed(ctx, entry.ID, "enqueuer port not wired", fmt.Errorf("outbox drainer: enqueuer port not wired for entry %d", entry.ID))
	}

	// Deserialize the payload back into an EnqueueExtractRequest.
	var req EnqueueExtractRequest
	if err := json.Unmarshal([]byte(entry.PayloadJSON), &req); err != nil {
		m.log.Error("outbox drainer: failed to deserialize payload",
			zap.Int64("outbox_id", entry.ID),
			zap.Error(err))
		return m.markOutboxFailed(ctx, entry.ID, fmt.Sprintf("deserialize: %v", err), err)
	}

	// Emit the durable job.
	if err := m.enqueuer.EnqueueExtract(ctx, req); err != nil {
		m.log.Warn("outbox drainer: EnqueueExtract failed",
			zap.Int64("outbox_id", entry.ID),
			zap.String("discovery_id", entry.DiscoveryID),
			zap.String("video_id", req.VideoID),
			zap.Error(err))
		return m.markOutboxFailed(ctx, entry.ID, err.Error(), err)
	}

	// Record successful dispatch.
	if markErr := m.discoveries.MarkOutboxDispatched(ctx, entry.ID, req.VideoID); markErr != nil {
		// The job WAS emitted, but the durable audit transition failed.
		// Return the error so the caller cannot mistake this for a fully
		// completed dispatch; retry/reconciliation remains operator-visible.
		return fmt.Errorf("outbox drainer: mark entry %d dispatched: %w", entry.ID, markErr)
	}
	m.log.Debug("outbox drainer: dispatched entry",
		zap.Int64("outbox_id", entry.ID),
		zap.String("video_id", req.VideoID),
		zap.String("discovery_id", entry.DiscoveryID))
	return nil
}

func (m *ChannelMonitor) markOutboxFailed(ctx context.Context, entryID int64, reason string, cause error) error {
	if markErr := m.discoveries.MarkOutboxFailed(ctx, entryID, reason); markErr != nil {
		return errors.Join(cause, fmt.Errorf("outbox drainer: mark entry %d failed: %w", entryID, markErr))
	}
	return cause
}
