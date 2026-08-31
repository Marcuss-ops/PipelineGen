package scheduling

import (
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// RetryPolicy is the canonical server-side retry schedule used by workers.
type RetryPolicy struct {
	InitialBackoff time.Duration
	BackoffFactor  float64
	MaxBackoff     time.Duration
}

// DefaultRetryPolicy is the deterministic retry schedule for job execution.
var DefaultRetryPolicy = RetryPolicy{
	InitialBackoff: 2 * time.Second,
	BackoffFactor:  2.0,
	MaxBackoff:     30 * time.Second,
}

// RetryAllowed reports whether another retry can be scheduled.
func RetryAllowed(retryCount, maxRetries int) bool {
	return retryCount < maxRetries
}

// RetryBackoff returns the persisted server-side delay for a retry attempt.
func RetryBackoff(retryCount int, policy RetryPolicy) time.Duration {
	return retry.BackoffFor(retryCount, retry.Options{
		InitialBackoff: policy.InitialBackoff,
		BackoffFactor:  policy.BackoffFactor,
		MaxBackoff:     policy.MaxBackoff,
	})
}

// RetryDecision describes the scheduling outcome for a failed job.
type RetryDecision int

const (
	RetryTerminal RetryDecision = iota
	RetryScheduled
)

// DecideRetry applies the retry budget to a failed job without performing any
// persistence side effect.
func DecideRetry(j *job.Job) RetryDecision {
	if j == nil || !RetryAllowed(j.RetryCount, j.MaxRetries) {
		return RetryTerminal
	}
	return RetryScheduled
}

// RetryDue reports whether a retry-wait job has reached its next retry slot.
func RetryDue(j *job.Job, now time.Time) bool {
	if j == nil || !RetryAllowed(j.RetryCount, j.MaxRetries) {
		return false
	}
	backoff := RetryBackoff(j.RetryCount-1, DefaultRetryPolicy)
	return now.UTC().Sub(j.UpdatedAt.UTC()) >= backoff
}
