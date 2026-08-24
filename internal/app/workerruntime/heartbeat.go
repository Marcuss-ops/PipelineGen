// Package workerruntime — heartbeat.go (P1-3, June 2026).
//
// HeartbeatLoop is the background ticker that keeps the master
// informed the worker is still alive (SessionTTL renewals).
// Moved verbatim from cmd/worker/main.go::heartbeatLoop.
//
// Why a 25s ticker (vs the 90s SessionTTL): binary safety margin
// of 3.6x. If a heartbeat is delayed (network jitter, master GC
// pause, etc.) we still have 65s of TTL left before the master
// marks the worker as dead and re-queues its claimed jobs.
package workerruntime

import (
	"context"
	"time"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
)

// HeartbeatLoop runs the canonical heartbeat ticker. Flushes a
// single broker.Heartbeat(ctx, …) per tick; on error, logs a
// WARN line and continues (next tick will recover). Returns
// when ctx is cancelled.
//
// Canonical interval: 25s. Canonical SessionTTL: 90s (per the
// RegisterWorkerCommand.SessionTTL field — see registration.go).
func HeartbeatLoop(ctx context.Context, broker appjobs.Broker, workerID, sessionID string, log *zap.Logger) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := broker.Heartbeat(ctx, appjobs.HeartbeatCommand{
				WorkerID:        workerID,
				WorkerSessionID: sessionID,
				SessionTTL:      90 * time.Second,
			}); err != nil {
				log.Warn("heartbeat failed", zap.Error(err))
			}
		}
	}
}
