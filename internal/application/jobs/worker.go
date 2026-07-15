// Package jobs owns the in-process worker lifecycle.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

var workerIDPrefix string

func init() {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	workerIDPrefix = fmt.Sprintf("%s_%d", host, os.Getpid())
}

// Worker polls the canonical job.Store and dispatches claimed jobs.
type Worker struct {
	id         string
	repo       job.Store
	dispatcher *Dispatcher
	log        *zap.Logger
	leaseTTL   time.Duration
	pollEvery  time.Duration
	backoff    BackoffConfig
	types      []string
	notifier   QueueNotifier
	reg        *Registry
	timeouts   TimeoutMap
	broker     CompletionPort
}

// WithRegistry attaches the immutable per-job timeout/retry registry.
func (w *Worker) WithRegistry(registry *Registry) *Worker {
	w.reg = registry
	if registry != nil {
		w.timeouts = registry.Compose()
	} else {
		w.timeouts = nil
	}
	return w
}

// WithBroker attaches the narrow artifact-completion port.
func (w *Worker) WithBroker(broker CompletionPort) *Worker {
	w.broker = broker
	return w
}

func (w *Worker) jobTimeoutFor(jobType string) time.Duration {
	if w.timeouts != nil {
		if duration, ok := w.timeouts[jobType]; ok && duration > 0 {
			return duration
		}
	}
	return 10 * time.Minute
}

func (w *Worker) maxRetriesFor(jobType string) int {
	if w.reg != nil {
		return w.reg.DefaultMaxRetries(jobType)
	}
	return 3
}

// Start runs the adaptive polling loop until ctx is cancelled.
func (w *Worker) Start(ctx context.Context) {
	w.log.Info("worker started",
		zap.String("worker_id", w.id),
		zap.Duration("base_poll_every", w.pollEvery),
		zap.Duration("max_backoff", w.backoff.MaxBackoff),
		zap.Int("consecutive_empty_threshold", w.backoff.ConsecutiveEmptyThreshold),
	)

	currentBackoff := w.pollEvery
	consecutiveEmpty := 0
	initialJitter := jitterDuration(w.pollEvery/4, 1.0)
	if !w.sleepBackoff(ctx, w.pollEvery+initialJitter) {
		w.log.Info("worker stopped (ctx before first claim)", zap.String("worker_id", w.id))
		return
	}

	for {
		if ctx.Err() != nil {
			w.log.Info("worker stopped",
				zap.String("worker_id", w.id),
				zap.Duration("current_backoff", currentBackoff),
				zap.Int("consecutive_empty", consecutiveEmpty),
			)
			return
		}

		claimed, err := w.repo.ClaimNext(ctx, w.id, w.leaseTTL, w.types)
		if err != nil {
			if errors.Is(err, job.ErrTransitionConflict) {
				if !w.sleepBackoff(ctx, w.effectiveSleep(w.pollEvery)) {
					w.log.Info("worker stopped", zap.String("worker_id", w.id))
					return
				}
				continue
			}
			w.log.Error("failed to claim next job", zap.Error(err))
			if !w.sleepBackoff(ctx, w.effectiveSleep(w.pollEvery)) {
				w.log.Info("worker stopped", zap.String("worker_id", w.id))
				return
			}
			continue
		}

		if claimed == nil {
			consecutiveEmpty++
			metrics.WorkerIdleTicksTotal.Inc()
			if w.backoff.ConsecutiveEmptyThreshold > 0 &&
				consecutiveEmpty > w.backoff.ConsecutiveEmptyThreshold {
				previous := currentBackoff
				next := previous * 2
				if next > w.backoff.MaxBackoff {
					next = w.backoff.MaxBackoff
				}
				if next > previous {
					metrics.WorkerBackoffEventsTotal.Inc()
					currentBackoff = next
					w.log.Debug("worker backoff escalated",
						zap.String("worker_id", w.id),
						zap.Int("consecutive_empty", consecutiveEmpty),
						zap.Duration("from", previous),
						zap.Duration("to", next),
					)
				}
			}
			if !w.sleepBackoff(ctx, w.effectiveSleep(currentBackoff)) {
				w.log.Info("worker stopped", zap.String("worker_id", w.id))
				return
			}
			continue
		}

		if consecutiveEmpty > 0 || currentBackoff != w.pollEvery {
			w.log.Debug("worker backoff reset on successful claim",
				zap.String("worker_id", w.id),
				zap.Int("previous_consecutive_empty", consecutiveEmpty),
				zap.Duration("previous_backoff", currentBackoff),
			)
		}
		consecutiveEmpty = 0
		currentBackoff = w.pollEvery
		w.runJob(ctx, claimed)
	}
}

func mapToRawMessage(value map[string]any) json.RawMessage {
	if value == nil {
		return json.RawMessage("{}")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage("{}")
	}
	return encoded
}
