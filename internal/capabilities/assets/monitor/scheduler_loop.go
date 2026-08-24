// Package monitor — scheduler_loop.go: lease-aware scheduler loop.
//
// God-object decomposition (PR-GODOBJ-2, July 2026): extracted from
// scheduler.go per the action-plan split topology. This file owns:
//   - Start: the main scheduler loop (ticker + ClaimDue + dispatch).
//   - runSchedulerCycle: claims due channels and dispatches them.
//
// The scheduler never touches os/exec, OllamaClient, or VTT regex
// directly — those concerns cross the package boundary through the
// typed ports on ChannelMonitor.
package monitor

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	channels "github.com/Marcuss-ops/PipelineGen/internal/capabilities/channels"
)

// Start begins the channel monitoring process.
//
// PR 5 (June 2026): job-based sync via ClaimDue/MarkChecked.
// PR 7 (June 2026): typed Channel DTO.
// God-object split (July 2026): extracted from scheduler.go. The
// previous "ListEnabled initial setup" shortcut is preserved → first
// tick goes through runSchedulerCycle → ClaimDue → lease →
// checkChannel → MarkChecked. The shortcut previously ran checks
// OUTSIDE that path (no worker_id, no lease, no
// consecutive_failures increment) which sabotaged the exponential
// backoff math in nextCheckTime.
func (m *ChannelMonitor) Start(ctx context.Context) {
	m.log.Info("Starting channel monitor (god-object split: 6-file topology)")

	if m.channelsSvc == nil {
		m.log.Error("Channel monitor: channels service not wired, cannot start")
		return
	}

	// Blocco 3 (July 2026): start the outbox drainer. It shares the
	// parent ctx so shutdown propagates cleanly. The drainer runs
	// independently of the scheduler tick — it dispatches entries
	// that were committed by checkChannel's per-video workers.
	go m.startOutboxDrainer(ctx)

	policy := m.policyOrDefault()
	m.log.Info("Channel monitor entering scheduling loop (first check via runSchedulerCycle)",
		zap.Duration("tick", policy.TickInterval),
		zap.Duration("lease", policy.LeaseDuration),
		zap.Int("claim_limit", policy.ClaimLimit))

	ticker := time.NewTicker(policy.TickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.runSchedulerCycle(ctx)
		case <-ctx.Done():
			m.log.Info("Channel monitor context cancelled")
			return
		}
	}
}

// runSchedulerCycle claims due channels and dispatches them to checkDueChannels.
// Reads TickInterval/LeaseDuration/ClaimLimit/WorkerIDPrefix from the policy.
func (m *ChannelMonitor) runSchedulerCycle(ctx context.Context) {
	policy := m.policyOrDefault()
	now := time.Now()
	nowStr := now.Format(time.RFC3339)
	leaseUntil := now.Add(policy.LeaseDuration).Format(time.RFC3339)
	// workerID = WorkerIDPrefix + "-" + nanos-mod-100000. The prefix is
	// a knob (multi-tenant deployments may want a custom prefix to
	// disambiguate lease_owner rows across instances). The modulo
	// keeps the ID short enough to fit comfortably in DIAG
	// spreadsheets; raise the modulus when more workers are expected
	// (future PR).
	workerID := fmt.Sprintf("%s-%d", policy.WorkerIDPrefix, time.Now().UnixNano()%100000)

	result, err := m.channelsSvc.ClaimDue(ctx, channels.ClaimDueCommand{
		Now:        nowStr,
		WorkerID:   workerID,
		LeaseUntil: leaseUntil,
		Limit:      policy.ClaimLimit,
	})
	if err != nil {
		m.log.Error("Failed to claim due channels", zap.Error(err))
		return
	}

	if len(result.Channels) == 0 {
		return
	}

	m.log.Info("Claimed due channels for checking", zap.Int("count", len(result.Channels)))

	m.checkDueChannels(ctx, result.Channels)
}
