package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// IsTerminalState reports whether a job state is terminal for a producer
// waiting on completion. `rendered` is deliberately NOT terminal: the render
// is durable but external publication is still pending, and the job remains
// re-claimable for a publication-only retry, so a producer that stopped
// waiting on it could observe an artifact that is never published.
//
// The terminal SET lives once, on the canonical wire type: this is a thin
// adapter that keeps the service-local spelling while delegating the decision
// to client.IsTerminalState, so the queue and the producers can never disagree
// about which states end a wait.
func IsTerminalState(state model.State) bool {
	return client.IsTerminalState(state)
}

// waitPollFloor/waitPollCeiling bound the multi-replica fallback re-poll of
// WaitState. The wake-up signal is local to one queue replica (the Notifier
// plus the local LISTEN session), so a terminal transition that happened on
// another replica is observed by the bounded re-poll. The floor keeps that
// cross-replica latency small; the ceiling keeps a fleet of waiting producers
// from polling the store at a fixed rate.
const (
	waitPollFloor   = 250 * time.Millisecond
	waitPollCeiling = 2 * time.Second
)

// WaitState blocks until the job reaches a terminal state (completed, failed
// or cancelled) or maxWait elapses, and returns the last observed job. It is
// the producer-side mirror of WaitAndClaim over the SAME single Notifier:
//
//   - the signal carries no data; every wake re-reads the store, which remains
//     the only source of truth;
//   - a bounded re-poll (waitPollFloor → waitPollCeiling) covers the case where
//     the terminal transition was published by another queue replica or the
//     wake-up was missed, so signal loss can never stall a producer;
//   - on deadline it returns the current (non-terminal) job rather than an
//     error, so the caller can decide whether to keep waiting.
//
// A job that does not exist fails immediately with the repository's
// ErrNotFound; waiting on an unknown id can never become terminal.
func (s *Service) WaitState(ctx context.Context, id string, maxWait time.Duration) (*model.Job, error) {
	if id == "" {
		return nil, fmt.Errorf("job id is required")
	}
	if maxWait <= 0 {
		maxWait = 25 * time.Second
	}
	job, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if IsTerminalState(job.State) {
		return job, nil
	}

	deadline := time.NewTimer(maxWait)
	defer deadline.Stop()
	wake := s.notify.Done()
	pollDelay := waitPollFloor
	// Reused poll timer: time.After in this loop would allocate and leak a
	// timer per iteration until GC.
	pollTimer := time.NewTimer(pollDelay)
	defer pollTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			// The bounded wait elapsed: hand back the last observed state.
			// The caller owns the decision to wait again.
			return job, nil
		case <-wake:
			// Spurious or competing wake-up: re-read the store at the fresh
			// floor so the waiter stays responsive.
			wake = s.notify.Done()
			pollDelay = waitPollFloor
			if !pollTimer.Stop() {
				select {
				case <-pollTimer.C:
				default:
				}
			}
			pollTimer.Reset(pollDelay)
		case <-pollTimer.C:
			pollDelay *= 2
			if pollDelay > waitPollCeiling {
				pollDelay = waitPollCeiling
			}
			pollTimer.Reset(pollDelay)
		}
		job, err = s.repo.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if IsTerminalState(job.State) {
			return job, nil
		}
	}
}
