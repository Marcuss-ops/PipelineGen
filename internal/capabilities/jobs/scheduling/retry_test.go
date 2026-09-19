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
