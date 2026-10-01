package scheduling

import (
	"testing"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestRetryDueAllowsTheFinalScheduledRetry(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name       string
		retryCount int
		maxRetries int
		want       bool
	}{
		{name: "first retry", retryCount: 1, maxRetries: 3, want: true},
		{name: "final retry", retryCount: 3, maxRetries: 3, want: true},
		{name: "past retry budget", retryCount: 4, maxRetries: 3, want: false},
		// retry_count 0 IS a schedulable wait: the lease reaper moves
		// RUNNING/FINALIZING rows to RETRY_WAIT without spending the budget
		// (a reclaim is not a failure). Requiring retry_count > 0 stranded
		// restart-reclaimed jobs forever (2026-09-30 Milton stall).
		{name: "reclaimed at retry_count 0 (lease reaper)", retryCount: 0, maxRetries: 3, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := &job.Job{
				RetryCount: tc.retryCount,
				MaxRetries: tc.maxRetries,
				UpdatedAt:  now.Add(-time.Minute),
			}
			if got := RetryDue(j, now); got != tc.want {
				t.Fatalf("RetryDue() = %v, want %v (retry_count=%d max_retries=%d)", got, tc.want, tc.retryCount, tc.maxRetries)
			}
		})
	}
}

// TestRetryDueHonoursADeferralHint is the requeue half of the deferral
// contract: a deferred row states its own instant, and the sweep must use it
// instead of the retry_count-derived backoff — including at retry_count == 0,
// where a deferred job normally sits (it spent no retry).
func TestRetryDueHonoursADeferralHint(t *testing.T) {
	now := time.Now().UTC()
	window := now.Add(30 * time.Second)
	for _, tc := range []struct {
		name string
		job  *job.Job
		want bool
	}{
		{
			name: "waiting inside the stated window",
			job:  &job.Job{Status: job.StatusRetryWait, DeferredUntil: &window, UpdatedAt: now.Add(-time.Hour)},
			want: false,
		},
		{
			name: "past the stated window",
			job:  &job.Job{Status: job.StatusRetryWait, DeferredUntil: timePtr(now.Add(-time.Second)), UpdatedAt: now},
			want: true,
		},
		{
			// 2026-09-30 fix: a reclaim at retry_count 0 with NO deferral hint
			// is due after the initial backoff (the lease reaper wrote no hint).
			// The old "retry_count > 0" gate stranded these rows forever.
			name: "reclaim at retry_count 0 without hint is due after initial backoff",
			job:  &job.Job{Status: job.StatusRetryWait, RetryCount: 0, MaxRetries: 3, UpdatedAt: now.Add(-time.Hour)},
			want: true,
		},
		{
			// A reclaim written moments ago still waits out its backoff.
			name: "reclaim at retry_count 0 inside the initial backoff waits",
			job:  &job.Job{Status: job.StatusRetryWait, RetryCount: 0, MaxRetries: 3, UpdatedAt: now},
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RetryDue(tc.job, now); got != tc.want {
				t.Fatalf("RetryDue() = %v, want %v", got, tc.want)
			}
		})
	}
}

func timePtr(t time.Time) *time.Time { return &t }
