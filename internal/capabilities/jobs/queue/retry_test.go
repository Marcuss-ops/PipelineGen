package queue

import (
	"testing"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestRetryBudgetAvailable(t *testing.T) {
	if !RetryBudgetAvailable(0, 1) {
		t.Fatal("retry should be available below the limit")
	}
	if RetryBudgetAvailable(1, 1) {
		t.Fatal("retry must not be available at the limit")
	}
}

func TestRetryDue(t *testing.T) {
	now := time.Now().UTC()
	if RetryDue(&job.Job{RetryCount: 1, MaxRetries: 3, UpdatedAt: now.Add(-time.Second)}, now) {
		t.Fatal("retry should not be due before the first backoff")
	}
	if !RetryDue(&job.Job{RetryCount: 1, MaxRetries: 3, UpdatedAt: now.Add(-3 * time.Second)}, now) {
		t.Fatal("retry should be due after the first backoff")
	}
	if RetryDue(&job.Job{RetryCount: 3, MaxRetries: 3, UpdatedAt: now.Add(-time.Hour)}, now) {
		t.Fatal("retry must not be due after budget exhaustion")
	}
	if RetryDue(nil, now) {
		t.Fatal("nil job must not be due")
	}
}
