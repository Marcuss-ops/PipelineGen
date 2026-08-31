package queue

import (
	"context"
	"fmt"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Claimer is the narrow persistence port needed by the queue claim loop.
// Implementations own lease acquisition; this package owns only polling policy.
type Claimer interface {
	ClaimNext(ctx context.Context, workerID string, leaseTTL time.Duration, types []string) (*job.Job, error)
}

// ValidateClaimCapabilities enforces the fail-closed queue contract: an empty
// capability list means that the caller can claim nothing, never "all jobs".
func ValidateClaimCapabilities(types []string) error {
	if len(types) == 0 {
		return fmt.Errorf("worker has no advertised capabilities")
	}
	return nil
}

// NormalizeWait applies the queue's default wait when a caller supplies a
// non-positive value. A positive value is preserved unchanged.
func NormalizeWait(wait, defaultWait time.Duration) time.Duration {
	if wait > 0 {
		return wait
	}
	if defaultWait > 0 {
		return defaultWait
	}
	return 20 * time.Second
}

// ClaimUntil polls a Claimer until a job is available, the wait budget expires,
// or the context is cancelled. Lease persistence and errors remain owned by the
// Claimer; this function only coordinates timing and cancellation.
func ClaimUntil(ctx context.Context, claimer Claimer, workerID string, leaseTTL, wait time.Duration, types []string) (*job.Job, error) {
	if err := ValidateClaimCapabilities(types); err != nil {
		return nil, err
	}
	if claimer == nil {
		return nil, fmt.Errorf("claim port is nil")
	}
	wait = NormalizeWait(wait, 20*time.Second)
	leaseTTL = NormalizeWait(leaseTTL, wait)
	deadline := time.NewTimer(wait)
	defer deadline.Stop()

	for {
		claimed, err := claimer.ClaimNext(ctx, workerID, leaseTTL, types)
		if err != nil {
			return nil, err
		}
		if claimed != nil {
			return claimed, nil
		}

		poll := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			poll.Stop()
			return nil, ctx.Err()
		case <-deadline.C:
			poll.Stop()
			return nil, nil
		case <-poll.C:
		}
	}
}
