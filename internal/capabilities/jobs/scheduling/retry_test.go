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
		{name: "not scheduled", retryCount: 0, maxRetries: 3, want: false},
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
			name: "deferral at retry_count 0 with no hint still waits for a retry (never a silent requeue)",
			job:  &job.Job{Status: job.StatusRetryWait, RetryCount: 0, MaxRetries: 3, UpdatedAt: now.Add(-time.Hour)},
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
